package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrefixWriter(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	pw := &prefixWriter{prefix: "[test] ", out: w}

	pw.Write([]byte("hello\nworld\n"))
	pw.Flush()
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "[test] hello\n") {
		t.Errorf("expected prefixed 'hello', got %q", out)
	}
	if !strings.Contains(out, "[test] world\n") {
		t.Errorf("expected prefixed 'world', got %q", out)
	}
}

func TestPrefixWriterPartialLines(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	pw := &prefixWriter{prefix: "> ", out: w}

	pw.Write([]byte("partial"))
	pw.Write([]byte(" line\n"))
	pw.Flush()
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "> partial line\n") {
		t.Errorf("expected combined partial line, got %q", out)
	}
}

func TestPrefixWriterFlushIncomplete(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	pw := &prefixWriter{prefix: "$ ", out: w}
	pw.Write([]byte("no newline"))
	pw.Flush()
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "$ no newline\n") {
		t.Errorf("Flush should emit incomplete buffer, got %q", out)
	}
}

func TestNewPrefixWriterTruncatesLabel(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	pw := newPrefixWriter("verylonglabelname", "0", w)
	pw.Write([]byte("hi\n"))
	pw.Flush()
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if strings.Contains(out, "verylonglabelname") {
		t.Errorf("label should be truncated to 12 chars, got %q", out)
	}
	if !strings.Contains(out, "verylonglab") {
		t.Errorf("should contain truncated label, got %q", out)
	}
}

func TestDetectProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__mdp/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	url, found := detectProxy(port)
	if !found {
		t.Fatal("expected to detect proxy")
	}
	if !strings.HasPrefix(url, "http://") {
		t.Errorf("expected http URL, got %q", url)
	}
}

func TestDetectProxyNotRunning(t *testing.T) {
	_, found := detectProxy(19999)
	if found {
		t.Fatal("expected no proxy on unused port")
	}
}

func TestIsOrchestratorRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__mdp/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	if !isOrchestratorRunning(port) {
		t.Fatal("expected orchestrator detected on test server")
	}
}

func TestIsOrchestratorNotRunning(t *testing.T) {
	if isOrchestratorRunning(19998) {
		t.Fatal("expected no orchestrator on unused port")
	}
}

func TestWatchHealthClosesOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	gone := watchHealth(srv.URL + "/__mdp/health")

	select {
	case <-gone:
	case <-time.After(15 * time.Second):
		t.Fatal("watchHealth should have closed after failures")
	}
}

func TestWatchHealthStaysOpenWhenHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	gone := watchHealth(srv.URL + "/__mdp/health")

	select {
	case <-gone:
		t.Fatal("watchHealth should not close while healthy")
	case <-time.After(5 * time.Second):
	}
}

func TestRunSoloNoEnvOverride(t *testing.T) {
	err := runSolo([]string{"sh", "-c", `test -z "$MDP" && test -z "$PORT"`})
	if err != nil {
		t.Fatalf("runSolo should not set MDP or PORT: %v", err)
	}
}

func TestDeregisterFromOrchestrator(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	deregisterFromOrchestrator(srv.URL, "app/main")

	if gotMethod != "DELETE" {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	if gotPath != "/__mdp/register/app/main" {
		t.Errorf("expected path /__mdp/register/app/main, got %s", gotPath)
	}
}

func TestDeregisterFromOrchestratorNoOp(t *testing.T) {
	deregisterFromOrchestrator("", "foo")
	deregisterFromOrchestrator("http://localhost:1234", "")
	deregisterFromOrchestrator("", "")
}

func TestRunProxiedDisconnectsOnChildExit(t *testing.T) {
	var disconnectCalled atomic.Int32
	var gotPath string
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/__mdp/disconnect" {
			disconnectCalled.Add(1)
			gotPath = r.URL.Path
			defer func() { close(done) }()
		}
		if r.URL.Path == "/__mdp/shutdown/watch" {
			<-done
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := runProxied(
		[]string{"sh", "-c", "exit 0"},
		"PORT", 12345, srv.URL, "test/svc", "test-client-id",
	)
	if err != nil {
		t.Fatalf("runProxied: %v", err)
	}
	if disconnectCalled.Load() != 1 {
		t.Errorf("expected 1 disconnect call, got %d", disconnectCalled.Load())
	}
	if gotPath != "/__mdp/disconnect" {
		t.Errorf("expected path /__mdp/disconnect, got %s", gotPath)
	}
}

func TestRunProxiedDisconnectsOnNonZeroExit(t *testing.T) {
	var disconnectCalled atomic.Int32
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/__mdp/disconnect" {
			disconnectCalled.Add(1)
			defer func() { close(done) }()
		}
		if r.URL.Path == "/__mdp/shutdown/watch" {
			<-done
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := runProxied(
		[]string{"sh", "-c", "exit 0"},
		"PORT", 12345, srv.URL, "test/svc", "test-client-id",
	)
	if err != nil {
		t.Fatalf("runProxied: %v", err)
	}
	if disconnectCalled.Load() != 1 {
		t.Errorf("expected 1 disconnect call, got %d", disconnectCalled.Load())
	}
}

func TestRunProxiedSetsMDPEnv(t *testing.T) {
	done := make(chan struct{})
	var closeOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__mdp/shutdown/watch" {
			<-done
			return
		}
		if r.Method == "POST" && r.URL.Path == "/__mdp/disconnect" {
			closeOnce.Do(func() { close(done) })
		}
		// Also handle PATCH (updatePID) with empty name — no-op on server
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Close done after runProxied returns (clientID is empty so disconnect is skipped)
	defer closeOnce.Do(func() { close(done) })

	err := runProxied(
		[]string{"sh", "-c", `test "$MDP" = "1" && test -n "$PORT"`},
		"PORT", 12345, srv.URL, "", "",
	)
	if err != nil {
		t.Fatalf("runProxied should set MDP=1 and PORT: %v", err)
	}
}
