package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/depwait"
	"github.com/derekgould/multi-dev-proxy/internal/envexpand"
)

// waitDoneOrFail waits for a supervise/launch goroutine to exit, reporting
// (not fataling, so the rest of cleanup still runs and kills any leaked
// process) if it's stuck past ctx cancel.
func waitDoneOrFail(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("goroutine did not exit within 5s of cancellation")
	}
}

func TestPrefixWriter(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	pw := &prefixWriter{prefix: "[test] ", out: w}

	pw.Write([]byte("hello\nworld\n"))
	pw.Flush()
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "[test] hello\n") {
		t.Errorf("expected prefixed 'hello', got %q", out)
	}
	if !strings.Contains(out, "[test] world\n") {
		t.Errorf("expected prefixed 'world', got %q", out)
	}
}

func TestPrefixWriterPartialLines(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	pw := &prefixWriter{prefix: "> ", out: w}

	pw.Write([]byte("partial"))
	pw.Write([]byte(" line\n"))
	pw.Flush()
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "> partial line\n") {
		t.Errorf("expected combined partial line, got %q", out)
	}
}

func TestPrefixWriterFlushIncomplete(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	pw := &prefixWriter{prefix: "$ ", out: w}
	pw.Write([]byte("no newline"))
	pw.Flush()
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "$ no newline\n") {
		t.Errorf("Flush should emit incomplete buffer, got %q", out)
	}
}

func TestNewPrefixWriterLongLabelNotTruncated(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	pw := newPrefixWriter("verylonglabelname", "0", w)
	pw.Write([]byte("hi\n"))
	pw.Flush()
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "verylonglabelname") {
		t.Errorf("full label must be preserved (no truncation), got %q", out)
	}
}

func TestDetectProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__mdp/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	url, found := detectProxy(port)
	if !found {
		t.Fatal("expected to detect proxy")
	}
	if !strings.HasPrefix(url, "http://") {
		t.Errorf("expected http URL, got %q", url)
	}
}

func TestDetectProxyNotRunning(t *testing.T) {
	_, found := detectProxy(19999)
	if found {
		t.Fatal("expected no proxy on unused port")
	}
}

func TestIsOrchestratorRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__mdp/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	port := testPort(t, srv.URL)
	if !isOrchestratorRunning(port) {
		t.Fatal("expected orchestrator detected on test server")
	}
}

func TestIsOrchestratorNotRunning(t *testing.T) {
	if isOrchestratorRunning(19998) {
		t.Fatal("expected no orchestrator on unused port")
	}
}

func TestWatchHealthClosesOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	gone := watchHealth(srv.URL + "/__mdp/health")

	select {
	case <-gone:
	case <-time.After(15 * time.Second):
		t.Fatal("watchHealth should have closed after failures")
	}
}

func TestWatchHealthStaysOpenWhenHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	gone := watchHealth(srv.URL + "/__mdp/health")

	select {
	case <-gone:
		t.Fatal("watchHealth should not close while healthy")
	case <-time.After(5 * time.Second):
	}
}

func TestLaunchBatchServiceSkipsUDPPortsFromProbe(t *testing.T) {
	// A port mapping with protocol: udp must never be TCP-probed — that's the
	// whole point of declaring a port UDP in the first place. The probe list
	// builder should exclude it, and the UDP port's proxy (always 0) should
	// not be registered either.
	var probedPorts []int
	var probeMu sync.Mutex
	rt := batchRuntime{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		readyTimeout: time.Second,
		readyPoll:    10 * time.Millisecond,
		tcpCheck: func(p int) bool {
			probeMu.Lock()
			probedPorts = append(probedPorts, p)
			probeMu.Unlock()
			return true
		},
	}

	var registerMu sync.Mutex
	var registerCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/__mdp/register" {
			registerMu.Lock()
			registerCount++
			registerMu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := batchAlloc{
		name:     "infra",
		svcGroup: "main",
		svc: config.ServiceConfig{
			// Commandless → external service code path.
			Env: map[string]config.EnvValue{
				"API_PORT":          {Value: "auto"},
				"JAEGER_AGENT_PORT": {Value: "auto"},
			},
			Ports: []config.PortMapping{
				{Env: "API_PORT", Proxy: 4000},
				{Env: "JAEGER_AGENT_PORT", Protocol: "udp"},
			},
		},
		portAssignments: map[string]int{"API_PORT": 40001, "JAEGER_AGENT_PORT": 54321},
		portProtocols:   map[string]string{"API_PORT": "tcp", "JAEGER_AGENT_PORT": "udp"},
	}

	bt := &batchTracker{}
	bt.wg.Add(1)
	states := depwait.NewStates([]string{"infra"})
	launchBatchService(context.Background(), bt, http.DefaultClient, srv.URL, "client-1", "test-repo", &a, states, rt, envexpand.PortMap{}, nil, nil)

	probeMu.Lock()
	defer probeMu.Unlock()
	for _, p := range probedPorts {
		if p == 54321 {
			t.Errorf("TCP probe called on UDP port 54321; UDP ports must be excluded from the probe list")
		}
	}
	registerMu.Lock()
	defer registerMu.Unlock()
	if registerCount != 1 {
		t.Errorf("register called %d times, want 1 (only the TCP API_PORT should register)", registerCount)
	}
}

func TestLaunchBatchServiceSkipsProxylessPorts(t *testing.T) {
	// Commandless services still get TCP-probed; stub the check so the test
	// doesn't block on unbound ports.
	rt := batchRuntime{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		readyTimeout: time.Second,
		readyPoll:    10 * time.Millisecond,
		tcpCheck:     func(int) bool { return true },
	}

	var registerMu sync.Mutex
	var registerBodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/__mdp/register" {
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			json.Unmarshal(b, &body)
			registerMu.Lock()
			registerBodies = append(registerBodies, body)
			registerMu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := batchAlloc{
		name:     "infra",
		svcGroup: "main",
		svc: config.ServiceConfig{
			// Command empty → no process launched.
			Env: map[string]config.EnvValue{
				"API_PORT": {Value: "auto"},
				"DB_PORT":  {Value: "auto"},
			},
			Ports: []config.PortMapping{
				{Env: "API_PORT", Proxy: 4000},
				{Env: "DB_PORT"}, // no proxy — should be skipped
			},
		},
		portAssignments: map[string]int{"API_PORT": 40001, "DB_PORT": 54321},
	}

	bt := &batchTracker{}
	bt.wg.Add(1)
	states := depwait.NewStates([]string{"infra"})
	launchBatchService(context.Background(), bt, http.DefaultClient, srv.URL, "client-1", "test-repo", &a, states, rt, envexpand.PortMap{}, nil, nil)

	registerMu.Lock()
	defer registerMu.Unlock()
	if len(registerBodies) != 1 {
		t.Fatalf("expected 1 register call, got %d", len(registerBodies))
	}
	body := registerBodies[0]
	if got, _ := body["proxyPort"].(float64); int(got) != 4000 {
		t.Errorf("proxyPort = %v, want 4000", body["proxyPort"])
	}
	if got, _ := body["port"].(float64); int(got) != 40001 {
		t.Errorf("port = %v, want 40001", body["port"])
	}
	if got, _ := body["name"].(string); got != "main/infra" {
		t.Errorf("name = %v, want main/infra", body["name"])
	}
}

