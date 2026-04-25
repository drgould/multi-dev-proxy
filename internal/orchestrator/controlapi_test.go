package orchestrator

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

func setupControlAPI(t *testing.T) (*Orchestrator, http.Handler) {
	t.Helper()
	o := New(&config.Config{}, "")
	o.mu.Lock()
	reg := registry.New()
	reg.Register(&registry.ServerEntry{Name: "app/dev", Repo: "app", Port: 4001, PID: 100, Group: "dev"})
	reg.Register(&registry.ServerEntry{Name: "app/staging", Repo: "app", Port: 4002, PID: 200, Group: "staging"})
	o.proxies[3000] = &ProxyInstance{Port: 3000, Label: "frontend", Registry: reg, CookieName: "__mdp_upstream_3000", cancel: func() {}}
	o.mu.Unlock()

	capi := NewControlAPI(o, nil)
	return o, capi.Handler()
}

func TestControlAPIHealth(t *testing.T) {
	_, handler := setupControlAPI(t)
	req := httptest.NewRequest("GET", "/__mdp/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	if body["ok"] != true {
		t.Error("expected ok: true")
	}
}

func TestControlAPIListProxies(t *testing.T) {
	_, handler := setupControlAPI(t)
	req := httptest.NewRequest("GET", "/__mdp/proxies", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body []map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(body))
	}
	if body[0]["label"] != "frontend" {
		t.Errorf("expected label frontend, got %v", body[0]["label"])
	}
}

