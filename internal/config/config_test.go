package config

import (
	"os"
	"path/filepath"
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

func TestLoadHooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  web:
    setup:
      - bun install
      - bun run build:assets
    command: bun dev
    shutdown:
      - rm -rf .cache/dev
    proxy: 3000
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	web := cfg.Services["web"]
	wantSetup := []string{"bun install", "bun run build:assets"}
	if len(web.Setup) != len(wantSetup) {
		t.Fatalf("setup len = %d, want %d", len(web.Setup), len(wantSetup))
	}
	for i, s := range wantSetup {
		if web.Setup[i] != s {
			t.Errorf("setup[%d] = %q, want %q", i, web.Setup[i], s)
		}
	}
	wantShutdown := []string{"rm -rf .cache/dev"}
	if len(web.Shutdown) != len(wantShutdown) {
		t.Fatalf("shutdown len = %d, want %d", len(web.Shutdown), len(wantShutdown))
	}
	if web.Shutdown[0] != wantShutdown[0] {
		t.Errorf("shutdown[0] = %q, want %q", web.Shutdown[0], wantShutdown[0])
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
