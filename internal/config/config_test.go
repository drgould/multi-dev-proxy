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

func TestLoadUDPPortMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  infra:
    command: docker compose up
    env:
      JAEGER_AGENT_PORT: auto
      API_PORT: auto
    ports:
      - env: JAEGER_AGENT_PORT
        protocol: UDP
      - env: API_PORT
        proxy: 4000
        name: api
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	infra := cfg.Services["infra"]
	if len(infra.Ports) != 2 {
		t.Fatalf("expected 2 port mappings, got %d", len(infra.Ports))
	}
	if infra.Ports[0].Protocol != "udp" {
		t.Errorf("ports[0].Protocol = %q, want \"udp\" (normalized to lowercase)", infra.Ports[0].Protocol)
	}
	if infra.Ports[1].Protocol != "tcp" && infra.Ports[1].Protocol != "" {
		t.Errorf("ports[1].Protocol = %q, want \"\" or \"tcp\"", infra.Ports[1].Protocol)
	}
}

func TestLoadRejectsUnknownProtocol(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  infra:
    command: docker compose up
    env:
      X: auto
    ports:
      - env: X
        protocol: sctp
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown protocol, got nil")
	}
	if !strings.Contains(err.Error(), "unknown protocol") {
		t.Errorf("error = %v, want it to mention \"unknown protocol\"", err)
	}
}

func TestLoadRejectsUDPWithProxy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  infra:
    command: docker compose up
    env:
      X: auto
    ports:
      - env: X
        protocol: udp
        proxy: 1234
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for udp+proxy, got nil")
	}
	if !strings.Contains(err.Error(), "udp") || !strings.Contains(err.Error(), "proxy") {
		t.Errorf("error = %v, want it to mention udp and proxy", err)
	}
}

func TestLoadRejectsUDPWithName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  infra:
    command: docker compose up
    env:
      X: auto
    ports:
      - env: X
        protocol: udp
        name: x
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for udp+name, got nil")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error = %v, want it to mention name", err)
	}
}

func TestEnvProtocols(t *testing.T) {
	svc := ServiceConfig{
		Ports: []PortMapping{
			{Env: "A", Protocol: "udp"},
			{Env: "B", Protocol: "tcp"},
			{Env: "C"},
		},
	}
	got := svc.EnvProtocols()
	if got["A"] != "udp" {
		t.Errorf("A = %q, want udp", got["A"])
	}
	if got["B"] != "tcp" {
		t.Errorf("B = %q, want tcp", got["B"])
	}
	if got["C"] != "tcp" {
		t.Errorf("C = %q, want tcp (default)", got["C"])
	}
}

func TestEnvProtocolsEmpty(t *testing.T) {
	svc := ServiceConfig{}
	if got := svc.EnvProtocols(); got != nil {
		t.Errorf("EnvProtocols() = %v, want nil for empty Ports", got)
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

func TestLoadGlobalEnvRejectsEmptyRef(t *testing.T) {
	cases := map[string]string{
		"bare ref":   "global:\n  env:\n    FOO:\n      ref:\n",
		"empty str":  "global:\n  env:\n    FOO:\n      ref: \"\"\n",
		"null value": "global:\n  env:\n    FOO:\n      ref: ~\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "mdp.yaml")
			os.WriteFile(path, []byte(body), 0644)
			if _, err := Load(path); err == nil {
				t.Fatal("expected error for empty ref, got nil")
			}
		})
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

func TestLoadHealthCheckMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  tcp_svc:
    command: run tcp
    port: 3000
    health_check:
      tcp: 3100
  http_svc:
    command: run http
    port: 4000
    health_check:
      http: http://localhost:4000/health
  cmd_svc:
    command: run cmd
    port: 5000
    health_check:
      command: "echo ok"
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if hc := cfg.Services["tcp_svc"].HealthCheck; hc == nil || hc.TCP != 3100 {
		t.Errorf("tcp_svc health_check = %+v", hc)
	}
	if hc := cfg.Services["http_svc"].HealthCheck; hc == nil || hc.HTTP != "http://localhost:4000/health" {
		t.Errorf("http_svc health_check = %+v", hc)
	}
	if hc := cfg.Services["cmd_svc"].HealthCheck; hc == nil || hc.Command != "echo ok" {
		t.Errorf("cmd_svc health_check = %+v", hc)
	}
}

func TestLoadHealthCheckDockerShorthand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  db:
    command: docker compose up -d
    dir: ./db
    port: 5432
    health_check: docker
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	hc := cfg.Services["db"].HealthCheck
	if hc == nil || !hc.Docker {
		t.Errorf("expected Docker=true, got %+v", hc)
	}
}

