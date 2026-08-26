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

func TestManagerRestartsOnExit(t *testing.T) {
	prevDelay := restartDelay
	restartDelay = 10 * time.Millisecond
	t.Cleanup(func() { restartDelay = prevDelay })

	tmpFile := t.TempDir() + "/runs.txt"
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	opts := RunOpts{
		AssignedPort: 19878,
		ProxyTimeout: 2 * time.Second,
		Restart:      true,
	}

	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := m.Run(ctx, []string{"sh", "-c", "echo x >> " + tmpFile}, opts)
		done <- result{code, err}
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(tmpFile)
		if strings.Count(string(data), "x\n") >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, _ := os.ReadFile(tmpFile)
	if got := strings.Count(string(data), "x\n"); got < 2 {
		t.Fatalf("expected at least 2 restarts, got %d runs; data=%q", got, string(data))
	}

	cancel()
	select {
	case res := <-done:
		if res.err != nil {
			t.Errorf("Run returned err: %v", res.err)
		}
		if res.code != 0 {
			t.Errorf("Run returned code %d, want 0", res.code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of cancellation")
	}
}

// TestManagerRestartsAndReregisters verifies each restart re-registers with
// the proxy (not just the initial launch), and that the fixed assigned port
// — not a previously detected one — is what gets passed to the child on
// every restart.
func TestManagerRestartsAndReregisters(t *testing.T) {
	prevDelay := restartDelay
	restartDelay = 10 * time.Millisecond
	t.Cleanup(func() { restartDelay = prevDelay })

	mp := newMockProxy(t)
	tmpFile := t.TempDir() + "/ports.txt"
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	opts := RunOpts{
		ProxyURL:     mp.server.URL,
		ServerName:   "app/main",
		AssignedPort: 29876,
		ProxyTimeout: 2 * time.Second,
		Restart:      true,
	}

	done := make(chan struct{})
	go func() {
		m.Run(ctx, []string{"sh", "-c", "echo $PORT >> " + tmpFile}, opts)
		close(done)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && mp.registered.Load() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := mp.registered.Load(); got < 2 {
		t.Fatalf("expected at least 2 registrations across restarts, got %d", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of cancellation")
	}

	data, _ := os.ReadFile(tmpFile)
	for _, line := range strings.Fields(string(data)) {
		if line != "29876" {
			t.Errorf("PORT env across restarts: got %q, want stable %q", line, "29876")
		}
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
