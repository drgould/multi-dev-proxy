package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
)

func newTestControlServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /__mdp/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "proxies": 1})
	})

	mux.HandleFunc("GET /__mdp/proxies", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{
				"port":       3000,
				"label":      "frontend",
				"cookieName": "__mdp_upstream_3000",
				"default":    "app/dev",
				"servers": []map[string]any{
					{"name": "app/dev", "port": 4001, "pid": 100, "group": "dev"},
				},
			},
		})
	})

	mux.HandleFunc("GET /__mdp/groups", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string][]string{
			"dev": {"app/dev"},
		})
	})

	mux.HandleFunc("GET /__mdp/services", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"name": "web", "group": "dev", "pid": 42, "port": 4001, "status": "running"},
		})
	})

	mux.HandleFunc("POST /__mdp/groups/dev/switch", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /__mdp/groups/nonexistent/switch", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	})

	mux.HandleFunc("POST /__mdp/proxies/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})

	return httptest.NewServer(mux)
}

func TestRemoteBackendSnapshot(t *testing.T) {
	srv := newTestControlServer(t)
	defer srv.Close()

	rb := &RemoteBackend{
		controlURL: srv.URL,
		client:     &http.Client{Timeout: 2 * time.Second},
		events:     make(chan orchestrator.Event, 64),
		stopPoll:   make(chan struct{}),
	}

	snap := rb.Snapshot()
	if len(snap.Proxies) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(snap.Proxies))
	}
	if snap.Proxies[0].Port != 3000 {
		t.Errorf("expected port 3000, got %d", snap.Proxies[0].Port)
	}
	if snap.Proxies[0].Default != "app/dev" {
		t.Errorf("expected default app/dev, got %q", snap.Proxies[0].Default)
	}
	if len(snap.Proxies[0].Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(snap.Proxies[0].Servers))
	}
	if len(snap.Groups) != 1 {
		t.Errorf("expected 1 group, got %d", len(snap.Groups))
	}
	if len(snap.Services) != 1 {
		t.Errorf("expected 1 service, got %d", len(snap.Services))
	}
}

func TestRemoteBackendSwitchGroup(t *testing.T) {
	srv := newTestControlServer(t)
	defer srv.Close()

	rb := &RemoteBackend{
		controlURL: srv.URL,
		client:     &http.Client{Timeout: 2 * time.Second},
		events:     make(chan orchestrator.Event, 64),
		stopPoll:   make(chan struct{}),
	}

	if err := rb.SwitchGroup("dev"); err != nil {
		t.Fatalf("SwitchGroup: %v", err)
	}
}

func TestRemoteBackendSwitchGroupNotFound(t *testing.T) {
	srv := newTestControlServer(t)
	defer srv.Close()

	rb := &RemoteBackend{
		controlURL: srv.URL,
		client:     &http.Client{Timeout: 2 * time.Second},
		events:     make(chan orchestrator.Event, 64),
		stopPoll:   make(chan struct{}),
	}

	if err := rb.SwitchGroup("nonexistent"); err == nil {
		t.Error("expected error for nonexistent group")
	}
}

func TestRemoteBackendSetDefault(t *testing.T) {
	srv := newTestControlServer(t)
	defer srv.Close()

	rb := &RemoteBackend{
		controlURL: srv.URL,
		client:     &http.Client{Timeout: 2 * time.Second},
		events:     make(chan orchestrator.Event, 64),
		stopPoll:   make(chan struct{}),
	}

	if err := rb.SetDefault(3000, "app/dev"); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
}

func TestRemoteBackendHealthCheck(t *testing.T) {
	srv := newTestControlServer(t)
	defer srv.Close()

	rb := &RemoteBackend{
		controlURL: srv.URL,
		client:     &http.Client{Timeout: 2 * time.Second},
		events:     make(chan orchestrator.Event, 64),
		stopPoll:   make(chan struct{}),
	}

	if !rb.healthCheck() {
		t.Error("expected health check to pass")
	}
}

func TestRemoteBackendHealthCheckFails(t *testing.T) {
	rb := &RemoteBackend{
		controlURL: "http://127.0.0.1:1",
		client:     &http.Client{Timeout: 100 * time.Millisecond},
		events:     make(chan orchestrator.Event, 64),
		stopPoll:   make(chan struct{}),
	}

	if rb.healthCheck() {
		t.Error("expected health check to fail on unreachable server")
	}
}

func TestRemoteBackendSnapshotUnreachable(t *testing.T) {
	rb := &RemoteBackend{
		controlURL: "http://127.0.0.1:1",
		client:     &http.Client{Timeout: 100 * time.Millisecond},
		events:     make(chan orchestrator.Event, 64),
		stopPoll:   make(chan struct{}),
	}

	snap := rb.Snapshot()
	if len(snap.Proxies) != 0 {
		t.Errorf("expected 0 proxies when unreachable, got %d", len(snap.Proxies))
	}
	if snap.Groups == nil {
		t.Error("Groups should be initialized even when unreachable")
	}
}