func TestLaunchBatchServiceWaitsForDependencies(t *testing.T) {
	gateA := make(chan struct{})
	rt := batchRuntime{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		readyTimeout: 5 * time.Second,
		readyPoll:    10 * time.Millisecond,
		tcpCheck: func(p int) bool {
			if p == 19001 {
				select {
				case <-gateA:
					return true
				default:
					return false
				}
			}
			return true
		},
	}

	type regCall struct {
		name string
		at   time.Time
	}
	var regMu sync.Mutex
	var regs []regCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/__mdp/register" {
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			json.Unmarshal(b, &body)
			name, _ := body["name"].(string)
			regMu.Lock()
			regs = append(regs, regCall{name: name, at: time.Now()})
			regMu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	allocs := []batchAlloc{
		{name: "a", svcGroup: "test", assignedPort: 19001, svc: config.ServiceConfig{Command: "sleep 30", Proxy: 3000}},
		{name: "b", svcGroup: "test", assignedPort: 19002, svc: config.ServiceConfig{Command: "sleep 30", Proxy: 3001, DependsOn: []string{"a"}}},
	}
	bt := &batchTracker{}
	states := depwait.NewStates([]string{"a", "b"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		bt.killAll()
		bt.wg.Wait()
	})

	for _, a := range allocs {
		bt.wg.Add(1)
		go launchBatchService(ctx, bt, http.DefaultClient, srv.URL, "c1", "test-repo", &a, states, rt, envexpand.PortMap{}, nil, nil)
	}

	waitFor := func(name string, dur time.Duration) bool {
		deadline := time.Now().Add(dur)
		for time.Now().Before(deadline) {
			regMu.Lock()
			for _, r := range regs {
				if r.name == name {
					regMu.Unlock()
					return true
				}
			}
			regMu.Unlock()
			time.Sleep(10 * time.Millisecond)
		}
		return false
	}

	if !waitFor("test/a", time.Second) {
		t.Fatal("a did not register")
	}

	// b must not register until a's TCP becomes ready.
	time.Sleep(150 * time.Millisecond)
	regMu.Lock()
	for _, r := range regs {
		if r.name == "test/b" {
			regMu.Unlock()
			t.Fatalf("b registered before a's TCP ready")
		}
	}
	regMu.Unlock()

	close(gateA)

	if !waitFor("test/b", 2*time.Second) {
		t.Fatal("b did not register after a became ready")
	}
}

func TestLaunchBatchServiceReturnsOnContextCancel(t *testing.T) {
	// Regression: on SIGINT, batchCancel() must unblock goroutines still
	// waiting on deps so shutdown isn't held up by the full readiness timeout.
	rt := batchRuntime{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		readyTimeout: 30 * time.Second,
		readyPoll:    10 * time.Millisecond,
		tcpCheck:     func(int) bool { return true },
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	// b depends on a; a's state.Done is never closed → b blocks in depwait.Wait.
	states := depwait.NewStates([]string{"a", "b"})
	bt := &batchTracker{}
	ctx, cancel := context.WithCancel(context.Background())

	a := batchAlloc{
		name:         "b",
		svcGroup:     "test",
		assignedPort: 29001,
		svc:          config.ServiceConfig{Command: "sleep 30", DependsOn: []string{"a"}},
	}

	bt.wg.Add(1)
	done := make(chan struct{})
	go func() {
		launchBatchService(ctx, bt, http.DefaultClient, srv.URL, "c1", "test-repo", &a, states, rt, envexpand.PortMap{}, nil, nil)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond) // let goroutine enter the dep wait
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("launchBatchService did not return promptly after ctx cancel")
	}
}

