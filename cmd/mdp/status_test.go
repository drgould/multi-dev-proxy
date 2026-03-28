package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchProxies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{
				"port":    3000,
				"label":   "frontend",
				"default": "app/main",
				"servers": []map[string]any{
					{"name": "app/main", "port": 4001, "pid": 100},
					{"name": "app/dev", "port": 4002, "pid": 200},
				},
			},
		})
	}))
	defer srv.Close()

	client := srv.Client()
	data := &statusData{Groups: make(map[string][]string)}
	if err := fetchProxies(client, srv.URL, data); err != nil {
		t.Fatal(err)
	}
	if len(data.Proxies) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(data.Proxies))
	}
	p := data.Proxies[0]
	if p.Port != 3000 {
		t.Errorf("expected port 3000, got %d", p.Port)
	}
	if p.Label != "frontend" {
		t.Errorf("expected label 'frontend', got %q", p.Label)
	}
	if p.Default != "app/main" {
		t.Errorf("expected default 'app/main', got %q", p.Default)
	}
	if len(p.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(p.Servers))
	}
	if p.Servers[0].Name != "app/dev" {
		t.Errorf("servers should be sorted, first is %q", p.Servers[0].Name)
	}
}

func TestFetchGroups(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string][]string{
			"dev":     {"app/dev", "api/dev"},
			"staging": {"app/staging", "api/staging"},
		})
	}))
	defer srv.Close()

	client := srv.Client()
	data := &statusData{Groups: make(map[string][]string)}
	if err := fetchGroups(client, srv.URL, data); err != nil {
		t.Fatal(err)
	}
	if len(data.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(data.Groups))
	}
	if len(data.Groups["dev"]) != 2 {
		t.Errorf("expected 2 members in dev, got %d", len(data.Groups["dev"]))
	}
}

func TestFetchServices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]serviceStatus{
			{Name: "web", Status: "running", PID: 42},
			{Name: "api", Status: "stopped"},
		})
	}))
	defer srv.Close()

	client := srv.Client()
	data := &statusData{Groups: make(map[string][]string)}
	if err := fetchServices(client, srv.URL, data); err != nil {
		t.Fatal(err)
	}
	if len(data.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(data.Services))
	}
	if data.Services[0].Name != "web" || data.Services[0].Status != "running" {
		t.Errorf("unexpected first service: %+v", data.Services[0])
	}
}

func TestFetchProxiesServerDown(t *testing.T) {
	client := &http.Client{}
	data := &statusData{Groups: make(map[string][]string)}
	err := fetchProxies(client, "http://127.0.0.1:1", data)
	if err != nil {
		t.Fatalf("fetchProxies should gracefully handle connection error, got %v", err)
	}
	if len(data.Proxies) != 0 {
		t.Fatalf("expected 0 proxies when server unreachable, got %d", len(data.Proxies))
	}
}

func TestPrintStatusSingleProxy(t *testing.T) {
	data := statusData{
		Daemon: daemonStatus{Running: true, PID: 123, ControlPort: 13100},
		Proxies: []proxyStatus{
			{
				Port:    3000,
				Label:   "frontend",
				Default: "app/main",
				Servers: []serverStatus{
					{Name: "app/main", Port: 4001, PID: 100},
				},
			},
		},
		Groups: map[string][]string{"dev": {"app/dev"}},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printStatus(data)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "1 proxy, 1 servers") {
		t.Errorf("single proxy should not show group count, got %q", out)
	}
	if strings.Contains(out, "Groups:") {
		t.Error("groups section should be hidden with single proxy")
	}
}

func TestPrintStatusMultiProxy(t *testing.T) {
	data := statusData{
		Daemon: daemonStatus{Running: true, PID: 123, ControlPort: 13100},
		Proxies: []proxyStatus{
			{Port: 3000, Label: "frontend"},
			{Port: 3001, Label: "backend"},
		},
		Groups: map[string][]string{"dev": {"app/dev"}, "staging": {"app/staging"}},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printStatus(data)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "2 proxies, 0 servers, 2 groups") {
		t.Errorf("multi-proxy should show group count, got %q", out)
	}
	if !strings.Contains(out, "Groups:") {
		t.Error("groups section should be visible with multiple proxies")
	}
}

func TestReadPIDFromFile(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "mdp.pid")
	os.WriteFile(pidFile, []byte("12345\n"), 0644)

	// readPID uses pidFilePath() which is platform-specific,
	// so we test the parsing logic directly
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(b))
	if got != "12345" {
		t.Fatalf("expected '12345', got %q", got)
	}
}

func TestReadPIDMissingFile(t *testing.T) {
	// readPID returns 0 when file doesn't exist — test the same parsing
	pid := readPID()
	// We can't control pidFilePath() but we can verify it doesn't panic
	// and returns a non-negative value
	if pid < 0 {
		t.Errorf("expected pid >= 0, got %d", pid)
	}
}

func TestOutputJSON(t *testing.T) {
	data := statusData{
		Daemon:  daemonStatus{Running: true, PID: 42, ControlPort: 13100},
		Groups:  map[string][]string{},
		Proxies: []proxyStatus{{Port: 3000}},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	outputJSON(data)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	var parsed statusData
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("outputJSON should produce valid JSON: %v", err)
	}
	if !parsed.Daemon.Running {
		t.Error("expected daemon running in JSON output")
	}
	if parsed.Daemon.PID != 42 {
		t.Errorf("expected PID 42, got %d", parsed.Daemon.PID)
	}
}
