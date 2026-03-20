package proxy

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/derekgould/multi-dev-proxy/internal/registry"
	"github.com/derekgould/multi-dev-proxy/internal/routing"
)

const switchPagePath = "/__mdp/switch"

// Proxy routes incoming requests to registered dev servers.
type Proxy struct {
	reg        *registry.Registry
	listenPort int
	listenTLS  bool
	rp         *httputil.ReverseProxy
}

// NewProxy creates a new Proxy.
func NewProxy(reg *registry.Registry, listenPort int, listenTLS bool) *Proxy {
	p := &Proxy{
		reg:        reg,
		listenPort: listenPort,
		listenTLS:  listenTLS,
	}
	// Skip TLS verification for upstream connections (localhost self-signed certs)
	p.rp = &httputil.ReverseProxy{
		Rewrite:       p.rewrite,
		FlushInterval: -1, // required for SSE / streaming
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp.Request != nil {
				p.rewriteLocationByHost(resp, resp.Request.URL.Host)
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

// SetModifyResponse wires an external ModifyResponse hook (e.g. HTML injection).
// It chains after the built-in Location header rewriting.
func (p *Proxy) SetModifyResponse(fn func(*http.Response) error) {
	p.rp.ModifyResponse = func(resp *http.Response) error {
		if resp.Request != nil {
			p.rewriteLocationByHost(resp, resp.Request.URL.Host)
		}
		if fn != nil {
			return fn(resp)
		}
		return nil
	}
}

// rewrite is the Rewrite function for httputil.ReverseProxy.
// MUST use Rewrite — Director is deprecated since Go 1.20.
func (p *Proxy) rewrite(r *httputil.ProxyRequest) {
	cookieHeader := r.In.Header.Get("Cookie")
	result := routing.ResolveUpstream(p.reg, cookieHeader)
	if result.Entry == nil {
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
	if p.listenTLS {
		proto = "https"
	}
	r.Out.Header.Set("X-Forwarded-Host", fmt.Sprintf("localhost:%d", p.listenPort))
	r.Out.Header.Set("X-Forwarded-Proto", proto)
	r.Out.Header.Set("X-Forwarded-Port", fmt.Sprintf("%d", p.listenPort))

	// Fix WebSocket header casing (Go canonicalises Sec-WebSocket-Key incorrectly)
	if IsWebSocketUpgrade(r.In) {
		FixWebSocketHeaders(r.Out.Header)
	}
}

// ServeHTTP implements http.Handler.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cookieHeader := r.Header.Get("Cookie")
	result := routing.ResolveUpstream(p.reg, cookieHeader)

	if result.Redirect || result.Entry == nil {
		http.Redirect(w, r, switchPagePath, http.StatusFound)
		return
	}

	p.rp.ServeHTTP(w, r)
}

// rewriteLocationByHost rewrites upstream Location headers to point to the proxy.
// host is the upstream host:port (e.g. "127.0.0.1:4001").
func (p *Proxy) rewriteLocationByHost(resp *http.Response, host string) {
	loc := resp.Header.Get("Location")
	if loc == "" {
		return
	}
	proto := "http"
	if p.listenTLS {
		proto = "https"
	}
	proxyAddr := fmt.Sprintf("localhost:%d", p.listenPort)
	loc = strings.ReplaceAll(loc, "http://"+host, proto+"://"+proxyAddr)
	loc = strings.ReplaceAll(loc, "https://"+host, proto+"://"+proxyAddr)
	// Also rewrite 127.0.0.1 and [::1] variants of the same port
	_, portStr, _ := net.SplitHostPort(host)
	if portStr != "" {
		for _, alt := range []string{"127.0.0.1:" + portStr, "[::1]:" + portStr} {
			loc = strings.ReplaceAll(loc, "http://"+alt, proto+"://"+proxyAddr)
			loc = strings.ReplaceAll(loc, "https://"+alt, proto+"://"+proxyAddr)
		}
	}
	resp.Header.Set("Location", loc)
}
