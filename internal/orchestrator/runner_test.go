package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/envexpand"
)

func TestBuildEnv(t *testing.T) {
	tests := []struct {
		name        string
		configEnv   map[string]string
		ports       map[string]int
		portMap     envexpand.PortMap
		wantLen     int
		wantContain string
	}{
		{
			name:        "auto port replaced",
			configEnv:   map[string]string{"PORT": "auto"},
			ports:       map[string]int{"PORT": 4001},
			wantLen:     1,
			wantContain: "PORT=4001",
		},
		{
			name:        "static value preserved",
			configEnv:   map[string]string{"NODE_ENV": "production"},
			ports:       map[string]int{},
			wantLen:     1,
			wantContain: "NODE_ENV=production",
		},
		{
			name:        "port assignment added for non-config key",
			configEnv:   map[string]string{},
			ports:       map[string]int{"PORT": 3000},
			wantLen:     1,
			wantContain: "PORT=3000",
		},
		{
			name:        "mixed env",
			configEnv:   map[string]string{"PORT": "auto", "HOST": "0.0.0.0"},
			ports:       map[string]int{"PORT": 4001},
			wantLen:     2,
			wantContain: "PORT=4001",
		},
		{
			name:        "cross-service reference expanded",
			configEnv:   map[string]string{"DB_URL": "postgres://localhost:${db.port}/app"},
			ports:       map[string]int{},
			portMap:     envexpand.PortMap{"db": {"port": 5432, "PORT": 5432}},
			wantLen:     1,
			wantContain: "DB_URL=postgres://localhost:5432/app",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, err := buildEnv(tt.configEnv, tt.ports, tt.portMap)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(env) != tt.wantLen {
				t.Errorf("expected %d env vars, got %d: %v", tt.wantLen, len(env), env)
			}
			found := false
			for _, e := range env {
				if e == tt.wantContain {
					found = true
				}
			}
			if !found {
				t.Errorf("expected env to contain %q, got %v", tt.wantContain, env)
			}
		})
	}
}

func TestBuildEnvUnresolvedReferenceErrors(t *testing.T) {
	_, err := buildEnv(map[string]string{"X": "${nope.port}"}, nil, envexpand.PortMap{})
	if err == nil {
		t.Fatal("expected error for unresolved reference, got nil")
	}
}

