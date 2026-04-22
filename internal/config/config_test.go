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

func TestLoadGlobalEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
global:
  env_file: ./.mdp.env
  env:
    API_BASE: "http://localhost:${api.PORT}"
    API_PORT:
      ref: api.env.PORT
services:
  api:
    command: ./api
    env:
      PORT: auto
    ports:
      - env: PORT
        proxy: 4000
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Global.EnvFile != filepath.Join(dir, ".mdp.env") {
		t.Errorf("Global.EnvFile = %q, want %q", cfg.Global.EnvFile, filepath.Join(dir, ".mdp.env"))
	}
	if len(cfg.Global.Env) != 2 {
		t.Fatalf("expected 2 global env entries, got %d", len(cfg.Global.Env))
	}
	if got := cfg.Global.Env["API_BASE"]; got.Value != "http://localhost:${api.PORT}" || got.Ref != "" {
		t.Errorf("API_BASE = %+v", got)
	}
	if got := cfg.Global.Env["API_PORT"]; got.Ref != "api.env.PORT" || got.Value != "" {
		t.Errorf("API_PORT = %+v", got)
	}
}

func TestLoadGlobalEnvRejectsUnknownKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
global:
  env:
    FOO:
      reference: api.env.PORT
`), 0644)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
}

func TestLoadGlobalEnvRejectsExtraKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
global:
  env:
    FOO:
      ref: api.env.PORT
      default: 1234
`), 0644)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for extra key, got nil")
	}
}

func TestLoadServiceEnvFileRelativeToServiceDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  web:
    command: ./serve
    dir: ./web
    env_file: ./.env.local
  api:
    command: ./api
    env_file: ./.env.api
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	web := cfg.Services["web"]
	wantWeb := filepath.Join(dir, "web", ".env.local")
	if web.EnvFile != wantWeb {
		t.Errorf("web.EnvFile = %q, want %q", web.EnvFile, wantWeb)
	}
	api := cfg.Services["api"]
	// No dir → fall back to config dir.
	wantAPI := filepath.Join(dir, ".env.api")
	if api.EnvFile != wantAPI {
		t.Errorf("api.EnvFile = %q, want %q", api.EnvFile, wantAPI)
	}
}
