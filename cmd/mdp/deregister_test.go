package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newDeregisterCmd(controlPort int, args []string) *cobra.Command {
	cmd := &cobra.Command{Use: "deregister", RunE: runDeregister, Args: cobra.ExactArgs(1)}
	cmd.Flags().Int("control-port", controlPort, "")
	cmd.SetArgs(args)
	return cmd
}

func TestDeregisterSuccess(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__mdp/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		gotMethod = r.Method
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "deleted": true})
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	cmd := newDeregisterCmd(port, []string{"app/main"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("deregister: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	if gotPath != "/__mdp/register/app/main" {
		t.Errorf("expected path /__mdp/register/app/main, got %s", gotPath)
	}
}

func TestDeregisterNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__mdp/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "deleted": false})
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	cmd := newDeregisterCmd(port, []string{"nonexistent"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("deregister should not error for not found: %v", err)
	}
}

func TestDeregisterNoOrchestrator(t *testing.T) {
	cmd := newDeregisterCmd(19997, []string{"app/main"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when orchestrator not running")
	}
	if !strings.Contains(err.Error(), "no orchestrator running") {
		t.Errorf("expected 'no orchestrator running' error, got: %v", err)
	}
}