func TestBuildEnvMultiPortWithCrossServiceRef(t *testing.T) {
	env, err := buildEnv(
		map[string]string{
			"API_PORT":  "auto",
			"AUTH_PORT": "auto",
			"DB_URL":    "postgres://localhost:${db.port}/app",
		},
		map[string]int{"API_PORT": 4001, "AUTH_PORT": 5001},
		envexpand.PortMap{"db": {"port": 5432, "PORT": 5432}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]bool{
		"API_PORT=4001":                              true,
		"AUTH_PORT=5001":                             true,
		"DB_URL=postgres://localhost:5432/app":       true,
	}
	for _, e := range env {
		delete(want, e)
	}
	if len(want) > 0 {
		t.Errorf("missing expected env vars: %v; got full env: %v", want, env)
	}
}

func TestBuildEnvAutoWithoutAssignmentIsSkipped(t *testing.T) {
	env, err := buildEnv(
		map[string]string{"PORT": "auto"},
		map[string]int{},
		envexpand.PortMap{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range env {
		if e == "PORT=" || len(e) > 4 && e[:5] == "PORT=" {
			t.Errorf("expected PORT to be skipped when no assignment, got %q in %v", e, env)
		}
	}
}

func TestStartConfigServicesWritesEnvFiles(t *testing.T) {
	tmp := t.TempDir()
	globalEnvPath := filepath.Join(tmp, "global.env")
	apiEnvDir := filepath.Join(tmp, "api-dir")
	apiEnvPath := filepath.Join(apiEnvDir, ".env")
	webEnvPath := filepath.Join(tmp, "web.env")

	cfg := &config.Config{
		PortRange: "30000-31000",
		Global: config.GlobalConfig{
			EnvFile: globalEnvPath,
			Env: map[string]config.GlobalEnvValue{
				"API_PORT": {Ref: "api.env.PORT"},
				"API_URL":  {Value: "http://localhost:${api.PORT}"},
				"WEB_MODE": {Ref: "web.env.MODE"},
			},
		},
		Services: map[string]config.ServiceConfig{
			"api": {
				Port:    30123,
				Dir:     apiEnvDir,
				EnvFile: apiEnvPath,
				Env:     map[string]string{"NAME": "api", "MODE": "test"},
			},
			"web": {
				Port:    30124,
				EnvFile: webEnvPath,
				Env:     map[string]string{"MODE": "dev"},
			},
		},
	}
	o := New(cfg, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := o.StartConfigServices(ctx, "test"); err != nil {
		t.Fatalf("StartConfigServices: %v", err)
	}

	gdata, err := os.ReadFile(globalEnvPath)
	if err != nil {
		t.Fatalf("read global: %v", err)
	}
	gtext := string(gdata)
	for _, want := range []string{
		`API_PORT="30123"`,
		`API_URL="http://localhost:30123"`,
		`WEB_MODE="dev"`,
	} {
		if !strings.Contains(gtext, want) {
			t.Errorf("global missing %q in:\n%s", want, gtext)
		}
	}

	sdata, err := os.ReadFile(apiEnvPath)
	if err != nil {
		t.Fatalf("read api env: %v", err)
	}
	stext := string(sdata)
	for _, want := range []string{`NAME="api"`, `MODE="test"`, `PORT="30123"`} {
		if !strings.Contains(stext, want) {
			t.Errorf("api missing %q in:\n%s", want, stext)
		}
	}

	wdata, err := os.ReadFile(webEnvPath)
	if err != nil {
		t.Fatalf("read web env: %v", err)
	}
	wtext := string(wdata)
	for _, want := range []string{`MODE="dev"`, `PORT="30124"`} {
		if !strings.Contains(wtext, want) {
			t.Errorf("web missing %q in:\n%s", want, wtext)
		}
	}
}

func TestStartConfigServicesSkipsWhenNoEnvFile(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		PortRange: "30000-31000",
		Services: map[string]config.ServiceConfig{
			"api": {Port: 30125, Env: map[string]string{"X": "y"}},
		},
	}
	o := New(cfg, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := o.StartConfigServices(ctx, "test"); err != nil {
		t.Fatalf("StartConfigServices: %v", err)
	}
	entries, _ := os.ReadDir(tmp)
	if len(entries) != 0 {
		t.Errorf("expected no files written, got: %v", entries)
	}
}

func TestStartConfigServicesGlobalRefErrorFailsFast(t *testing.T) {
	tmp := t.TempDir()
	globalEnvPath := filepath.Join(tmp, "global.env")
	cfg := &config.Config{
		PortRange: "30000-31000",
		Global: config.GlobalConfig{
			EnvFile: globalEnvPath,
			Env:     map[string]config.GlobalEnvValue{"X": {Ref: "nope.env.MISSING"}},
		},
		Services: map[string]config.ServiceConfig{
			"api": {Port: 30126, Env: map[string]string{"A": "b"}},
		},
	}
	o := New(cfg, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := o.StartConfigServices(ctx, "test"); err == nil {
		t.Fatal("expected error for unresolved ref")
	}
	if _, err := os.Stat(globalEnvPath); !os.IsNotExist(err) {
		t.Errorf("global file should not exist after ref error")
	}
}

func TestDetectGroupFallback(t *testing.T) {
	group := DetectGroup("/nonexistent/path/that/has/no/git")
	if group != "default" {
		t.Errorf("expected 'default' for non-git dir, got %q", group)
	}
}