func TestLoadHealthCheckUnknownShorthand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  svc:
    command: run
    port: 3000
    health_check: bogus
`), 0644)

	if _, err := Load(path); err == nil {
		t.Error("expected error for unknown shorthand")
	} else if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("expected error to mention 'bogus', got: %v", err)
	}
}

func TestLoadHealthCheckMultipleVariants(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  svc:
    command: run
    port: 3000
    health_check:
      tcp: 3000
      http: http://localhost:3000/health
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error when multiple variants are set")
	}
	if !strings.Contains(err.Error(), "only one") {
		t.Errorf("expected 'only one' error, got: %v", err)
	}
}

func TestLoadHealthCheckEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  svc:
    command: run
    port: 3000
    health_check: {}
`), 0644)

	if _, err := Load(path); err == nil {
		t.Error("expected error for empty health_check mapping")
	}
}

func TestLoadLogSplit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  svc:
    command: docker compose up
    proxy: 3000
    log_split: compose
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Services["svc"].LogSplit.Mode != "compose" {
		t.Errorf("LogSplit.Mode = %q, want \"compose\"", cfg.Services["svc"].LogSplit.Mode)
	}
}

func TestLoadLogSplitInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  svc:
    command: run
    proxy: 3000
    log_split: bogus
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown log_split value")
	}
	if !strings.Contains(err.Error(), "log_split") {
		t.Errorf("expected error mentioning log_split, got: %v", err)
	}
}

func TestLoadLogSplitRegex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  svc:
    command: some-prefixed-tool
    proxy: 3000
    log_split:
      regex: '^\[(?P<name>[^\]]+)\]\s*(?P<msg>.*)$'
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	svc := cfg.Services["svc"]
	if svc.LogSplit.Mode != "regex" {
		t.Errorf("LogSplit.Mode = %q, want \"regex\"", svc.LogSplit.Mode)
	}
	if !strings.Contains(svc.LogSplit.Regex, "?P<name>") {
		t.Errorf("LogSplit.Regex did not round-trip: %q", svc.LogSplit.Regex)
	}
}

func TestLoadLogSplitRegexMissingCaptures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  svc:
    command: x
    proxy: 3000
    log_split:
      regex: '^(.+)$'
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error when regex omits named captures")
	}
	if !strings.Contains(err.Error(), "name") || !strings.Contains(err.Error(), "msg") {
		t.Errorf("error should mention required `name`/`msg` captures, got: %v", err)
	}
}

func TestLoadLogSplitRegexInvalidPattern(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  svc:
    command: x
    proxy: 3000
    log_split:
      regex: '[unclosed'
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for malformed regex")
	}
}

func TestLoadLogSplitMappingWithoutRegex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
services:
  svc:
    command: x
    proxy: 3000
    log_split:
      foo: bar
`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error when mapping form is missing regex: key")
	}
	if !strings.Contains(err.Error(), "regex") {
		t.Errorf("error should mention the missing `regex:` key, got: %v", err)
	}
}

func TestParseLogSplitFlag(t *testing.T) {
	cases := []struct {
		in       string
		wantMode string
		wantErr  bool
	}{
		{in: "", wantMode: ""},
		{in: "compose", wantMode: "compose"},
		{in: `regex:^\[(?P<name>[^\]]+)\]\s*(?P<msg>.*)$`, wantMode: "regex"},
		{in: "compose:extra", wantErr: true}, // unknown prefix
		{in: "regex:(", wantErr: true},       // malformed pattern
		{in: "regex:^(.+)$", wantErr: true},  // missing named captures
		{in: "bogus", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseLogSplitFlag(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for %q, got mode=%q", tc.in, got.Mode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.in, err)
			}
			if got.Mode != tc.wantMode {
				t.Errorf("mode: got %q, want %q", got.Mode, tc.wantMode)
			}
		})
	}
}
