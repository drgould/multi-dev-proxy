package process

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type mockProxy struct {
	registered   atomic.Int32
	deregistered atomic.Int32
	server       *httptest.Server
}

func newMockProxy(t *testing.T) *mockProxy {
	t.Helper()
	mp := &mockProxy{}
	mp.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/__mdp/register":
			mp.registered.Add(1)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/__mdp/register/"):
			mp.deregistered.Add(1)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "deleted": true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(mp.server.Close)
	return mp
}

func TestManagerSpawnAndRegister(t *testing.T) {
	mp := newMockProxy(t)
	m := New()
	ctx := context.Background()
	opts := RunOpts{
		ProxyURL:     mp.server.URL,
		ServerName:   "app/main",
		AssignedPort: 19876,
		ProxyTimeout: 2 * time.Second,
	}
	code, err := m.Run(ctx, []string{"echo", "hello"}, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
	if mp.registered.Load() != 1 {
		t.Errorf("expected 1 registration, got %d", mp.registered.Load())
	}
	if mp.deregistered.Load() != 1 {
		t.Errorf("expected 1 deregistration, got %d", mp.deregistered.Load())
	}
}

func TestManagerPortEnvVar(t *testing.T) {
	tmpFile := t.TempDir() + "/port.txt"
	m := New()
	ctx := context.Background()
	opts := RunOpts{
		AssignedPort: 54321,
		ProxyTimeout: 2 * time.Second,
	}
	code, err := m.Run(ctx, []string{"sh", "-c", "echo $PORT > " + tmpFile}, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("read port file: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if got != "54321" {
		t.Errorf("PORT env: got %q, want %q", got, "54321")
	}
}

func TestManagerNoProxy(t *testing.T) {
	m := New()
	ctx := context.Background()
	opts := RunOpts{
		AssignedPort: 19877,
		ProxyTimeout: 500 * time.Millisecond,
	}
	code, err := m.Run(ctx, []string{"echo", "solo"}, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}

// flushingWriter captures bytes and records whether Flush() was called so we
// can verify Manager.Run drains custom sinks before returning.
type flushingWriter struct {
	buf     []byte
	flushed atomic.Bool
}

func (w *flushingWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func (w *flushingWriter) Flush() { w.flushed.Store(true) }

func TestManagerFlushesCustomSinks(t *testing.T) {
	out := &flushingWriter{}
	errW := &flushingWriter{}
	m := New()
	ctx := context.Background()
	opts := RunOpts{
		AssignedPort: 19878,
		ProxyTimeout: 500 * time.Millisecond,
		Stdout:       out,
		Stderr:       errW,
	}
	code, err := m.Run(ctx, []string{"echo", "hello"}, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
	if !out.flushed.Load() {
		t.Error("Manager.Run should call Flush on custom Stdout sink")
	}
	if !errW.flushed.Load() {
		t.Error("Manager.Run should call Flush on custom Stderr sink")
	}
}

func TestManagerExitCode(t *testing.T) {
	m := New()
	ctx := context.Background()
	opts := RunOpts{ProxyTimeout: 500 * time.Millisecond}
	code, err := m.Run(ctx, []string{"sh", "-c", "exit 42"}, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 42 {
		t.Errorf("expected exit code 42, got %d", code)
	}
}
