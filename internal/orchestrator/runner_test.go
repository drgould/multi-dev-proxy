package orchestrator

import (
	"testing"
)

func TestBuildEnv(t *testing.T) {
	tests := []struct {
		name       string
		configEnv  map[string]string
		ports      map[string]int
		wantLen    int
		wantContain string
	}{
		{
			name:       "auto port replaced",
			configEnv:  map[string]string{"PORT": "auto"},
			ports:      map[string]int{"PORT": 4001},
			wantLen:    1,
			wantContain: "PORT=4001",
		},
		{
			name:       "static value preserved",
			configEnv:  map[string]string{"NODE_ENV": "production"},
			ports:      map[string]int{},
			wantLen:    1,
			wantContain: "NODE_ENV=production",
		},
		{
			name:       "port assignment added for non-config key",
			configEnv:  map[string]string{},
			ports:      map[string]int{"PORT": 3000},
			wantLen:    1,
			wantContain: "PORT=3000",
		},
		{
			name:       "mixed env",
			configEnv:  map[string]string{"PORT": "auto", "HOST": "0.0.0.0"},
			ports:      map[string]int{"PORT": 4001},
			wantLen:    2,
			wantContain: "PORT=4001",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := buildEnv(tt.configEnv, tt.ports)
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

func TestDetectGroupFallback(t *testing.T) {
	group := DetectGroup("/nonexistent/path/that/has/no/git")
	if group != "default" {
		t.Errorf("expected 'default' for non-git dir, got %q", group)
	}
}