func TestLaunchBatchServiceHookOrdering(t *testing.T) {
	// Stub TCP readiness so the probe against our no-op main command's
	// unbound port doesn't block the test.
	rt := batchRuntime{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		readyTimeout: time.Second,
		readyPoll:    10 * time.Millisecond,
		tcpCheck:     func(int) bool { return true },
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.log")
	appendCmd := func(tag string) string {
		return "sh -c 'echo " + tag + " >> " + logPath + "'"
	}

	var regMu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/__mdp/register" {
			regMu.Lock()
			f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			f.WriteString("register\n")
			f.Close()
			regMu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := batchAlloc{
		name:         "web",
		svcGroup:     "main",
		assignedPort: 40101,
		svc: config.ServiceConfig{
			Setup:    []string{appendCmd("setup")},
			Command:  appendCmd("main"),
			Shutdown: []string{appendCmd("shutdown")},
			Proxy:    3000,
		},
	}

	bt := &batchTracker{}
	bt.wg.Add(1)
	states := depwait.NewStates([]string{"web"})
	launchBatchService(context.Background(), bt, http.DefaultClient, srv.URL, "c1", "test-repo", &a, states, rt, envexpand.PortMap{}, nil, nil)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "setup\nregister\nmain\nshutdown"
	if got != want {
		t.Errorf("hook ordering:\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestLaunchBatchServiceSetupFailureSkipsRegistration(t *testing.T) {
	rt := batchRuntime{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		readyTimeout: time.Second,
		readyPoll:    10 * time.Millisecond,
		tcpCheck:     func(int) bool { return true },
	}

	dir := t.TempDir()
	mainSentinel := filepath.Join(dir, "main-ran")

	var regMu sync.Mutex
	var regCount int
	var deregCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		regMu.Lock()
		defer regMu.Unlock()
		switch {
		case r.Method == "POST" && r.URL.Path == "/__mdp/register":
			regCount++
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/__mdp/register/"):
			deregCount++
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := batchAlloc{
		name:         "web",
		svcGroup:     "main",
		assignedPort: 40102,
		svc: config.ServiceConfig{
			Setup:   []string{"sh -c 'exit 1'"},
			Command: "sh -c 'touch " + mainSentinel + "'",
			Proxy:   3000,
		},
	}

	bt := &batchTracker{}
	bt.wg.Add(1)
	states := depwait.NewStates([]string{"web"})
	launchBatchService(context.Background(), bt, http.DefaultClient, srv.URL, "c1", "test-repo", &a, states, rt, envexpand.PortMap{}, nil, nil)

	regMu.Lock()
	defer regMu.Unlock()
	if regCount != 0 {
		t.Errorf("expected no register calls, got %d", regCount)
	}
	if deregCount != 0 {
		t.Errorf("expected no deregister calls (nothing was registered), got %d", deregCount)
	}
	if _, err := os.Stat(mainSentinel); err == nil {
		t.Error("main command ran but should not have after setup failure")
	}
	if states["web"].Err == nil {
		t.Error("expected state.Err to be set after setup failure")
	}
}

func TestLaunchBatchServicePostStartRunsAfterReady(t *testing.T) {
	rt := batchRuntime{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		readyTimeout: time.Second,
		readyPoll:    10 * time.Millisecond,
		tcpCheck:     func(int) bool { return true },
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.log")
	appendCmd := func(tag string) string {
		return "sh -c 'echo " + tag + " >> " + logPath + "'"
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := batchAlloc{
		name:         "web",
		svcGroup:     "main",
		assignedPort: 40103,
		svc: config.ServiceConfig{
			Command:   "sh -c 'echo main >> " + logPath + "; sleep 0.4'",
			PostStart: config.PostStartConfig{Commands: []string{appendCmd("post_start")}},
			Shutdown:  []string{appendCmd("shutdown")},
			Proxy:     3000,
		},
	}

	bt := &batchTracker{}
	bt.wg.Add(1)
	states := depwait.NewStates([]string{"web"})
	launchBatchService(context.Background(), bt, http.DefaultClient, srv.URL, "c1", "test-repo", &a, states, rt, envexpand.PortMap{}, nil, nil)

	lines := strings.Split(strings.TrimSpace(readFile(t, logPath)), "\n")
	post := slices.Index(lines, "post_start")
	shutdown := slices.Index(lines, "shutdown")
	if post == -1 {
		t.Fatalf("post_start hook did not run; log=%v", lines)
	}
	if shutdown == -1 || shutdown < post {
		t.Errorf("shutdown hooks must run after post_start; log=%v", lines)
	}
	if states["web"].Err != nil {
		t.Errorf("state.Err = %v, want nil", states["web"].Err)
	}
}

func TestLaunchBatchServicePostStartFailureKeepsRunning(t *testing.T) {
	rt := batchRuntime{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		readyTimeout: time.Second,
		readyPoll:    10 * time.Millisecond,
		tcpCheck:     func(int) bool { return true },
	}

	dir := t.TempDir()
	secondSentinel := filepath.Join(dir, "second-ran")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := batchAlloc{
		name:         "web",
		svcGroup:     "main",
		assignedPort: 40104,
		svc: config.ServiceConfig{
			Command: "sh -c 'sleep 0.3'",
			PostStart: config.PostStartConfig{Commands: []string{
				"sh -c 'exit 1'",
				"sh -c 'touch " + secondSentinel + "'",
			}},
			Proxy: 3000,
		},
	}

	bt := &batchTracker{}
	bt.wg.Add(1)
	states := depwait.NewStates([]string{"web"})
	launchBatchService(context.Background(), bt, http.DefaultClient, srv.URL, "c1", "test-repo", &a, states, rt, envexpand.PortMap{}, nil, nil)

	if _, err := os.Stat(secondSentinel); err != nil {
		t.Error("second post_start command did not run after the first failed")
	}
	if states["web"].Err != nil {
		t.Errorf("state.Err = %v, want nil (post_start failures are best-effort)", states["web"].Err)
	}
}

func TestLaunchBatchServicePostStartWaitsForDockerGate(t *testing.T) {
	var probeCalls atomic.Int64
	rt := batchRuntime{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		readyTimeout: time.Second,
		readyPoll:    10 * time.Millisecond,
		tcpCheck:     func(int) bool { return true },
		buildProbe: func(*config.HealthCheck, int, string) func() bool {
			return func() bool { return probeCalls.Add(1) >= 3 }
		},
	}

	dir := t.TempDir()
	sentinel := filepath.Join(dir, "hook-ran")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := batchAlloc{
		name:         "web",
		svcGroup:     "main",
		assignedPort: 40105,
		svc: config.ServiceConfig{
			Command:     "sh -c 'sleep 0.1'",
			HealthCheck: &config.HealthCheck{DockerServices: []string{"db"}},
			PostStart:   config.PostStartConfig{Commands: []string{"sh -c 'touch " + sentinel + "'"}},
			Proxy:       3000,
		},
	}

	bt := &batchTracker{}
	bt.wg.Add(1)
	states := depwait.NewStates([]string{"web"})
	launchBatchService(context.Background(), bt, http.DefaultClient, srv.URL, "c1", "test-repo", &a, states, rt, envexpand.PortMap{}, nil, nil)

	if _, err := os.Stat(sentinel); err != nil {
		t.Error("post_start hook did not run after docker gate passed")
	}
	if got := probeCalls.Load(); got < 3 {
		t.Errorf("probe called %d times, want >= 3 (hooks must wait for the gate)", got)
	}
}

func TestLaunchBatchServicePostStartSkippedWhenDockerGateTimesOut(t *testing.T) {
	rt := batchRuntime{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		readyTimeout: 200 * time.Millisecond,
		readyPoll:    10 * time.Millisecond,
		tcpCheck:     func(int) bool { return true },
		buildProbe: func(*config.HealthCheck, int, string) func() bool {
			return func() bool { return false }
		},
	}

	dir := t.TempDir()
	sentinel := filepath.Join(dir, "hook-ran")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := batchAlloc{
		name:         "web",
		svcGroup:     "main",
		assignedPort: 40106,
		svc: config.ServiceConfig{
			Command:     "sh -c 'sleep 0.1'",
			HealthCheck: &config.HealthCheck{DockerServices: []string{"db"}},
			PostStart:   config.PostStartConfig{Commands: []string{"sh -c 'touch " + sentinel + "'"}},
			Proxy:       3000,
		},
	}

	bt := &batchTracker{}
	bt.wg.Add(1)
	states := depwait.NewStates([]string{"web"})
	launchBatchService(context.Background(), bt, http.DefaultClient, srv.URL, "c1", "test-repo", &a, states, rt, envexpand.PortMap{}, nil, nil)

	if _, err := os.Stat(sentinel); err == nil {
		t.Error("post_start hook ran but the docker gate never passed")
	}
	if states["web"].Err != nil {
		t.Errorf("state.Err = %v, want nil (a skipped post_start must not fail the service)", states["web"].Err)
	}
}

func TestLaunchBatchServicePostStartDoesNotBlockReady(t *testing.T) {
	rt := batchRuntime{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		readyTimeout: time.Second,
		readyPoll:    10 * time.Millisecond,
		tcpCheck:     func(int) bool { return true },
	}

	dir := t.TempDir()
	sentinel := filepath.Join(dir, "hook-ran")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := batchAlloc{
		name:         "web",
		svcGroup:     "main",
		assignedPort: 40107,
		svc: config.ServiceConfig{
			Command:   "sh -c 'sleep 2'",
			PostStart: config.PostStartConfig{Commands: []string{"sh -c 'sleep 0.5; touch " + sentinel + "'"}},
			Proxy:     3000,
		},
	}

	bt := &batchTracker{}
	bt.wg.Add(1)
	states := depwait.NewStates([]string{"web"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		launchBatchService(ctx, bt, http.DefaultClient, srv.URL, "c1", "test-repo", &a, states, rt, envexpand.PortMap{}, nil, nil)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		bt.killAll()
		waitDoneOrFail(t, done)
	})

	select {
	case <-states["web"].Done:
	case <-time.After(time.Second):
		t.Fatal("state.Done did not close; readiness must not wait on post_start")
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("post_start finished before readiness was signaled; expected it to still be running")
	}
}

func TestLaunchBatchServicePostStartExternalUpstream(t *testing.T) {
	rt := batchRuntime{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		readyTimeout: time.Second,
		readyPoll:    10 * time.Millisecond,
		tcpCheck:     func(int) bool { return true },
	}

	dir := t.TempDir()
	sentinel := filepath.Join(dir, "hook-ran")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := batchAlloc{
		name:         "infra",
		svcGroup:     "main",
		assignedPort: 40108,
		svc: config.ServiceConfig{
			// Command empty → external upstream path.
			PostStart: config.PostStartConfig{Commands: []string{"sh -c 'touch " + sentinel + "'"}},
			Proxy:     3000,
		},
	}

	bt := &batchTracker{}
	bt.wg.Add(1)
	states := depwait.NewStates([]string{"infra"})
	launchBatchService(context.Background(), bt, http.DefaultClient, srv.URL, "c1", "test-repo", &a, states, rt, envexpand.PortMap{}, nil, nil)

	if _, err := os.Stat(sentinel); err != nil {
		t.Error("post_start hook did not run for external upstream")
	}
}

func TestSuperviseProcessPostStartOnRestart(t *testing.T) {
	for _, tc := range []struct {
		name      string
		onRestart bool
		wantCalls int64
	}{
		{"on_restart true reruns hooks", true, 1},
		{"default does not rerun", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var port atomic.Int64
			port.Store(9001)
			var peerHits atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/__mdp/peers" {
					json.NewEncoder(w).Encode(map[string]any{
						"port": port.Load(),
						"env":  map[string]string{},
					})
					peerHits.Add(1)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			prev := peerWatchInterval
			peerWatchInterval = 20 * time.Millisecond
			t.Cleanup(func() { peerWatchInterval = prev })

			command := "sh -c 'exec sleep 60'"
			a := &batchAlloc{
				name:     "frontend",
				svcGroup: "dev",
				svc: config.ServiceConfig{
					Command: command,
					PostStart: config.PostStartConfig{
						Commands:  []string{"echo seeded"},
						OnRestart: tc.onRestart,
					},
					Env: map[string]config.EnvValue{
						"TARGET_PORT": {Ref: "@backend.api.port"},
					},
				},
			}
			resolver := newPeerResolver(http.DefaultClient, srv.URL, "dev", nil)
			initialEnv, err := buildBatchEnv(*a, envexpand.PortMap{}, resolver)
			if err != nil {
				t.Fatalf("initial env: %v", err)
			}
			a.env = initialEnv

			bt := &batchTracker{}
			pw := newPrefixWriter("test", "0;0", os.Stdout)
			pwErr := newPrefixWriter("test", "0;0", os.Stderr)
			cmd, err := startBatchCommand(bt, command, "", initialEnv, pw, pwErr)
			if err != nil {
				t.Fatalf("startBatchCommand: %v", err)
			}

			var hookCalls atomic.Int64
			hookWaitTCP := make(chan bool, 4)
			runPostStart := func(env []string, waitTCP bool) {
				hookCalls.Add(1)
				hookWaitTCP <- waitTCP
			}

			registerAll := func() ([]string, error) { return nil, nil }
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				superviseProcess(ctx, cmd, bt, http.DefaultClient, srv.URL, a, nil, registerAll, runPostStart, envexpand.PortMap{}, resolver, nil, pw, pwErr)
				close(done)
			}()
			t.Cleanup(func() {
				cancel()
				bt.killAll()
				waitDoneOrFail(t, done)
				bt.wg.Wait()
			})

			// Wait for the supervisor to seed its peer baseline before flipping:
			// the 1st /__mdp/peers hit is buildBatchEnv's initial resolve, the
			// 2nd is superviseProcess's seed.
			seedDeadline := time.Now().Add(2 * time.Second)
			for peerHits.Load() < 2 && time.Now().Before(seedDeadline) {
				time.Sleep(5 * time.Millisecond)
			}
			port.Store(9999) // trigger a peer-change restart

			deadline := time.Now().Add(2 * time.Second)
			for hookCalls.Load() < tc.wantCalls && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if tc.onRestart {
				if got := hookCalls.Load(); got != 1 {
					t.Fatalf("runPostStart called %d times, want 1", got)
				}
				if waitTCP := <-hookWaitTCP; !waitTCP {
					t.Error("restart re-run must pass waitTCP=true")
				}
			} else {
				// Give the restart time to land, then confirm no call.
				time.Sleep(300 * time.Millisecond)
				if got := hookCalls.Load(); got != 0 {
					t.Fatalf("runPostStart called %d times, want 0", got)
				}
			}
		})
	}
}

func TestSuperviseProcessReturnsErrorOnCrash(t *testing.T) {
	bt := &batchTracker{}
	pw := newPrefixWriter("test", "0;0", os.Stdout)
	pwErr := newPrefixWriter("test", "0;0", os.Stderr)
	a := &batchAlloc{name: "svc", svcGroup: "test", svc: config.ServiceConfig{Command: "sh -c 'exit 1'"}}
	cmd, err := startBatchCommand(bt, a.svc.Command, "", nil, pw, pwErr)
	if err != nil {
		t.Fatalf("startBatchCommand: %v", err)
	}

	registerAll := func() ([]string, error) { return nil, nil }
	runPostStart := func([]string, bool) {}
	err = superviseProcess(context.Background(), cmd, bt, http.DefaultClient, "", a, nil, registerAll, runPostStart, envexpand.PortMap{}, nil, nil, pw, pwErr)
	if err == nil {
		t.Fatal("expected non-nil error from a crashed process, got nil")
	}
}

func TestSuperviseProcessReturnsNilOnCleanExit(t *testing.T) {
	bt := &batchTracker{}
	pw := newPrefixWriter("test", "0;0", os.Stdout)
	pwErr := newPrefixWriter("test", "0;0", os.Stderr)
	a := &batchAlloc{name: "svc", svcGroup: "test", svc: config.ServiceConfig{Command: "sh -c 'exit 0'"}}
	cmd, err := startBatchCommand(bt, a.svc.Command, "", nil, pw, pwErr)
	if err != nil {
		t.Fatalf("startBatchCommand: %v", err)
	}

	registerAll := func() ([]string, error) { return nil, nil }
	runPostStart := func([]string, bool) {}
	err = superviseProcess(context.Background(), cmd, bt, http.DefaultClient, "", a, nil, registerAll, runPostStart, envexpand.PortMap{}, nil, nil, pw, pwErr)
	if err != nil {
		t.Fatalf("expected nil error from a clean exit, got %v", err)
	}
}

func TestSuperviseProcessReturnsNilOnCtxCancel(t *testing.T) {
	// A signal-driven shutdown (ctx canceled, process SIGTERM'd) is not a
	// crash — it must not be attributed to the service as an error, or every
	// ordinary Ctrl-C would look like every service failed.
	bt := &batchTracker{}
	pw := newPrefixWriter("test", "0;0", os.Stdout)
	pwErr := newPrefixWriter("test", "0;0", os.Stderr)
	a := &batchAlloc{name: "svc", svcGroup: "test", svc: config.ServiceConfig{Command: "sh -c 'sleep 30'"}}
	cmd, err := startBatchCommand(bt, a.svc.Command, "", nil, pw, pwErr)
	if err != nil {
		t.Fatalf("startBatchCommand: %v", err)
	}

	registerAll := func() ([]string, error) { return nil, nil }
	runPostStart := func([]string, bool) {}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- superviseProcess(ctx, cmd, bt, http.DefaultClient, "", a, nil, registerAll, runPostStart, envexpand.PortMap{}, nil, nil, pw, pwErr)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil error on ctx-cancelled shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("superviseProcess did not return after ctx cancel")
	}
}

func TestLaunchBatchServiceSetsStateErrOnProcessCrash(t *testing.T) {
	rt := batchRuntime{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		readyTimeout: time.Second,
		readyPoll:    10 * time.Millisecond,
		tcpCheck:     func(int) bool { return true },
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := batchAlloc{
		name:         "web",
		svcGroup:     "main",
		assignedPort: 40201,
		svc:          config.ServiceConfig{Command: "sh -c 'exit 1'", Proxy: 3000},
	}

	bt := &batchTracker{}
	bt.wg.Add(1)
	states := depwait.NewStates([]string{"web"})
	launchBatchService(context.Background(), bt, http.DefaultClient, srv.URL, "c1", "test-repo", &a, states, rt, envexpand.PortMap{}, nil, nil)

	if states["web"].Err == nil {
		t.Fatal("expected state.Err to be set after the service's process crashed")
	}
}

func TestRunSoloNoEnvOverride(t *testing.T) {
	err := runSolo([]string{"sh", "-c", `test -z "$MDP" && test -z "$PORT"`}, config.LogSplitConfig{})
	if err != nil {
		t.Fatalf("runSolo should not set MDP or PORT: %v", err)
	}
}

func TestDeregisterFromOrchestrator(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	deregisterFromOrchestrator(srv.URL, "app/main")

	if gotMethod != "DELETE" {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	if gotPath != "/__mdp/register/app/main" {
		t.Errorf("expected path /__mdp/register/app/main, got %s", gotPath)
	}
}

func TestDeregisterFromOrchestratorNoOp(t *testing.T) {
	deregisterFromOrchestrator("", "foo")
	deregisterFromOrchestrator("http://localhost:1234", "")
	deregisterFromOrchestrator("", "")
}

func TestRunProxiedDisconnectsOnChildExit(t *testing.T) {
	var disconnectCalled atomic.Int32
	var gotPath string
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/__mdp/disconnect" {
			disconnectCalled.Add(1)
			gotPath = r.URL.Path
			defer func() { close(done) }()
		}
		if r.URL.Path == "/__mdp/shutdown/watch" {
			<-done
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := runProxied(
		[]string{"sh", "-c", "exit 0"},
		"PORT", 12345, srv.URL, "test/svc", "test-client-id", config.LogSplitConfig{},
	)
	if err != nil {
		t.Fatalf("runProxied: %v", err)
	}
	if disconnectCalled.Load() != 1 {
		t.Errorf("expected 1 disconnect call, got %d", disconnectCalled.Load())
	}
	if gotPath != "/__mdp/disconnect" {
		t.Errorf("expected path /__mdp/disconnect, got %s", gotPath)
	}
}

func TestRunProxiedDisconnectsOnNonZeroExit(t *testing.T) {
	var disconnectCalled atomic.Int32
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/__mdp/disconnect" {
			disconnectCalled.Add(1)
			defer func() { close(done) }()
		}
		if r.URL.Path == "/__mdp/shutdown/watch" {
			<-done
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := runProxied(
		[]string{"sh", "-c", "exit 0"},
		"PORT", 12345, srv.URL, "test/svc", "test-client-id", config.LogSplitConfig{},
	)
	if err != nil {
		t.Fatalf("runProxied: %v", err)
	}
	if disconnectCalled.Load() != 1 {
		t.Errorf("expected 1 disconnect call, got %d", disconnectCalled.Load())
	}
}

func TestRunProxiedSetsMDPEnv(t *testing.T) {
	done := make(chan struct{})
	var closeOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__mdp/shutdown/watch" {
			<-done
			return
		}
		if r.Method == "POST" && r.URL.Path == "/__mdp/disconnect" {
			closeOnce.Do(func() { close(done) })
		}
		// Also handle PATCH (updatePID) with empty name — no-op on server
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Close done after runProxied returns (clientID is empty so disconnect is skipped)
	defer closeOnce.Do(func() { close(done) })

	err := runProxied(
		[]string{"sh", "-c", `test "$MDP" = "1" && test -n "$PORT"`},
		"PORT", 12345, srv.URL, "", "", config.LogSplitConfig{},
	)
	if err != nil {
		t.Fatalf("runProxied should set MDP=1 and PORT: %v", err)
	}
}

func TestBuildBatchEnvCrossRepoResolverHits(t *testing.T) {
	resolver := func(repo, svc string, isEnv bool, key string) (string, bool) {
		if repo == "backend" && svc == "api" && !isEnv && key == "port" {
			return "9999", true
		}
		if repo == "backend" && svc == "api" && isEnv && key == "URL" {
			return "http://backend", true
		}
		return "", false
	}
	a := batchAlloc{
		name:         "frontend",
		svcGroup:     "test",
		assignedPort: 3000,
		svc: config.ServiceConfig{
			Env: map[string]config.EnvValue{
				"API_URL":     {Value: "http://localhost:${@backend.api.port}"},
				"AUTH":        {Ref: "@backend.api.env.URL"},
				"FALLBACK":    {Value: "${@backend.unknown.port:-7777}"},
				"OMIT":        {Ref: "@backend.unknown.port"},
				"OMIT_INTERP": {Value: "x=${@backend.unknown.env.X:-}"},
			},
		},
	}
	env, err := buildBatchEnv(a, envexpand.PortMap{}, resolver)
	if err != nil {
		t.Fatalf("buildBatchEnv: %v", err)
	}
	got := map[string]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			got[kv[:i]] = kv[i+1:]
		}
	}
	if got["API_URL"] != "http://localhost:9999" {
		t.Errorf("API_URL = %q", got["API_URL"])
	}
	if got["AUTH"] != "http://backend" {
		t.Errorf("AUTH = %q", got["AUTH"])
	}
	if got["FALLBACK"] != "7777" {
		t.Errorf("FALLBACK = %q", got["FALLBACK"])
	}
	if _, exists := got["OMIT"]; exists {
		t.Errorf("OMIT should be omitted; got %q", got["OMIT"])
	}
	if got["OMIT_INTERP"] != "x=" {
		t.Errorf("OMIT_INTERP = %q, want x=", got["OMIT_INTERP"])
	}
}

func TestSuperviseProcessRestartsOnPeerChange(t *testing.T) {
	// Stand up a fake control API that returns a port we can flip.
	var port atomic.Int64
	port.Store(9001)
	// peerHits counts /__mdp/peers responses, incremented after encoding so a
	// given count guarantees that many responses were sent with the port value
	// current at send time. Used below to gate the flip on the supervisor's seed.
	var peerHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__mdp/peers" {
			json.NewEncoder(w).Encode(map[string]any{
				"port": port.Load(),
				"env":  map[string]string{},
			})
			peerHits.Add(1)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Speed up the watcher so the test stays fast.
	prev := peerWatchInterval
	peerWatchInterval = 20 * time.Millisecond
	t.Cleanup(func() { peerWatchInterval = prev })

	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	// `exec sleep` replaces the shell so SIGKILL targets the sleep directly,
	// otherwise cmd.Wait blocks on the inherited stdout pipe until sleep exits.
	command := "sh -c 'echo $TARGET_PORT >> " + logPath + "; exec sleep 60'"

	a := &batchAlloc{
		name:     "frontend",
		svcGroup: "dev",
		svc: config.ServiceConfig{
			Command: command,
			Env: map[string]config.EnvValue{
				"TARGET_PORT": {Ref: "@backend.api.port"},
			},
		},
	}
	resolver := newPeerResolver(http.DefaultClient, srv.URL, "dev", nil)
	initialEnv, err := buildBatchEnv(*a, envexpand.PortMap{}, resolver)
	if err != nil {
		t.Fatalf("initial env: %v", err)
	}
	a.env = initialEnv

	bt := &batchTracker{}
	pw := newPrefixWriter("test", "0;0", os.Stdout)
	pwErr := newPrefixWriter("test", "0;0", os.Stderr)
	cmd, err := startBatchCommand(bt, command, "", initialEnv, pw, pwErr)
	if err != nil {
		t.Fatalf("startBatchCommand: %v", err)
	}

	registerAll := func() ([]string, error) { return nil, nil }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		superviseProcess(ctx, cmd, bt, http.DefaultClient, srv.URL, a, nil, registerAll, func([]string, bool) {}, envexpand.PortMap{}, resolver, nil, pw, pwErr)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		bt.killAll()
		waitDoneOrFail(t, done)
		bt.wg.Wait()
	})

	// Wait for the initial run to write 9001.
	waitFor := func(want string, dur time.Duration) bool {
		deadline := time.Now().Add(dur)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(logPath)
			if strings.Contains(string(data), want) {
				return true
			}
			time.Sleep(20 * time.Millisecond)
		}
		return false
	}
	if !waitFor("9001", 2*time.Second) {
		t.Fatalf("initial cmd never wrote 9001; log=%q", readFile(t, logPath))
	}

	// Wait for the supervisor to seed its peer baseline at 9001 before flipping.
	// The 1st /__mdp/peers hit is buildBatchEnv's initial resolve (above); the
	// 2nd is superviseProcess's seed. If the flip lands before the seed (e.g. a
	// loaded CI runner delays the supervisor goroutine), the seed reads 9999 and
	// the watcher never sees a change — the flaky "restart cmd never wrote 9999".
	seedDeadline := time.Now().Add(2 * time.Second)
	for peerHits.Load() < 2 && time.Now().Before(seedDeadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if peerHits.Load() < 2 {
		t.Fatalf("supervisor never seeded peer baseline; /__mdp/peers hits=%d", peerHits.Load())
	}

	// Flip the peer port; the supervisor should kill the cmd and relaunch
	// with TARGET_PORT=9999.
	port.Store(9999)

	if !waitFor("9999", 3*time.Second) {
		t.Fatalf("restart cmd never wrote 9999; log=%q", readFile(t, logPath))
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, _ := os.ReadFile(path)
	return string(data)
}

func TestBuildBatchEnvLocalRefMissingErrors(t *testing.T) {
	a := batchAlloc{
		name: "x",
		svc: config.ServiceConfig{
			Env: map[string]config.EnvValue{
				"BAD": {Ref: "nope.port"}, // local; not present in portMap
			},
		},
	}
	if _, err := buildBatchEnv(a, envexpand.PortMap{}, nil); err == nil {
		t.Fatal("expected error for unresolved local ref")
	}
}

func TestExportBatchEnvFilesWritesGlobalAndPerService(t *testing.T) {
	tmp := t.TempDir()
	globalPath := filepath.Join(tmp, "global.env")
	apiEnvPath := filepath.Join(tmp, "api.env")
	webEnvPath := filepath.Join(tmp, "web.env")

	cfg := &config.Config{
		Global: config.GlobalConfig{
			EnvFile: globalPath,
			Env: map[string]config.EnvValue{
				"API_PORT": {Ref: "api.env.PORT"},
				"API_URL":  {Value: "http://localhost:${api.PORT}"},
				"WEB_MODE": {Ref: "web.env.MODE"},
			},
		},
	}

	allocations := []batchAlloc{
		{
			name:         "api",
			svcGroup:     "test",
			assignedPort: 40100,
			svc: config.ServiceConfig{
				EnvFile: apiEnvPath,
				Env:     map[string]config.EnvValue{"NAME": {Value: "api"}},
			},
		},
		{
			name:         "web",
			svcGroup:     "test",
			assignedPort: 40101,
			svc: config.ServiceConfig{
				EnvFile: webEnvPath,
				Env:     map[string]config.EnvValue{"MODE": {Value: "dev"}},
			},
		},
	}
	portMap := envexpand.PortMap{
		"api": {"port": 40100, "PORT": 40100},
		"web": {"port": 40101, "PORT": 40101},
	}

	if err := exportBatchEnvFiles(cfg, allocations, portMap, nil, nil, nil); err != nil {
		t.Fatalf("exportBatchEnvFiles: %v", err)
	}

	gdata, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read global: %v", err)
	}
	gtext := string(gdata)
	for _, want := range []string{
		`API_PORT="40100"`,
		`API_URL="http://localhost:40100"`,
		`WEB_MODE="dev"`,
	} {
		if !strings.Contains(gtext, want) {
			t.Errorf("global missing %q in:\n%s", want, gtext)
		}
	}

	adata, err := os.ReadFile(apiEnvPath)
	if err != nil {
		t.Fatalf("read api: %v", err)
	}
	atext := string(adata)
	for _, want := range []string{`NAME="api"`, `PORT="40100"`} {
		if !strings.Contains(atext, want) {
			t.Errorf("api missing %q in:\n%s", want, atext)
		}
	}

	wdata, err := os.ReadFile(webEnvPath)
	if err != nil {
		t.Fatalf("read web: %v", err)
	}
	wtext := string(wdata)
	for _, want := range []string{`MODE="dev"`, `PORT="40101"`} {
		if !strings.Contains(wtext, want) {
			t.Errorf("web missing %q in:\n%s", want, wtext)
		}
	}

	if allocations[0].env == nil || allocations[1].env == nil {
		t.Fatal("expected allocations[i].env to be populated for launch goroutines")
	}
}

