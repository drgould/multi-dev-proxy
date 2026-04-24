package orchestrator

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestShutdownProxyIdempotent(t *testing.T) {
	o, proxyPort := startOrchProxy(t)
	if o.GetProxy(proxyPort) == nil {
		t.Fatal("precondition: proxy should exist")
	}

	o.ShutdownProxy(proxyPort)
	// Second call must be a no-op (and must not panic).
	o.ShutdownProxy(proxyPort)

	if pi := o.GetProxy(proxyPort); pi != nil {
		t.Errorf("proxy still present after shutdown: %+v", pi)
	}
	waitPortFree(t, proxyPort, 2*time.Second)
}

func TestProxyStaysUpWithOtherServers(t *testing.T) {
	upA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	upB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(upA.Close)
	t.Cleanup(upB.Close)

	o, proxyPort := startOrchProxy(t)
	registerViaControlAPI(t, o, map[string]any{
		"name": "app/a", "port": mustPort(t, upA.URL), "proxyPort": proxyPort,
	})
	registerViaControlAPI(t, o, map[string]any{
		"name": "app/b", "port": mustPort(t, upB.URL), "proxyPort": proxyPort,
	})

	// Deregister only one — proxy should remain.
	req := httptest.NewRequest(http.MethodDelete, "/__mdp/register/app/a", nil)
	rec := httptest.NewRecorder()
	NewControlAPI(o, nil).Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deregister: status %d", rec.Code)
	}

	pi := o.GetProxy(proxyPort)
	if pi == nil {
		t.Fatal("proxy shut down despite remaining server")
	}
	if got := pi.Registry.Count(); got != 1 {
		t.Errorf("registry count = %d, want 1", got)
	}
}

func TestShutdownProxyOnDisconnect(t *testing.T) {
	o, proxyPort := startOrchProxy(t)
	registerViaControlAPI(t, o, map[string]any{
		"name":      "app/main",
		"port":      freeTCPPort(t),
		"proxyPort": proxyPort,
		"clientID":  "client-1",
	})
	if o.GetProxy(proxyPort) == nil {
		t.Fatal("precondition: proxy should exist")
	}

	if removed := o.Disconnect("client-1"); removed != 1 {
		t.Fatalf("Disconnect removed = %d, want 1", removed)
	}

	if pi := o.GetProxy(proxyPort); pi != nil {
		t.Errorf("proxy still present after client disconnect: %+v", pi)
	}
	waitPortFree(t, proxyPort, 2*time.Second)
}

// TestShutdownProxyViaPerProxyAPI covers the pathway used by
// internal/process/manager.go — DELETE to /__mdp/register on the proxy's own
// port (not the control API).
func TestShutdownProxyViaPerProxyAPI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(upstream.Close)

	o, proxyPort := startOrchProxy(t)
	registerViaControlAPI(t, o, map[string]any{
		"name":      "app/main",
		"port":      mustPort(t, upstream.URL),
		"proxyPort": proxyPort,
	})

	req, err := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("http://127.0.0.1:%d/__mdp/register/app/main", proxyPort), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE via per-proxy API: %v", err)
	}
	resp.Body.Close()

	// GetProxy isn't guaranteed to be nil synchronously because the per-proxy
	// DeregisterHandler callback returns before the map removal completes in
	// the async shutdown goroutine. Poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if o.GetProxy(proxyPort) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pi := o.GetProxy(proxyPort); pi != nil {
		t.Errorf("proxy still present after per-proxy deregister: %+v", pi)
	}
	waitPortFree(t, proxyPort, 2*time.Second)
}

// TestEnsureProxyAfterShutdownReusesPort verifies that EnsureProxy on the
// same port succeeds immediately after ShutdownProxy returns — without any
// wait. ShutdownProxy must release the listener synchronously so the port
// is rebindable before the next net.Listen call.
func TestEnsureProxyAfterShutdownReusesPort(t *testing.T) {
	o, proxyPort := startOrchProxy(t)
	o.ShutdownProxy(proxyPort)

	// Immediately re-bind the same port — no polling, no sleep.
	pi, err := o.EnsureProxy(proxyPort)
	if err != nil {
		t.Fatalf("EnsureProxy on just-shutdown port: %v", err)
	}
	t.Cleanup(func() {
		pi.cancel()
		_ = pi.Server.Close()
	})
	if got := o.GetProxy(proxyPort); got != pi {
		t.Error("expected fresh proxy registered under same port")
	}
}
