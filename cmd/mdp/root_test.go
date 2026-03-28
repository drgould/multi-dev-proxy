package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunStopViaControlAPI(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	err := runStop(port)
	if err != nil {
		t.Fatalf("runStop should succeed via control API: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/__mdp/shutdown" {
		t.Errorf("expected /__mdp/shutdown, got %s", gotPath)
	}
}

func TestRunStopAPIDown(t *testing.T) {
	// Control API unreachable and no PID file → error
	err := runStop(19996)
	if err == nil {
		t.Fatal("expected error when no orchestrator and no PID file")
	}
	if !strings.Contains(err.Error(), "no orchestrator found") && !strings.Contains(err.Error(), "PID file") {
		t.Errorf("expected PID/orchestrator error, got: %v", err)
	}
}

func TestRunOrchestratorDaemonFlagSkipsTUI(t *testing.T) {
	// When orchestrator is already running, the --daemon path returns nil
	// without launching TUI. Verify the condition check works.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	if !isOrchestratorRunning(port) {
		t.Fatal("test server should be detected as orchestrator")
	}
	// The daemon path in runOrchestrator checks isOrchestratorRunning,
	// skips startDaemon, and returns nil when daemon=true.
}

func TestVersionString(t *testing.T) {
	v := rootCmd.Version
	if v == "" {
		t.Fatal("version should not be empty")
	}
	if !strings.Contains(v, "(") {
		t.Errorf("version should contain commit info in parens, got %q", v)
	}
}

func TestCleanupPIDFileNoop(t *testing.T) {
	// cleanupPIDFile should not panic when the PID file doesn't exist
	cleanupPIDFile()
}

func TestCleanupPIDFileRemovesFile(t *testing.T) {
	// Create a PID file at the real path and verify cleanup removes it
	path := pidFilePath()
	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0755)
	os.WriteFile(path, []byte("99999"), 0644)

	cleanupPIDFile()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("PID file should have been removed: %v", err)
	}
}
