package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newRegisterCmd(controlPort int, flags map[string]string, args []string) *cobra.Command {
	cmd := &cobra.Command{Use: "register", RunE: runRegister, Args: cobra.MaximumNArgs(1)}
	cmd.Flags().IntP("port", "p", 0, "")
	cmd.Flags().Int("pid", 0, "")
	cmd.Flags().IntP("proxy-port", "P", 3000, "")
	cmd.Flags().BoolP("list", "l", false, "")
	cmd.Flags().String("group", "", "")
	cmd.Flags().Int("control-port", controlPort, "")
	for k, v := range flags {
		cmd.Flags().Set(k, v)
	}
	cmd.SetArgs(args)
	return cmd
}

func TestRegisterViaOrchestratorSuccess(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__mdp/health" {
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		gotMethod = r.Method
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	cmd := newRegisterCmd(port, map[string]string{
		"port":       "4001",
		"pid":        "100",
		"proxy-port": "3000",
		"group":      "dev",
	}, []string{"app/main"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("register: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/__mdp/register" {
		t.Errorf("expected path /__mdp/register, got %s", gotPath)
	}
	if gotBody["name"] != "app/main" {
		t.Errorf("expected name app/main, got %v", gotBody["name"])
	}
}

func TestRegisterViaOrchestratorList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__mdp/health" {
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		if r.URL.Path == "/__mdp/proxies" {
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"port": 3000,
					"servers": []map[string]any{
						{"name": "app/dev", "port": 4001},
					},
				},
			})
			return
		}
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	cmd := newRegisterCmd(port, map[string]string{"list": "true"}, nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("register --list: %v", err)
	}
}

func TestRegisterViaOrchestratorMissingName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	cmd := newRegisterCmd(port, nil, nil)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when name missing")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected 'name is required' error, got: %v", err)
	}
}

func TestRegisterViaOrchestratorMissingPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	cmd := newRegisterCmd(port, nil, []string{"app/main"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --port missing")
	}
	if !strings.Contains(err.Error(), "--port is required") {
		t.Errorf("expected '--port is required' error, got: %v", err)
	}
}

func TestRegisterViaOrchestratorServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__mdp/health" {
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	cmd := newRegisterCmd(port, map[string]string{
		"port":       "4001",
		"proxy-port": "3000",
	}, []string{"app/main"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error on server error response")
	}
	if !strings.Contains(err.Error(), "register failed") {
		t.Errorf("expected 'register failed' error, got: %v", err)
	}
}

func TestDiscoverProxyURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	url := discoverProxyURL(port)
	if !strings.HasPrefix(url, "http") {
		t.Errorf("expected http(s) URL, got %q", url)
	}
}

func TestDiscoverProxyURLUnreachable(t *testing.T) {
	url := discoverProxyURL(1)
	if !strings.Contains(url, "https://localhost:1") {
		t.Errorf("expected fallback https URL, got %q", url)
	}
}

func TestListServersEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	if err := listServers(srv.URL); err != nil {
		t.Fatalf("listServers: %v", err)
	}
}

func TestListServersWithData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"myrepo": map[string]any{
				"myrepo/main": map[string]any{"port": 3000},
			},
		})
	}))
	defer srv.Close()

	if err := listServers(srv.URL); err != nil {
		t.Fatalf("listServers: %v", err)
	}
}

func TestListServersUnreachable(t *testing.T) {
	err := listServers("http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}
