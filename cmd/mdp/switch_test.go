package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newSwitchCmd(controlPort int, flags map[string]any, args []string) *cobra.Command {
	cmd := &cobra.Command{Use: "switch", RunE: runSwitch, Args: cobra.MaximumNArgs(1)}
	cmd.Flags().IntP("proxy-port", "P", 0, "")
	cmd.Flags().String("group", "", "")
	cmd.Flags().Bool("clear", false, "")
	cmd.Flags().Int("control-port", controlPort, "")
	for k, v := range flags {
		switch val := v.(type) {
		case string:
			cmd.Flags().Set(k, val)
		case int:
			cmd.Flags().Set(k, fmt.Sprintf("%d", val))
		case bool:
			if val {
				cmd.Flags().Set(k, "true")
			}
		}
	}
	cmd.SetArgs(args)
	return cmd
}

func TestSwitchGroup(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	cmd := newSwitchCmd(port, map[string]any{"group": "dev"}, nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("switch --group: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/__mdp/groups/dev/switch" {
		t.Errorf("expected path /__mdp/groups/dev/switch, got %s", gotPath)
	}
}

func TestSwitchClear(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	cmd := newSwitchCmd(port, map[string]any{"clear": true, "proxy-port": 3000}, nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("switch --clear: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	if gotPath != "/__mdp/proxies/3000/default" {
		t.Errorf("expected path /__mdp/proxies/3000/default, got %s", gotPath)
	}
}

func TestSwitchClearRequiresProxyPort(t *testing.T) {
	cmd := newSwitchCmd(19999, map[string]any{"clear": true}, nil)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --clear without --proxy-port")
	}
	if !strings.Contains(err.Error(), "--proxy-port is required") {
		t.Errorf("expected proxy-port required error, got: %v", err)
	}
}

func TestSwitchServerRequiresProxyPort(t *testing.T) {
	cmd := newSwitchCmd(19999, nil, []string{"app/main"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when switching server without --proxy-port")
	}
	if !strings.Contains(err.Error(), "--proxy-port is required") {
		t.Errorf("expected proxy-port required error, got: %v", err)
	}
}

func TestSwitchNoNameRequiresGroup(t *testing.T) {
	cmd := newSwitchCmd(19999, nil, nil)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no name or --group")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected name required error, got: %v", err)
	}
}

func TestSwitchServer(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	cmd := newSwitchCmd(port, map[string]any{"proxy-port": 3000}, []string{"app/main"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("switch server: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/__mdp/proxies/3000/default/app/main" {
		t.Errorf("expected path /__mdp/proxies/3000/default/app/main, got %s", gotPath)
	}
}

func TestSwitchGroupFailedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	cmd := newSwitchCmd(port, map[string]any{"group": "nonexistent"}, nil)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for failed group switch")
	}
	if !strings.Contains(err.Error(), "switch group failed") {
		t.Errorf("expected switch group failed error, got: %v", err)
	}
}

func testPort(t *testing.T, url string) int {
	t.Helper()
	parts := strings.Split(url, ":")
	portStr := parts[len(parts)-1]
	var p int
	for _, c := range portStr {
		if c >= '0' && c <= '9' {
			p = p*10 + int(c-'0')
		}
	}
	return p
}
