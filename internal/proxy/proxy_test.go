package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

func serverPort(s *httptest.Server) int {
	return s.Listener.Addr().(*net.TCPAddr).Port
}

func newTestProxy(t *testing.T) (*Proxy, *registry.Registry) {
	t.Helper()
	reg := registry.New()
	p := NewProxy(reg, 3000, false)
	return p, reg
}

func registerUpstream(t *testing.T, reg *registry.Registry, s *httptest.Server, name, repo string) {
	t.Helper()
	if err := reg.Register(&registry.ServerEntry{
		Name: name,
		Repo: repo,
		Port: serverPort(s),
		PID:  1,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
}

func TestProxySingleServerNoCookie(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Got-Forwarded-Host", r.Header.Get("X-Forwarded-Host"))
		w.WriteHeader(200)
		w.Write([]byte("hello from upstream"))
	}))
	defer upstream.Close()

	p, reg := newTestProxy(t)
	registerUpstream(t, reg, upstream, "app/main", "app")

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if string(body) != "hello from upstream" {
		t.Errorf("unexpected body: %q", body)
	}
	if rr.Header().Get("X-Got-Forwarded-Host") != "localhost:3000" {
		t.Errorf("X-Forwarded-Host not set correctly, got: %q", rr.Header().Get("X-Got-Forwarded-Host"))
	}
}

func TestProxyRedirectsMultipleServersNoCookie(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer s1.Close()
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer s2.Close()

	p, reg := newTestProxy(t)
	registerUpstream(t, reg, s1, "app/main", "app")
	registerUpstream(t, reg, s2, "app/feature", "app")

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != switchPagePath {
		t.Errorf("expected redirect to %s, got %q", switchPagePath, loc)
	}
}

func TestProxyRoutesByValidCookie(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("server1"))
	}))
	defer s1.Close()
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("server2"))
	}))
	defer s2.Close()

	p, reg := newTestProxy(t)
	registerUpstream(t, reg, s1, "app/main", "app")
	registerUpstream(t, reg, s2, "app/feature", "app")

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Cookie", "__mdp_upstream=app%2Ffeature")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if string(body) != "server2" {
		t.Errorf("expected server2 response, got %q", body)
	}
}

func TestProxyUpstreamDown(t *testing.T) {
	p, reg := newTestProxy(t)
	// Register a server on a port with nothing listening
	if err := reg.Register(&registry.ServerEntry{
		Name: "app/dead",
		Repo: "app",
		Port: 19999, // nothing listening here
		PID:  1,
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rr.Code)
	}
}

func TestProxyLocationRewrite(t *testing.T) {
	var upstreamPort int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://127.0.0.1:"+itoa(upstreamPort)+"/foo")
		w.WriteHeader(http.StatusFound)
	}))
	defer upstream.Close()
	upstreamPort = serverPort(upstream)

	p, reg := newTestProxy(t)
	registerUpstream(t, reg, upstream, "app/main", "app")

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	loc := rr.Header().Get("Location")
	expected := "http://localhost:3000/foo"
	if loc != expected {
		t.Errorf("Location rewrite: got %q, want %q", loc, expected)
	}
}

func itoa(n int) string {
	return http.StatusText(0)[:0] + func() string {
		b := make([]byte, 0, 10)
		if n == 0 {
			return "0"
		}
		for n > 0 {
			b = append([]byte{byte('0' + n%10)}, b...)
			n /= 10
		}
		return string(b)
	}()
}
