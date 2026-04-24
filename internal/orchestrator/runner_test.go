package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/envexpand"
	"github.com/derekgould/multi-dev-proxy/internal/ports"
)

func TestSplitHookArgs(t *testing.T) {
	tests := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"", nil, false},
		{"   ", nil, false},
		{"echo hello", []string{"echo", "hello"}, false},
		{"  echo   hello  ", []string{"echo", "hello"}, false},
		{`sh -c 'echo "[setup] preparing"; sleep 1'`,
			[]string{"sh", "-c", `echo "[setup] preparing"; sleep 1`}, false},
		{`sh -c "echo 'hi there'"`,
			[]string{"sh", "-c", "echo 'hi there'"}, false},
		{"rm -rf .cache/dev", []string{"rm", "-rf", ".cache/dev"}, false},
		{`echo ""`, []string{"echo", ""}, false},
		{`'unterminated`, nil, true},
		{`"unterminated`, nil, true},
	}
	for _, tc := range tests {
		got, err := SplitHookArgs(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("SplitHookArgs(%q): want error, got %v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("SplitHookArgs(%q): unexpected err %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitHookArgs(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

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
	// Services here have no Command but a fixed Port; stub TCP readiness so
	// the test doesn't block 60s on the probe of unbound ports.
	swapTimeouts(t, 100*time.Millisecond, 10*time.Millisecond)
	swapTCPCheck(t, func(int) bool { return true })

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
	swapTimeouts(t, 100*time.Millisecond, 10*time.Millisecond)
	swapTCPCheck(t, func(int) bool { return true })

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

func TestStartConfigServicesProbesExternalDependency(t *testing.T) {
	// An external (commandless) service with a fixed port must be TCP-probed
	// before dependents start. Here external "ext" is registered on port 9951
	// but never becomes reachable → dep "d" must fail instead of racing ahead.
	swapTimeouts(t, 200*time.Millisecond, 10*time.Millisecond)
	swapTCPCheck(t, func(p int) bool { return p != 9951 })

	cfg := &config.Config{
		PortRange: "10000-60000",
		Services: map[string]config.ServiceConfig{
			"ext": {Port: 9951, Proxy: 3005},
			"d":   {Command: "sleep 30", Port: 9952, DependsOn: []string{"ext"}},
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
		t.Fatal("StartConfigServices should return after timeout")
	}

	if status, _ := o.ServiceStatus("test/d"); status != "failed" {
		t.Errorf("d status = %q, want 'failed' because ext never became reachable", status)
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

// swapFinders replaces ports.FindFreePort and ports.FindFreeUDPPort with
// recording stubs. Returns pointers that can be inspected after the test runs.
// Each finder returns an incrementing port from startPort so assignments are
// deterministic and non-colliding across the test.
func swapFinders(t *testing.T, startPort int) (tcpCalls, udpCalls *[]string) {
	t.Helper()
	origTCP := ports.FindFreePort
	origUDP := ports.FindFreeUDPPort
	var tcp, udp []string
	tcpNext, udpNext := startPort, startPort+1000
	var mu sync.Mutex
	ports.FindFreePort = func(r ports.PortRange, exclude []int) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		tcp = append(tcp, "tcp")
		p := tcpNext
		tcpNext++
		return p, nil
	}
	ports.FindFreeUDPPort = func(r ports.PortRange, exclude []int) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		udp = append(udp, "udp")
		p := udpNext
		udpNext++
		return p, nil
	}
	t.Cleanup(func() {
		ports.FindFreePort = origTCP
		ports.FindFreeUDPPort = origUDP
	})
	return &tcp, &udp
}

func TestStartConfigServicesDispatchesUDPAllocator(t *testing.T) {
	// Service with a UDP port mapping must allocate via FindFreeUDPPort, and
	// a TCP mapping must allocate via FindFreePort.
	swapTimeouts(t, 100*time.Millisecond, 10*time.Millisecond)
	swapTCPCheck(t, func(int) bool { return true })
	tcpCalls, udpCalls := swapFinders(t, 30000)

	cfg := &config.Config{
		PortRange: "10000-60000",
		Services: map[string]config.ServiceConfig{
			"infra": {
				Command: "sleep 30",
				Env: map[string]string{
					"TCP_PORT": "auto",
					"UDP_PORT": "auto",
				},
				Ports: []config.PortMapping{
					{Env: "TCP_PORT"},
					{Env: "UDP_PORT", Protocol: "udp"},
				},
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

	if len(*tcpCalls) != 1 {
		t.Errorf("FindFreePort called %d times, want 1 (for TCP_PORT)", len(*tcpCalls))
	}
	if len(*udpCalls) != 1 {
		t.Errorf("FindFreeUDPPort called %d times, want 1 (for UDP_PORT)", len(*udpCalls))
	}
}

func TestProbePortsForExcludesUDP(t *testing.T) {
	a := serviceAlloc{
		portAssignments: map[string]int{"TCP_PORT": 40001, "UDP_PORT": 40002},
		portProtocols:   map[string]string{"TCP_PORT": "tcp", "UDP_PORT": "udp"},
	}
	got := probePortsFor(a)
	if len(got) != 1 || got[0] != 40001 {
		t.Errorf("probePortsFor() = %v, want [40001] (UDP port excluded)", got)
	}
}

func TestStartMultiPortServiceSkipsRegistrationWhenProxyZero(t *testing.T) {
	// A port mapping with proxy:0 (such as a UDP-only allocation or an
	// internal-only port) must not call Register — otherwise EnsureProxy(0)
	// would spin up an ephemeral proxy that nothing ever reaches.
	swapTimeouts(t, 100*time.Millisecond, 10*time.Millisecond)
	swapTCPCheck(t, func(int) bool { return true })
	swapFinders(t, 31000)

	cfg := &config.Config{
		PortRange: "10000-60000",
		Services: map[string]config.ServiceConfig{
			"infra": {
				Command: "sleep 30",
				Env:     map[string]string{"UDP_PORT": "auto"},
				Ports:   []config.PortMapping{{Env: "UDP_PORT", Protocol: "udp"}},
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

	// No proxy should have been created. ListProxies returns all proxy
	// instances ever created by the orchestrator.
	if got := len(o.ListProxies()); got != 0 {
		t.Errorf("ListProxies() = %d, want 0 (UDP mapping should not create a proxy)", got)
	}
}
