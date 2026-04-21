package proxy

import (
	"crypto/tls"
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
	p := NewProxy(reg, 3000, "")
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

func TestProxyLastPathTracking(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	p, reg := newTestProxy(t)
	registerUpstream(t, reg, upstream, "app/main", "app")

	// Browser navigation (Accept: text/html) should be tracked.
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if got := p.GetLastPath("app/main"); got != "/" {
		t.Errorf("last path = %q, want /", got)
	}

	// Navigate to another page with query string.
	req = httptest.NewRequest("GET", "/dashboard?tab=settings", nil)
	req.Header.Set("Accept", "text/html")
	rr = httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if got := p.GetLastPath("app/main"); got != "/dashboard?tab=settings" {
		t.Errorf("last path = %q, want /dashboard?tab=settings", got)
	}
}

func TestProxyLastPathSkipsNonNavigation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	p, reg := newTestProxy(t)
	registerUpstream(t, reg, upstream, "app/main", "app")

	// Navigate to a page first.
	req := httptest.NewRequest("GET", "/some-page", nil)
	req.Header.Set("Accept", "text/html")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if got := p.GetLastPath("app/main"); got != "/some-page" {
		t.Errorf("last path = %q, want /some-page", got)
	}

	// Asset request (no text/html Accept) should NOT overwrite.
	req = httptest.NewRequest("GET", "/assets/main.js", nil)
	req.Header.Set("Accept", "application/javascript")
	rr = httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if got := p.GetLastPath("app/main"); got != "/some-page" {
		t.Errorf("last path should still be /some-page after asset request, got %q", got)
	}

	// API/XHR request (Accept: application/json) should NOT overwrite.
	req = httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Accept", "application/json")
	rr = httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if got := p.GetLastPath("app/main"); got != "/some-page" {
		t.Errorf("last path should still be /some-page after API request, got %q", got)
	}

	// POST request should NOT overwrite (even with text/html).
	req = httptest.NewRequest("POST", "/form-submit", nil)
	req.Header.Set("Accept", "text/html")
	rr = httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if got := p.GetLastPath("app/main"); got != "/some-page" {
		t.Errorf("last path should still be /some-page after POST, got %q", got)
	}
}

func TestProxyGetLastPathEmpty(t *testing.T) {
	p, _ := newTestProxy(t)

	if got := p.GetLastPath("nonexistent"); got != "" {
		t.Errorf("last path for unknown service = %q, want empty", got)
	}
}

func TestProxyRedirectsHTTPToHTTPSWhenUpstreamIsHTTPS(t *testing.T) {
	p, reg := newTestProxy(t)
	if err := reg.Register(&registry.ServerEntry{
		Name:   "app/main",
		Repo:   "app",
		Port:   19998,
		PID:    1,
		Scheme: "https",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "http://localhost:3000/some/path?q=1", nil)
	req.Host = "localhost:3000"
	req.TLS = nil
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc != "https://localhost:3000/some/path?q=1" {
		t.Errorf("expected redirect to https with path+query, got %q", loc)
	}
}

func TestProxySchemeRedirectPreservesPOST(t *testing.T) {
	p, reg := newTestProxy(t)
	if err := reg.Register(&registry.ServerEntry{
		Name:   "app/main",
		Repo:   "app",
		Port:   19998,
		PID:    1,
		Scheme: "https",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "http://localhost:3000/submit", nil)
	req.Host = "localhost:3000"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	// 307 preserves method and body; 302 would let clients downgrade POST to GET.
	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 so POST is preserved, got %d", rr.Code)
	}
}

func TestProxyRedirectsHTTPSToHTTPWhenUpstreamIsHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	p, reg := newTestProxy(t)
	registerUpstream(t, reg, upstream, "app/feature", "app")

	req := httptest.NewRequest("GET", "https://localhost:3000/foo", nil)
	req.Host = "localhost:3000"
	req.TLS = &tls.ConnectionState{}
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "http://localhost:3000/foo" {
		t.Errorf("expected redirect to http, got %q", loc)
	}
}

func TestProxyDoesNotRedirectMdpPaths(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	p, reg := newTestProxy(t)
	if err := reg.Register(&registry.ServerEntry{
		Name:   "app/main",
		Repo:   "app",
		Port:   serverPort(upstream),
		PID:    1,
		Scheme: "https",
	}); err != nil {
		t.Fatal(err)
	}

	// Plain HTTP request to /__mdp/* with an HTTPS upstream should NOT be
	// redirected — those paths must stay accessible on both schemes so that
	// group switching works across scheme changes.
	req := httptest.NewRequest("GET", "http://localhost:3000/__mdp/health", nil)
	req.Host = "localhost:3000"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code == http.StatusTemporaryRedirect {
		t.Fatalf("/__mdp/* path was unexpectedly redirected (got %d, Location=%q)", rr.Code, rr.Header().Get("Location"))
	}
}

func TestProxyNoRedirectWhenSchemeMatches(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	p, reg := newTestProxy(t)
	// http upstream, http request — should not redirect.
	registerUpstream(t, reg, upstream, "app/main", "app")

	req := httptest.NewRequest("GET", "http://localhost:3000/", nil)
	req.Host = "localhost:3000"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code == http.StatusTemporaryRedirect {
		t.Fatalf("unexpected redirect (Location=%q)", rr.Header().Get("Location"))
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
