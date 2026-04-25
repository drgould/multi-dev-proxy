package orchestrator

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/routing"
)

// TestRegisterTLSEndToEnd exercises the full path that was broken before:
// register a service via the control API with cert paths → orchestrator loads
// the keypair → SmartListener serves real TLS → tls.Dial completes the
// handshake and receives the same cert.
func TestRegisterTLSEndToEnd(t *testing.T) {
	certPath, keyPath, wantCN := writeSelfSignedCert(t)

	proxyPort := freeTCPPort(t)
	o := New(&config.Config{}, "127.0.0.1")
	pi, err := o.EnsureProxy(proxyPort)
	if err != nil {
		t.Fatalf("EnsureProxy: %v", err)
	}
	t.Cleanup(func() {
		pi.cancel()
		_ = pi.Server.Close()
	})

	capi := NewControlAPI(o, nil)
	body, _ := json.Marshal(map[string]any{
		"name":        "app/main",
		"port":        freeTCPPort(t),
		"proxyPort":   proxyPort,
		"scheme":      "https",
		"tlsCertPath": certPath,
		"tlsKeyPath":  keyPath,
	})
	req := httptest.NewRequest(http.MethodPost, "/__mdp/register", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	capi.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("register: status %d, body %s", rec.Code, rec.Body.String())
	}

	addr := fmt.Sprintf("127.0.0.1:%d", proxyPort)
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "localhost",
	})
	if err != nil {
		t.Fatalf("tls.Dial: %v", err)
	}
	defer conn.Close()

	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		t.Fatal("server presented no certificate")
	}
	if got := state.PeerCertificates[0].Subject.CommonName; got != wantCN {
		t.Errorf("served cert CN = %q, want %q", got, wantCN)
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// waitPortFree polls until a TCP bind to the given port succeeds, or fails
// the test on timeout. Used to verify an auto-shut-down proxy actually
// released its listener.
func waitPortFree(t *testing.T, port int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = ln.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("port %d not released within %s", port, timeout)
}

// startOrchProxy spins up an orchestrator listening on a real ephemeral port
// and returns the orchestrator, proxy port, and a teardown helper that runs
// on test cleanup.
func startOrchProxy(t *testing.T) (*Orchestrator, int) {
	t.Helper()
	port := freeTCPPort(t)
	o := New(&config.Config{}, "127.0.0.1")
	pi, err := o.EnsureProxy(port)
	if err != nil {
		t.Fatalf("EnsureProxy: %v", err)
	}
	t.Cleanup(func() {
		pi.cancel()
		_ = pi.Server.Close()
	})
	return o, port
}

// registerViaControlAPI POSTs a register payload and fails the test if the
// orchestrator rejects it.
func registerViaControlAPI(t *testing.T, o *Orchestrator, payload map[string]any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/__mdp/register", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	NewControlAPI(o, nil).Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("register: status %d, body %s", rec.Code, rec.Body.String())
	}
}

func writeSelfSignedCert(t *testing.T) (certPath, keyPath, commonName string) {
	t.Helper()
	commonName = "localhost"
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{commonName},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	return
}

// TestPlainHTTPRegisterAndRoute spins up a real upstream, registers it via
// the control API, then issues a real HTTP request to the proxy port and
// asserts the upstream's body comes back. This is the bare-bones happy-path
// integration that previously had no Go-level coverage.
func TestPlainHTTPRegisterAndRoute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "hello from upstream")
	}))
	t.Cleanup(upstream.Close)

	upstreamPort := mustPort(t, upstream.URL)
	o, proxyPort := startOrchProxy(t)
	registerViaControlAPI(t, o, map[string]any{
		"name":      "app/main",
		"port":      upstreamPort,
		"proxyPort": proxyPort,
	})

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", proxyPort))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if got, want := string(body), "hello from upstream\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// TestDeregisterLastServerShutsDownProxy registers a single server, deregisters
// it, and asserts the proxy is removed and its port can be rebound.
func TestDeregisterLastServerShutsDownProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "live")
	}))
	t.Cleanup(upstream.Close)

	o, proxyPort := startOrchProxy(t)
	registerViaControlAPI(t, o, map[string]any{
		"name":      "app/main",
		"port":      mustPort(t, upstream.URL),
		"proxyPort": proxyPort,
	})

	// Sanity: routing works.
	if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", proxyPort)); err != nil {
		t.Fatalf("pre-deregister GET: %v", err)
	} else {
		resp.Body.Close()
	}

	// Deregister via the control API — proxy should auto-shutdown.
	delReq := httptest.NewRequest(http.MethodDelete, "/__mdp/register/app/main", nil)
	delRec := httptest.NewRecorder()
	NewControlAPI(o, nil).Handler().ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("deregister: status %d, body %s", delRec.Code, delRec.Body.String())
	}

	if pi := o.GetProxy(proxyPort); pi != nil {
		t.Errorf("proxy still registered after last deregister: %+v", pi)
	}

	// Wait for the port to be rebindable (Server.Shutdown runs async).
	waitPortFree(t, proxyPort, 2*time.Second)
}

