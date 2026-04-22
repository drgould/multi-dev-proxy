package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"

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

func TestDetectGroupFallback(t *testing.T) {
	group := DetectGroup("/nonexistent/path/that/has/no/git")
	if group != "default" {
		t.Errorf("expected 'default' for non-git dir, got %q", group)
	}
}

// waitForStatus polls ServiceStatus until it equals want or timeout elapses.
func waitForStatus(t *testing.T, o *Orchestrator, name, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if status, ok := o.ServiceStatus(name); ok && status == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// swapTCPCheck replaces the package tcpCheck with fn for the duration of the
// test and restores it on cleanup.
func swapTCPCheck(t *testing.T, fn func(int) bool) {
	t.Helper()
	orig := tcpCheck
	tcpCheck = fn
	t.Cleanup(func() { tcpCheck = orig })
}

// swapTimeouts shortens readyTimeout/readyPoll for the test duration.
func swapTimeouts(t *testing.T, timeout, poll time.Duration) {
	t.Helper()
	origTimeout, origPoll := readyTimeout, readyPoll
	readyTimeout, readyPoll = timeout, poll
	t.Cleanup(func() { readyTimeout, readyPoll = origTimeout, origPoll })
}

func TestStartConfigServicesRespectsDependsOn(t *testing.T) {
	swapTimeouts(t, 5*time.Second, 10*time.Millisecond)

	// a is "ready" only after gateA is closed. Everything else is always ready.
	gateA := make(chan struct{})
	var once sync.Once
	readyPorts := func() map[int]bool {
		select {
		case <-gateA:
			return map[int]bool{9901: true, 9902: true}
		default:
			return map[int]bool{9902: true}
		}
	}
	swapTCPCheck(t, func(p int) bool {
		return readyPorts()[p]
	})

	cfg := &config.Config{
		PortRange: "10000-60000",
		Services: map[string]config.ServiceConfig{
			"a": {Command: "sleep 30", Port: 9901},
			"b": {Command: "sleep 30", Port: 9902, DependsOn: []string{"a"}},
		},
	}
	o := New(cfg, "")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan struct{})
	go func() {
		_ = o.StartConfigServices(ctx, "test")
		close(done)
	}()

	if !waitForStatus(t, o, "test/a", "starting", 2*time.Second) {
		t.Fatal("a did not reach 'starting'")
	}
	// a is launched; b should still be waiting because tcpCheck(9901) returns false.
	if !waitForStatus(t, o, "test/b", "waiting", 2*time.Second) {
		status, _ := o.ServiceStatus("test/b")
		t.Fatalf("b status = %q, want 'waiting' while a not yet TCP-ready", status)
	}

	// Flip a to ready: a transitions to running, b is unblocked.
	once.Do(func() { close(gateA) })

	if !waitForStatus(t, o, "test/a", "running", 2*time.Second) {
		t.Fatal("a did not transition to 'running' after TCP-ready")
	}
	if !waitForStatus(t, o, "test/b", "running", 2*time.Second) {
		t.Fatal("b did not transition to 'running' after a became ready")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("StartConfigServices did not return after cancel")
	}
}

func TestStartConfigServicesParallelForIndependentServices(t *testing.T) {
	swapTimeouts(t, 5*time.Second, 10*time.Millisecond)
	swapTCPCheck(t, func(int) bool { return true })

	cfg := &config.Config{
		PortRange: "10000-60000",
		Services: map[string]config.ServiceConfig{
			"x": {Command: "sleep 30", Port: 9911},
			"y": {Command: "sleep 30", Port: 9912},
			"z": {Command: "sleep 30", Port: 9913},
		},
	}
	o := New(cfg, "")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() { _ = o.StartConfigServices(ctx, "test") }()

	for _, name := range []string{"test/x", "test/y", "test/z"} {
		if !waitForStatus(t, o, name, "running", 2*time.Second) {
			t.Fatalf("%s did not reach 'running'", name)
		}
	}
}

func TestStartConfigServicesMarksDependentFailedOnLaunchError(t *testing.T) {
	// Regression: if a dependent service's own launch fails (e.g., bad
	// command), it was pre-registered as "waiting" and the launch error path
	// left it stuck there instead of transitioning to "failed".
	swapTimeouts(t, 2*time.Second, 10*time.Millisecond)
	swapTCPCheck(t, func(int) bool { return true })

	cfg := &config.Config{
		PortRange: "10000-60000",
		Services: map[string]config.ServiceConfig{
			"a": {Command: "sleep 30", Port: 9941},
			"b": {
				Command:   "/path/that/does/not/exist/binary",
				Port:      9942,
				DependsOn: []string{"a"},
			},
		},
	}
	o := New(cfg, "")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan struct{})
	go func() {
		_ = o.StartConfigServices(ctx, "test")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("StartConfigServices did not return")
	}

	if status, _ := o.ServiceStatus("test/b"); status != "failed" {
		t.Errorf("b status = %q, want 'failed' after launch error", status)
	}
}

func TestStartConfigServicesFailsWhenDepTimesOut(t *testing.T) {
	swapTimeouts(t, 200*time.Millisecond, 20*time.Millisecond)
	// a's port never becomes TCP-ready.
	swapTCPCheck(t, func(p int) bool { return p != 9921 })

	cfg := &config.Config{
		PortRange: "10000-60000",
		Services: map[string]config.ServiceConfig{
			"a": {Command: "sleep 30", Port: 9921},
			"b": {Command: "sleep 30", Port: 9922, DependsOn: []string{"a"}},
		},
	}
	o := New(cfg, "")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan struct{})
	go func() {
		_ = o.StartConfigServices(ctx, "test")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("StartConfigServices should return after timeouts")
	}

	if status, _ := o.ServiceStatus("test/a"); status != "failed" {
		t.Errorf("a status = %q, want 'failed' (TCP-ready timeout)", status)
	}
	if status, _ := o.ServiceStatus("test/b"); status != "failed" {
		t.Errorf("b status = %q, want 'failed' (dep failed)", status)
	}
}