func TestExportBatchEnvFilesSkipsWhenNoEnvFile(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{}
	allocations := []batchAlloc{
		{
			name:         "api",
			svcGroup:     "test",
			assignedPort: 40200,
			svc:          config.ServiceConfig{Env: map[string]config.EnvValue{"X": {Value: "y"}}},
		},
	}
	if err := exportBatchEnvFiles(cfg, allocations, envexpand.PortMap{}, nil, nil, nil); err != nil {
		t.Fatalf("exportBatchEnvFiles: %v", err)
	}
	entries, _ := os.ReadDir(tmp)
	if len(entries) != 0 {
		t.Errorf("expected no files written, got: %v", entries)
	}
	if allocations[0].env == nil {
		t.Error("env should be populated even when no env files are configured")
	}
}

func TestExportBatchEnvFilesPropagatesExpansionError(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			EnvFile: filepath.Join(tmp, "global.env"),
			Env:     map[string]config.EnvValue{"X": {Ref: "nope.env.MISSING"}},
		},
	}
	allocations := []batchAlloc{
		{
			name:         "api",
			svcGroup:     "test",
			assignedPort: 40300,
			svc:          config.ServiceConfig{Env: map[string]config.EnvValue{"A": {Value: "b"}}},
		},
	}
	err := exportBatchEnvFiles(cfg, allocations, envexpand.PortMap{"api": {"port": 40300, "PORT": 40300}}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error from unresolved ref")
	}
	if !strings.Contains(err.Error(), "write global env file") {
		t.Errorf("expected wrapped global env file error, got: %v", err)
	}
}