// TestCookieRoutingBetweenSiblings registers two upstreams on the same proxy
// and asserts that the routing cookie picks the correct one for each request.
func TestCookieRoutingBetweenSiblings(t *testing.T) {
	upA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "A")
	}))
	upB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "B")
	}))
	t.Cleanup(upA.Close)
	t.Cleanup(upB.Close)

	o, proxyPort := startOrchProxy(t)
	registerViaControlAPI(t, o, map[string]any{
		"name": "app/a", "port": mustPort(t, upA.URL), "proxyPort": proxyPort,
	})
	registerViaControlAPI(t, o, map[string]any{
		"name": "app/b", "port": mustPort(t, upB.URL), "proxyPort": proxyPort,
	})

	cookieName := routing.CookieNameForPort(proxyPort)
	for _, tc := range []struct {
		name, body string
	}{
		{"app/a", "A"},
		{"app/b", "B"},
	} {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", proxyPort), nil)
		req.AddCookie(&http.Cookie{Name: cookieName, Value: url.QueryEscape(tc.name)})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != tc.body {
			t.Errorf("cookie=%s → body %q, want %q", tc.name, body, tc.body)
		}
	}
}

// TestGroupSwitchUpdatesDefaults registers two services in different groups
// on a single proxy, then issues a group switch and verifies a cookieless
// request now routes to the switched group's service.
func TestGroupSwitchUpdatesDefaults(t *testing.T) {
	upDev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "dev")
	}))
	upStg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "stg")
	}))
	t.Cleanup(upDev.Close)
	t.Cleanup(upStg.Close)

	o, proxyPort := startOrchProxy(t)
	registerViaControlAPI(t, o, map[string]any{
		"name": "app/dev", "port": mustPort(t, upDev.URL), "proxyPort": proxyPort, "group": "dev",
	})
	registerViaControlAPI(t, o, map[string]any{
		"name": "app/stg", "port": mustPort(t, upStg.URL), "proxyPort": proxyPort, "group": "stg",
	})

	// Switch the default to the stg group.
	switchReq := httptest.NewRequest(http.MethodPost, "/__mdp/groups/stg/switch", nil)
	switchRec := httptest.NewRecorder()
	NewControlAPI(o, nil).Handler().ServeHTTP(switchRec, switchReq)
	if switchRec.Code != http.StatusOK {
		t.Fatalf("group switch: status %d, body %s", switchRec.Code, switchRec.Body.String())
	}

	// Cookieless request should now hit the stg upstream via the registry default.
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", proxyPort))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "stg" {
		t.Errorf("default after switch = %q, want %q", body, "stg")
	}
}

// TestHeartbeatPrunesStaleSession registers a service tied to a clientID and
// verifies the session pruner deregisters it once heartbeats stop arriving.
func TestHeartbeatPrunesStaleSession(t *testing.T) {
	o, proxyPort := startOrchProxy(t)
	registerViaControlAPI(t, o, map[string]any{
		"name":      "app/main",
		"port":      freeTCPPort(t),
		"proxyPort": proxyPort,
		"clientID":  "client-1",
	})
	pi := o.GetProxy(proxyPort)
	if pi.Registry.Get("app/main") == nil {
		t.Fatal("precondition: service should be registered")
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	o.StartSessionPruner(ctx, 5*time.Millisecond, 1*time.Millisecond)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if pi.Registry.Get("app/main") == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("expected pruner to deregister stale-session service within 1s")
}

// TestMultiPortServiceRegistersAll exercises startMultiPortService end-to-end:
// a single mdp.yaml service declares two `ports:` entries, each pointing to a
// different proxy. After StartConfigServices, both registrations should appear
// on their respective proxy registries.
func TestMultiPortServiceRegistersAll(t *testing.T) {
	swapTimeouts(t, 100*time.Millisecond, 5*time.Millisecond)
	swapTCPCheck(t, func(int) bool { return true })

	proxyA := freeTCPPort(t)
	proxyB := freeTCPPort(t)

	cfg := &config.Config{
		PortRange: "30000-31000",
		Services: map[string]config.ServiceConfig{
			"myapp": {
				Command: "true", // exits immediately; registration happens after cmd.Start
				Env: map[string]config.EnvValue{
					"API_PORT": {Value: "auto"},
					"WS_PORT":  {Value: "auto"},
				},
				Ports: []config.PortMapping{
					{Env: "API_PORT", Proxy: proxyA, Name: "api"},
					{Env: "WS_PORT", Proxy: proxyB, Name: "ws"},
				},
			},
		},
	}
	o := New(cfg, "127.0.0.1")
	t.Cleanup(func() { o.Shutdown(context.Background()) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := o.StartConfigServices(ctx, "test"); err != nil {
		t.Fatalf("StartConfigServices: %v", err)
	}

	piA := o.GetProxy(proxyA)
	if piA == nil || piA.Registry.Get("test/api") == nil {
		t.Errorf("expected test/api registered on proxy %d", proxyA)
	}
	piB := o.GetProxy(proxyB)
	if piB == nil || piB.Registry.Get("test/ws") == nil {
		t.Errorf("expected test/ws registered on proxy %d", proxyB)
	}
}

func mustPort(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	_, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host %q: %v", u.Host, err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return port
}
