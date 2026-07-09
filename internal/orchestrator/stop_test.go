package orchestrator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/process"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

func postStop(t *testing.T, handler http.Handler, name string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/__mdp/servers/stop", strings.NewReader(`{"name":"`+name+`"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestStopServerUnknown(t *testing.T) {
	_, handler := setupControlAPI(t)
	if rec := postStop(t, handler, "nope/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestStopServerMissingName(t *testing.T) {
	_, handler := setupControlAPI(t)
	req := httptest.NewRequest("POST", "/__mdp/servers/stop", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestStopServerExternal(t *testing.T) {
	o := New(&config.Config{}, "", "")
	o.mu.Lock()
	reg := registry.New()
	_ = reg.Register(&registry.ServerEntry{Name: "docker/svc", Repo: "docker", Port: 4001, PID: 0})
	o.proxies[3000] = &ProxyInstance{Port: 3000, Registry: reg, cancel: func() {}}
	o.mu.Unlock()
	handler := NewControlAPI(o, nil).Handler()

	if rec := postStop(t, handler, "docker/svc"); rec.Code != http.StatusConflict {
		t.Errorf("expected 409 for PID-less server, got %d", rec.Code)
	}
}

func TestStopServerDeterministicAcrossProxies(t *testing.T) {
	// Same name on two proxies with different PIDs: the lower proxy port wins,
	// deterministically, regardless of map iteration order.
	o := New(&config.Config{}, "", "")
	o.mu.Lock()
	reg1 := registry.New()
	_ = reg1.Register(&registry.ServerEntry{Name: "app/dev", Repo: "app", Port: 4001, PID: 1111})
	reg2 := registry.New()
	_ = reg2.Register(&registry.ServerEntry{Name: "app/dev", Repo: "app", Port: 5001, PID: 2222})
	o.proxies[3001] = &ProxyInstance{Port: 3001, Registry: reg2, cancel: func() {}}
	o.proxies[3000] = &ProxyInstance{Port: 3000, Registry: reg1, cancel: func() {}}
	o.mu.Unlock()
	handler := NewControlAPI(o, nil).Handler()

	for i := 0; i < 20; i++ {
		rec := postStop(t, handler, "app/dev")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("expected 202, got %d", rec.Code)
		}
		var body map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&body)
		if int(body["pid"].(float64)) != 1111 {
			t.Fatalf("expected the :3000 PID (1111) every time, got %v", body["pid"])
		}
	}
}

func TestStopServerSignalsProcess(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start test process: %v", err)
	}
	pid := cmd.Process.Pid
	defer func() { _ = cmd.Process.Kill() }()
	go func() { _ = cmd.Wait() }() // reap so IsProcessAlive sees the exit

	o := New(&config.Config{}, "", "")
	o.mu.Lock()
	reg := registry.New()
	_ = reg.Register(&registry.ServerEntry{Name: "app/dev", Repo: "app", Port: 4001, PID: pid})
	o.proxies[3000] = &ProxyInstance{Port: 3000, Registry: reg, cancel: func() {}}
	o.mu.Unlock()
	handler := NewControlAPI(o, nil).Handler()

	rec := postStop(t, handler, "app/dev")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if int(body["pid"].(float64)) != pid {
		t.Errorf("expected pid %d in response, got %v", pid, body["pid"])
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !process.IsProcessAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("process was not stopped")
}