// TestExportBatchEnvFilesUsesPerAllocResolver verifies that each allocation's
// resolver is the one used to resolve its env — i.e. a service overriding its
// `group:` resolves cross-repo refs against that override, not the workspace's
// top-level group. Regression test for a bug where one shared resolver was
// reused for every allocation.
func TestExportBatchEnvFilesUsesPerAllocResolver(t *testing.T) {
	allocations := []batchAlloc{
		{
			name:     "frontend",
			svcGroup: "feature-x",
			svc: config.ServiceConfig{
				Env: map[string]config.EnvValue{
					"BACKEND_PORT": {Ref: "@backend.api.port"},
				},
			},
		},
		{
			name:     "infra",
			svcGroup: "shared",
			svc: config.ServiceConfig{
				Env: map[string]config.EnvValue{
					"BACKEND_PORT": {Ref: "@backend.api.port"},
				},
			},
		},
	}

	allocResolvers := []envexpand.Resolver{
		// frontend resolver: only finds the peer in feature-x.
		func(repo, svc string, isEnv bool, key string) (string, bool) {
			if repo == "backend" && svc == "api" && key == "port" {
				return "8001", true
			}
			return "", false
		},
		// infra resolver: only finds the peer in shared.
		func(repo, svc string, isEnv bool, key string) (string, bool) {
			if repo == "backend" && svc == "api" && key == "port" {
				return "9001", true
			}
			return "", false
		},
	}

	if err := exportBatchEnvFiles(&config.Config{}, allocations, envexpand.PortMap{}, allocResolvers, nil, nil); err != nil {
		t.Fatalf("exportBatchEnvFiles: %v", err)
	}

	if !contains(allocations[0].env, "BACKEND_PORT=8001") {
		t.Errorf("frontend env did not get its own resolver's value 8001; got: %v", allocations[0].env)
	}
	if !contains(allocations[1].env, "BACKEND_PORT=9001") {
		t.Errorf("infra env did not get its own resolver's value 9001; got: %v", allocations[1].env)
	}
}

