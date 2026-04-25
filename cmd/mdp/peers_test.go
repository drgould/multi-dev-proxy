package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/config"
)

func TestExtractPeerRefs(t *testing.T) {
	defStr := "fallback"
	svc := config.ServiceConfig{
		Env: map[string]config.EnvValue{
			"PLAIN":      {Value: "literal"},
			"LOCAL":      {Value: "${api.port}"},
			"REMOTE_INT": {Value: "https://localhost:${@backend.api.port}"},
			"REMOTE_REF": {Ref: "@backend.db.env.URL"},
			"DUP":        {Ref: "@backend.api.port", Default: &defStr},
		},
	}
	got := extractPeerRefs(svc)

	want := map[string]bool{
		"@backend.api.port.port": true,
		"@backend.db.env.URL":    true,
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d distinct refs, got %d: %v", len(want), len(got), peerRefSignatures(got))
	}
	for _, p := range got {
		if !want[p.signature()] {
			t.Errorf("unexpected ref %q in result", p.signature())
		}
	}
}

func TestExtractPeerRefsIgnoresLocal(t *testing.T) {
	svc := config.ServiceConfig{
		Env: map[string]config.EnvValue{
			"X": {Ref: "api.port"}, // local, no '@'
			"Y": {Value: "${api.port}"},
		},
	}
	if got := extractPeerRefs(svc); len(got) != 0 {
		t.Errorf("expected 0 refs (all local), got %v", peerRefSignatures(got))
	}
}

func TestResolvePeerSucceedsAndFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("repo") == "backend" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"port": 9001,
				"env":  map[string]string{"AUTH_TOKEN": "secret"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	got, ok := resolvePeer(http.DefaultClient, srv.URL, "dev", peerRef{repo: "backend", svc: "api", isEnv: false, key: "port"})
	if !ok || got != "9001" {
		t.Errorf("port lookup: got=%q ok=%v, want 9001 true", got, ok)
	}

	got, ok = resolvePeer(http.DefaultClient, srv.URL, "dev", peerRef{repo: "backend", svc: "api", isEnv: true, key: "AUTH_TOKEN"})
	if !ok || got != "secret" {
		t.Errorf("env lookup: got=%q ok=%v, want secret true", got, ok)
	}

	_, ok = resolvePeer(http.DefaultClient, srv.URL, "dev", peerRef{repo: "missing", svc: "api", isEnv: false, key: "port"})
	if ok {
		t.Error("expected 404 to return ok=false")
	}
}

func TestWatchPeerRefsFiresOnChange(t *testing.T) {
	var port atomic.Int64
	port.Store(9001)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"port": port.Load(),
			"env":  map[string]string{},
		})
	}))
	defer srv.Close()

	// Seed with an initial value of 9001 so the first poll is a no-op.
	refs := []peerRef{{repo: "backend", svc: "api", key: "port", current: "9001", found: true}}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	changed := make(chan []peerRef, 1)
	go watchPeerRefs(ctx, http.DefaultClient, srv.URL, "dev", refs, 20*time.Millisecond, changed)

	// Confirm watcher does NOT fire while the value is unchanged.
	select {
	case <-changed:
		t.Fatal("watcher fired on unchanged value")
	case <-time.After(80 * time.Millisecond):
	}

	// Flip the port; watcher should fire.
	port.Store(9999)

	select {
	case got := <-changed:
		if got[0].current != "9999" {
			t.Errorf("updated current = %q, want 9999", got[0].current)
		}
	case <-time.After(time.Second):
		t.Fatal("watcher did not fire after value changed")
	}
}

func TestNewPeerResolverIntegratesWithEnvexpand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"port": 9001,
			"env":  map[string]string{"URL": "http://backend"},
		})
	}))
	defer srv.Close()

	resolver := newPeerResolver(http.DefaultClient, srv.URL, "dev")
	if val, ok := resolver("backend", "api", false, "port"); !ok || val != "9001" {
		t.Errorf("port: got=%q ok=%v", val, ok)
	}
	if val, ok := resolver("backend", "api", true, "URL"); !ok || val != "http://backend" {
		t.Errorf("env: got=%q ok=%v", val, ok)
	}
}