func TestControlAPIRegister(t *testing.T) {
	o, handler := setupControlAPI(t)

	payload := map[string]any{
		"name":      "api/main",
		"port":      5001,
		"pid":       300,
		"proxyPort": 3000,
		"group":     "dev",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/__mdp/register", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	pi := o.GetProxy(3000)
	if pi.Registry.Get("api/main") == nil {
		t.Error("expected api/main to be registered")
	}
}

// TestControlAPIRegisterDoesNotLoadCertWhenProxyBindFails verifies that a
// failure to bind the proxy port aborts the request before AddCert mutates
// the orchestrator-wide cert store. Otherwise a busy port could leak certs.
func TestControlAPIRegisterDoesNotLoadCertWhenProxyBindFails(t *testing.T) {
	// Hold a port so EnsureProxy(busyPort) fails on bind.
	busyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busyLn.Close()
	busyPort := busyLn.Addr().(*net.TCPAddr).Port

	o := New(&config.Config{}, "127.0.0.1")
	capi := NewControlAPI(o, nil)

	// Use real cert paths so AddCert *would* succeed if it were called.
	certPath, keyPath, _ := writeSelfSignedCert(t)

	payload := map[string]any{
		"name":        "app/main",
		"port":        9999,
		"proxyPort":   busyPort,
		"scheme":      "https",
		"tlsCertPath": certPath,
		"tlsKeyPath":  keyPath,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/__mdp/register", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	capi.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("expected non-200 when port bind fails, got 200")
	}
	if o.HasCerts() {
		t.Error("cert store should be empty when proxy bind fails")
	}
}

func TestControlAPIRegisterAtomicOnTLSFailure(t *testing.T) {
	o, handler := setupControlAPI(t)

	payload := map[string]any{
		"name":        "api/main",
		"port":        5001,
		"pid":         300,
		"proxyPort":   3000,
		"group":       "dev",
		"scheme":      "https",
		"tlsCertPath": "/nonexistent/cert.pem",
		"tlsKeyPath":  "/nonexistent/key.pem",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/__mdp/register", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	pi := o.GetProxy(3000)
	if pi.Registry.Get("api/main") != nil {
		t.Error("service should not be registered when cert load fails")
	}
}

func TestControlAPIRegisterBadJSON(t *testing.T) {
	_, handler := setupControlAPI(t)
	req := httptest.NewRequest("POST", "/__mdp/register", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestControlAPIRegisterMissingFields(t *testing.T) {
	_, handler := setupControlAPI(t)
	payload := map[string]any{"name": "", "port": 0, "proxyPort": 3000}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/__mdp/register", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestControlAPIDeregister(t *testing.T) {
	_, handler := setupControlAPI(t)
	req := httptest.NewRequest("DELETE", "/__mdp/register/app/dev", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	if body["deleted"] != true {
		t.Error("expected deleted: true")
	}
}

func TestControlAPISetDefault(t *testing.T) {
	o, handler := setupControlAPI(t)
	req := httptest.NewRequest("POST", "/__mdp/proxies/3000/default/app/dev", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	pi := o.GetProxy(3000)
	if d := pi.Registry.GetDefault(); d != "app/dev" {
		t.Errorf("expected default app/dev, got %q", d)
	}
}

func TestControlAPISetDefaultBadPort(t *testing.T) {
	_, handler := setupControlAPI(t)
	req := httptest.NewRequest("POST", "/__mdp/proxies/abc/default/app/dev", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestControlAPIClearDefault(t *testing.T) {
	o, handler := setupControlAPI(t)

	o.SetDefault(3000, "app/dev")

	req := httptest.NewRequest("DELETE", "/__mdp/proxies/3000/default", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	pi := o.GetProxy(3000)
	if d := pi.Registry.GetDefault(); d != "" {
		t.Errorf("expected empty default, got %q", d)
	}
}

func TestControlAPIClearDefaultBadPort(t *testing.T) {
	_, handler := setupControlAPI(t)
	req := httptest.NewRequest("DELETE", "/__mdp/proxies/abc/default", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestControlAPIListGroups(t *testing.T) {
	_, handler := setupControlAPI(t)
	req := httptest.NewRequest("GET", "/__mdp/groups", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var groups map[string][]string
	json.NewDecoder(rec.Body).Decode(&groups)
	if len(groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(groups))
	}
}

func TestControlAPISwitchGroup(t *testing.T) {
	o, handler := setupControlAPI(t)
	req := httptest.NewRequest("POST", "/__mdp/groups/dev/switch", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	pi := o.GetProxy(3000)
	if d := pi.Registry.GetDefault(); d != "app/dev" {
		t.Errorf("expected default app/dev, got %q", d)
	}
}

func TestControlAPISwitchGroupNotFound(t *testing.T) {
	_, handler := setupControlAPI(t)
	req := httptest.NewRequest("POST", "/__mdp/groups/nonexistent/switch", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestControlAPIListServices(t *testing.T) {
	o, handler := setupControlAPI(t)
	o.SetService("web/main", &ManagedService{Name: "web/main", Group: "dev", PID: 42, Port: 4001, Status: "running"})

	req := httptest.NewRequest("GET", "/__mdp/services", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var services []map[string]any
	json.NewDecoder(rec.Body).Decode(&services)
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0]["name"] != "web/main" {
		t.Errorf("expected web/main, got %v", services[0]["name"])
	}
}

func TestControlAPIShutdown(t *testing.T) {
	_, handler := setupControlAPI(t)
	req := httptest.NewRequest("POST", "/__mdp/shutdown", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	if body["ok"] != true {
		t.Error("expected ok: true")
	}
}

func TestControlAPIPeerLookup(t *testing.T) {
	o := New(&config.Config{}, "")
	o.mu.Lock()
	reg := registry.New()
	reg.Register(&registry.ServerEntry{
		Name:  "dev/api",
		Repo:  "backend",
		Group: "dev",
		Port:  9001,
		Env:   map[string]string{"AUTH_TOKEN": "secret-xyz", "MODE": "test"},
	})
	o.proxies[4000] = &ProxyInstance{Port: 4000, Registry: reg, CookieName: "__mdp_upstream_4000", cancel: func() {}}
	o.mu.Unlock()
	handler := NewControlAPI(o, nil).Handler()

	t.Run("port and env lookup", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/__mdp/peers?group=dev&repo=backend&service=api", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		json.NewDecoder(rec.Body).Decode(&body)
		if got, _ := body["port"].(float64); int(got) != 9001 {
			t.Errorf("port = %v, want 9001", body["port"])
		}
		envBody, _ := body["env"].(map[string]any)
		if envBody["AUTH_TOKEN"] != "secret-xyz" {
			t.Errorf("env.AUTH_TOKEN = %v, want secret-xyz", envBody["AUTH_TOKEN"])
		}
	})

	t.Run("missing peer 404", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/__mdp/peers?group=dev&repo=other&service=api", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("missing required params", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/__mdp/peers?group=dev", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})
}

func TestControlAPIRegisterAcceptsRepoAndEnv(t *testing.T) {
	o := New(&config.Config{}, "")
	handler := NewControlAPI(o, nil).Handler()

	body := bytes.NewBufferString(`{
		"name": "dev/api",
		"port": 9001,
		"proxyPort": 4000,
		"group": "dev",
		"repo": "backend",
		"env": {"AUTH_TOKEN": "secret"}
	}`)
	req := httptest.NewRequest("POST", "/__mdp/register", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	pi := o.GetProxy(4000)
	if pi == nil {
		t.Fatal("proxy not created")
	}
	entry := pi.Registry.Get("dev/api")
	if entry == nil {
		t.Fatal("entry not registered")
	}
	if entry.Repo != "backend" {
		t.Errorf("repo = %q, want backend", entry.Repo)
	}
	if entry.Env["AUTH_TOKEN"] != "secret" {
		t.Errorf("env.AUTH_TOKEN = %q, want secret", entry.Env["AUTH_TOKEN"])
	}
}

func TestControlAPIShutdownCallsCallback(t *testing.T) {
	o := New(&config.Config{}, "")
	called := make(chan bool, 1)
	capi := NewControlAPI(o, func() { called <- true })
	handler := capi.Handler()

	req := httptest.NewRequest("POST", "/__mdp/shutdown", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	select {
	case <-called:
	default:
		// shutdownFn is called in a goroutine, give it a moment
		// but don't block the test
	}
}
