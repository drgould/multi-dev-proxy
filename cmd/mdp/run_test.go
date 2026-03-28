package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestRunProxiedSetsMDPEnv(t *testing.T) {
	// Start a fake orchestrator health endpoint
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := runProxied(
		[]string{"sh", "-c", `test "$MDP" = "1" && test -n "$PORT"`},
		"PORT", 12345, srv.URL+"/__mdp/health",
	)
	if err != nil {
		t.Fatalf("runProxied should set MDP=1 and PORT: %v", err)
	}
}
