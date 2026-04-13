package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	"github.com/derekgould/multi-dev-proxy/internal/registry"
	"github.com/derekgould/multi-dev-proxy/internal/routing"
)

const switchPagePath = "/__mdp/switch"

// Proxy routes incoming requests to registered dev servers.
type Proxy struct {
	reg        *registry.Registry
	listenPort int
	cookieName string
	rp         *httputil.ReverseProxy

	lastPathMu sync.RWMutex
	lastPaths  map[string]string // service name → last URL path+query
}

// NewProxy creates a new Proxy.
func NewProxy(reg *registry.Registry, listenPort int, cookieName string) *Proxy {
	if cookieName == "" {
		cookieName = routing.DefaultCookieName
	}
	p := &Proxy{
		reg:        reg,
		listenPort: listenPort,
		cookieName: cookieName,
		lastPaths:  make(map[string]string),
	}
	p.rp = &httputil.ReverseProxy{
		Rewrite:       p.rewrite,
		FlushInterval: -1,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp.Request != nil {
				p.rewriteLocationByHost(resp, resp.Request)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("proxy error", "url", r.URL.String(), "err", err)
			http.Error(w, fmt.Sprintf("upstream unreachable: %v", err), http.StatusBadGateway)
		},
	}
	return p
}

// GetLastPath returns the last visited path for the given service name.
func (p *Proxy) GetLastPath(name string) string {
	p.lastPathMu.RLock()
	defer p.lastPathMu.RUnlock()
	return p.lastPaths[name]
}

func (p *Proxy) setLastPath(name, path string) {
	p.lastPathMu.Lock()
	defer p.lastPathMu.Unlock()
	p.lastPaths[name] = path
}

// CookieName returns the cookie name this proxy uses for routing.
func (p *Proxy) CookieName() string {
	return p.cookieName
}

// SetModifyResponse wires an external ModifyResponse hook (e.g. HTML injection).
// It chains after the built-in Location header rewriting.
func (p *Proxy) SetModifyResponse(fn func(*http.Response) error) {
	p.rp.ModifyResponse = func(resp *http.Response) error {
		if resp.Request != nil {
			p.rewriteLocationByHost(resp, resp.Request)
		}
		if fn != nil {
			return fn(resp)
		}
		return nil
	}
}

func (p *Proxy) resolve(cookieHeader, queryUpstream string) routing.ResolveResult {
	return routing.ResolveUpstream(p.reg, cookieHeader, p.cookieName, p.reg.GetDefault(), queryUpstream)
}

type contextKey struct{}

// rewrite is the Rewrite function for httputil.ReverseProxy.
// MUST use Rewrite — Director is deprecated since Go 1.20.
func (p *Proxy) rewrite(r *httputil.ProxyRequest) {
	result, ok := r.In.Context().Value(contextKey{}).(routing.ResolveResult)
	if !ok || result.Entry == nil {
		return
	}

	scheme := result.Entry.Scheme
	if scheme == "" {
		scheme = "http"
	}
	target := &url.URL{
		Scheme: scheme,
		Host:   fmt.Sprintf("localhost:%d", result.Entry.Port),
	}
	r.SetURL(target)

	proto := "http"
	if r.In.TLS != nil {
		proto = "https"
	}
	r.Out.Header.Set("X-Forwarded-Host", fmt.Sprintf("localhost:%d", p.listenPort))
	r.Out.Header.Set("X-Forwarded-Proto", proto)
	r.Out.Header.Set("X-Forwarded-Port", fmt.Sprintf("%d", p.listenPort))

	if IsWebSocketUpgrade(r.In) {
		FixWebSocketHeaders(r.Out.Header)
	}
}

// ServeHTTP implements http.Handler.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cookieHeader := r.Header.Get("Cookie")
	queryUpstream := r.URL.Query().Get(routing.QueryParamName)
	result := p.resolve(cookieHeader, queryUpstream)

	if result.Redirect || result.Entry == nil {
		http.Redirect(w, r, switchPagePath, http.StatusFound)
		return
	}

	// Track last visited page for this service. Only track browser navigation
	// requests (Accept: text/html), skip assets, API calls, and /__mdp/ paths.
	if isNavigationRequest(r) {
		pathQuery := r.URL.Path
		if r.URL.RawQuery != "" {
			pathQuery += "?" + r.URL.RawQuery
		}
		p.setLastPath(result.Entry.Name, pathQuery)
	}

	ctx := context.WithValue(r.Context(), contextKey{}, result)
	p.rp.ServeHTTP(w, r.WithContext(ctx))
}

// isNavigationRequest returns true if the request looks like a browser
// page navigation (as opposed to an asset, XHR, or API request).
func isNavigationRequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/__mdp/") {
		return false
	}
	// Browser navigations send Accept: text/html.
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

// rewriteLocationByHost rewrites upstream Location headers to point to the proxy.
func (p *Proxy) rewriteLocationByHost(resp *http.Response, req *http.Request) {
	loc := resp.Header.Get("Location")
	if loc == "" {
		return
	}
	proto := "http"
	if req.TLS != nil {
		proto = "https"
	}
	host := req.URL.Host
	proxyAddr := fmt.Sprintf("localhost:%d", p.listenPort)
	loc = strings.ReplaceAll(loc, "http://"+host, proto+"://"+proxyAddr)
	loc = strings.ReplaceAll(loc, "https://"+host, proto+"://"+proxyAddr)
	_, portStr, _ := net.SplitHostPort(host)
	if portStr != "" {
		for _, alt := range []string{"127.0.0.1:" + portStr, "[::1]:" + portStr} {
			loc = strings.ReplaceAll(loc, "http://"+alt, proto+"://"+proxyAddr)
			loc = strings.ReplaceAll(loc, "https://"+alt, proto+"://"+proxyAddr)
		}
	}
	resp.Header.Set("Location", loc)
}
