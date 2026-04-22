package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadBasic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  frontend:
    command: npm run dev
    dir: ./frontend
    proxy: 3000
    env:
      NEXT_PUBLIC_API_URL: https://localhost:4000
  api:
    command: go run ./cmd/server
    dir: ./backend
    proxy: 4000
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(cfg.Services))
	}
	fe := cfg.Services["frontend"]
	if fe.Command != "npm run dev" {
		t.Errorf("frontend command = %q", fe.Command)
	}
	if fe.Proxy != 3000 {
		t.Errorf("frontend proxy = %d", fe.Proxy)
	}
	expectedDir := filepath.Join(dir, "frontend")
	if fe.Dir != expectedDir {
		t.Errorf("frontend dir = %q, want %q", fe.Dir, expectedDir)
	}
	if fe.Env["NEXT_PUBLIC_API_URL"] != "https://localhost:4000" {
		t.Errorf("frontend env = %v", fe.Env)
	}
}

func TestLoadMultiPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  infra:
    command: docker compose up
    env:
      API_PORT: auto
      AUTH_PORT: auto
    ports:
      - env: API_PORT
        proxy: 4000
        name: api
      - env: AUTH_PORT
        proxy: 5000
        name: auth
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	infra := cfg.Services["infra"]
	if len(infra.Ports) != 2 {
		t.Fatalf("expected 2 port mappings, got %d", len(infra.Ports))
	}
	if infra.Ports[0].Name != "api" || infra.Ports[0].Proxy != 4000 {
		t.Errorf("ports[0] = %+v", infra.Ports[0])
	}
}

func TestLoadMultiPortWithoutProxy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  postgres:
    command: ./run-pg.sh
    env:
      DB_PORT: auto
      REPL_PORT: auto
    ports:
      - env: DB_PORT
      - env: REPL_PORT
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	pg := cfg.Services["postgres"]
	if len(pg.Ports) != 2 {
		t.Fatalf("expected 2 port mappings, got %d", len(pg.Ports))
	}
	for i, pm := range pg.Ports {
		if pm.Proxy != 0 {
			t.Errorf("ports[%d].Proxy = %d, want 0", i, pm.Proxy)
		}
	}
	if pg.Ports[0].Env != "DB_PORT" || pg.Ports[1].Env != "REPL_PORT" {
		t.Errorf("ports envs = %q, %q", pg.Ports[0].Env, pg.Ports[1].Env)
	}
}

func TestLoadDefaultPortRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte("services: {}\n"), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PortRange != "10000-60000" {
		t.Errorf("port_range = %q, want 10000-60000", cfg.PortRange)
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	os.MkdirAll(sub, 0755)
	configPath := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(configPath, []byte("services: {}\n"), 0644)

	found := Find(sub)
	if found != configPath {
		t.Errorf("Find() = %q, want %q", found, configPath)
	}
}

func TestFindNotFound(t *testing.T) {
	dir := t.TempDir()
	found := Find(dir)
	if found != "" {
		t.Errorf("Find() = %q, want empty", found)
	}
}

func TestLoadWithValidDependencies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  db:
    command: ./db
  api:
    command: ./api
    depends_on:
      - db
  web:
    command: ./web
    depends_on:
      - api
      - db
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Services["api"].DependsOn; len(got) != 1 || got[0] != "db" {
		t.Errorf("api.DependsOn = %v, want [db]", got)
	}
	if got := cfg.Services["web"].DependsOn; len(got) != 2 {
		t.Errorf("web.DependsOn = %v, want 2 entries", got)
	}
}

func TestLoadWithUnknownDependency(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  api:
    command: ./api
    depends_on:
      - ghost
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown dependency, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v; want mention of ghost", err)
	}
}

func TestLoadWithSelfDependency(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  api:
    command: ./api
    depends_on:
      - api
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected cycle error for self-dependency, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %v; want mention of cycle", err)
	}
}

func TestLoadWithTwoNodeCycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  a:
    command: ./a
    depends_on: [b]
  b:
    command: ./b
    depends_on: [a]
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %v; want cycle", err)
	}
}

func TestLoadWithThreeNodeCycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  a:
    command: ./a
    depends_on: [b]
  b:
    command: ./b
    depends_on: [c]
  c:
    command: ./c
    depends_on: [a]
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %v; want cycle", err)
	}
}