func contains(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

func TestParseLinks(t *testing.T) {
	t.Run("nil and empty", func(t *testing.T) {
		m, err := parseLinks(nil)
		if err != nil || m != nil {
			t.Errorf("nil input: got (%v, %v), want (nil, nil)", m, err)
		}
		m, err = parseLinks([]string{})
		if err != nil || m != nil {
			t.Errorf("empty input: got (%v, %v), want (nil, nil)", m, err)
		}
	})

	t.Run("valid", func(t *testing.T) {
		m, err := parseLinks([]string{"backend=main", "auth=derek/foo"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m["backend"] != "main" || m["auth"] != "derek/foo" {
			t.Errorf("got %v, want backend=main auth=derek/foo", m)
		}
	})

	t.Run("last wins", func(t *testing.T) {
		m, err := parseLinks([]string{"backend=stale", "backend=main"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m["backend"] != "main" {
			t.Errorf("last-wins failed: got %q, want main", m["backend"])
		}
	})

	t.Run("invalid forms", func(t *testing.T) {
		for _, v := range []string{"", "backend", "=main", "backend=", " =main", "backend= "} {
			if _, err := parseLinks([]string{v}); err == nil {
				t.Errorf("%q: expected error, got nil", v)
			}
		}
	})
}

func TestResolveServiceSelection(t *testing.T) {
	def := "9"
	cfg := &config.Config{
		Services: map[string]config.ServiceConfig{
			"db":       {Command: "db"},
			"cache":    {Command: "cache"},
			"api":      {Command: "api", DependsOn: []string{"db", "cache"}},
			"worker":   {Command: "worker", DependsOn: []string{"db"}},
			"frontend": {Command: "fe", DependsOn: []string{"api"}},
			"solo":     {Command: "solo"},
			// Env-ref services: depend on siblings via env, not depends_on.
			"metrics": {Command: "m"},
			"reporter": {Command: "r", Env: map[string]config.EnvValue{
				"METRICS_URL": {Value: "http://localhost:${metrics.PORT}"},
			}},
			"queue": {Command: "q"},
			"ingest": {Command: "i", Env: map[string]config.EnvValue{
				"Q": {Ref: "queue.env.ADDR"},
			}},
			// Mirrors testbed web-main: depends_on api, env-refs db.
			"web": {Command: "w", DependsOn: []string{"api"}, Env: map[string]config.EnvValue{
				"DB_URL": {Value: "postgres://localhost:${db.DB_PORT}/app"},
			}},
			// Defaulted ref → tolerates absence, must not pull in (or error).
			"opt": {Command: "o", Env: map[string]config.EnvValue{
				"X": {Ref: "absent.PORT", Default: &def},
			}},
			// Cross-repo ref → resolved by orchestrator, must not pull in.
			"xrepo": {Command: "x", Env: map[string]config.EnvValue{
				"P": {Value: "${@backend.api.PORT}"},
			}},
		},
	}

	t.Run("empty selection returns nil", func(t *testing.T) {
		for _, in := range [][]string{nil, {}, {"", "  "}} {
			got, err := resolveServiceSelection(cfg, in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != nil {
				t.Errorf("input %v: got %v, want nil", in, got)
			}
		}
	})

	t.Run("single service with no deps", func(t *testing.T) {
		got, err := resolveServiceSelection(cfg, []string{"solo"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflectEqualSet(got, map[string]bool{"solo": true}) {
			t.Errorf("got %v, want {solo}", got)
		}
	})

	t.Run("transitive depends_on pulled in", func(t *testing.T) {
		got, err := resolveServiceSelection(cfg, []string{"frontend"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := map[string]bool{"frontend": true, "api": true, "db": true, "cache": true}
		if !reflectEqualSet(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("diamond deps not double-counted", func(t *testing.T) {
		// api and worker both pull in db; selecting both must not loop forever.
		got, err := resolveServiceSelection(cfg, []string{"api", "worker"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := map[string]bool{"api": true, "worker": true, "db": true, "cache": true}
		if !reflectEqualSet(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("comma-separated and whitespace tolerated", func(t *testing.T) {
		// Mirrors the env-var path which uses strings.Split on commas.
		got, err := resolveServiceSelection(cfg, []string{"solo", "  db  "})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := map[string]bool{"solo": true, "db": true}
		if !reflectEqualSet(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("unknown name errors with valid list", func(t *testing.T) {
		_, err := resolveServiceSelection(cfg, []string{"nope"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, "nope") {
			t.Errorf("error %q should mention the bad name", msg)
		}
		for _, name := range []string{"api", "db", "frontend"} {
			if !strings.Contains(msg, name) {
				t.Errorf("error %q should list valid name %q", msg, name)
			}
		}
	})

	t.Run("case sensitive", func(t *testing.T) {
		// "API" is not "api" — must error, not silently match.
		if _, err := resolveServiceSelection(cfg, []string{"API"}); err == nil {
			t.Error("expected case-sensitive mismatch to error")
		}
	})

	t.Run("value-form env ref pulled in", func(t *testing.T) {
		// reporter references ${metrics.PORT} but doesn't list it in depends_on.
		got, err := resolveServiceSelection(cfg, []string{"reporter"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := map[string]bool{"reporter": true, "metrics": true}
		if !reflectEqualSet(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("bare-ref-form env ref pulled in", func(t *testing.T) {
		got, err := resolveServiceSelection(cfg, []string{"ingest"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := map[string]bool{"ingest": true, "queue": true}
		if !reflectEqualSet(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("depends_on and env ref combine without double-count", func(t *testing.T) {
		// Mirrors testbed `mdp run --service web-main`: web depends_on api and
		// env-refs db. db arrives via both api's depends_on and web's env ref.
		got, err := resolveServiceSelection(cfg, []string{"web"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := map[string]bool{"web": true, "api": true, "db": true, "cache": true}
		if !reflectEqualSet(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("defaulted ref does not pull in or error", func(t *testing.T) {
		// opt's ref targets a nonexistent service but has a default, so it must
		// neither pull anything in nor error on the missing target.
		got, err := resolveServiceSelection(cfg, []string{"opt"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflectEqualSet(got, map[string]bool{"opt": true}) {
			t.Errorf("got %v, want {opt}", got)
		}
	})

	t.Run("cross-repo ref does not pull in", func(t *testing.T) {
		got, err := resolveServiceSelection(cfg, []string{"xrepo"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflectEqualSet(got, map[string]bool{"xrepo": true}) {
			t.Errorf("got %v, want {xrepo}", got)
		}
	})
}

func reflectEqualSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestExportBatchEnvFilesOmitsRefsToSkippedServices is the regression test for
// `mdp run --service <subset>` runs aborting when global.env references a
// service that was filtered out of the selection. Entries that reference a
// skipped local service must be silently omitted (matching the cross-repo
// unresolved-ref behavior); entries with defaults must use the default;
// entries that reference selected services must pass through normally.
func TestExportBatchEnvFilesOmitsRefsToSkippedServices(t *testing.T) {
	tmp := t.TempDir()
	globalPath := filepath.Join(tmp, "global.env")

	defaultPort := "3000"
	cfg := &config.Config{
		Global: config.GlobalConfig{
			EnvFile: globalPath,
			Env: map[string]config.EnvValue{
				"API_PORT":         {Ref: "api.PORT"},                                 // selected → kept
				"WEB_PORT":         {Ref: "web.PORT"},                                 // skipped, no default → omit
				"WEB_PORT_DEFAULT": {Ref: "web.PORT", Default: &defaultPort},          // skipped but default → kept via default
				"COMBO_URL":        {Value: "http://api:${api.PORT}/x?w=${web.PORT}"}, // mixes selected + skipped, no default → omit
				"COMBO_URL_DEF":    {Value: "http://${web.PORT:-3000}/"},              // skipped but inline default → kept
			},
		},
	}

	allocations := []batchAlloc{
		{
			name:         "api",
			svcGroup:     "test",
			assignedPort: 40100,
			svc:          config.ServiceConfig{Env: map[string]config.EnvValue{"X": {Value: "y"}}},
		},
	}
	portMap := envexpand.PortMap{"api": {"port": 40100, "PORT": 40100}}
	skipped := map[string]bool{"web": true}

	if err := exportBatchEnvFiles(cfg, allocations, portMap, nil, nil, skipped); err != nil {
		t.Fatalf("exportBatchEnvFiles: %v", err)
	}

	raw, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read global env: %v", err)
	}
	got := string(raw)

	mustContain := []string{`API_PORT="40100"`, `WEB_PORT_DEFAULT="3000"`, `COMBO_URL_DEF="http://3000/"`}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("global env file missing %q\n--- contents ---\n%s", want, got)
		}
	}

	mustNotContain := []string{"WEB_PORT=", "COMBO_URL="}
	for _, bad := range mustNotContain {
		// Allow "WEB_PORT_DEFAULT=" / "COMBO_URL_DEF=" — match exact key boundary.
		if strings.Contains(got, "\n"+bad) || strings.HasPrefix(got, bad) {
			t.Errorf("global env file should have omitted entry starting with %q\n--- contents ---\n%s", bad, got)
		}
	}
}
