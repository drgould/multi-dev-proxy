package proxy

import (
	"fmt"
	"log/slog"
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
	p.rp = &httputil.ReverseProxy{
		Rewrite:       p.rewrite,
		FlushInterval: -1, // required for SSE / streaming
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("proxy error", "url", r.URL.String(), "err", err)
			http.Error(w, fmt.Sprintf("upstream unreachable: %v", err), http.StatusBadGateway)
		},
	}
	return p
}

// SetModifyResponse wires an external ModifyResponse hook (e.g. HTML injection).
func (p *Proxy) SetModifyResponse(fn func(*http.Response) error) {
	p.rp.ModifyResponse = fn
}

// rewrite is the Rewrite function for httputil.ReverseProxy.
// MUST use Rewrite — Director is deprecated since Go 1.20.
func (p *Proxy) rewrite(r *httputil.ProxyRequest) {
	cookieHeader := r.In.Header.Get("Cookie")
	result := routing.ResolveUpstream(p.reg, cookieHeader)
	if result.Entry == nil {
		return
	}

	// Always connect via 127.0.0.1 (not localhost — may resolve to IPv6)
	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", result.Entry.Port),
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

	// Wrap ModifyResponse to also rewrite Location headers
	upstreamPort := result.Entry.Port
	origModify := p.rp.ModifyResponse
	p.rp.ModifyResponse = func(resp *http.Response) error {
		p.rewriteLocation(resp, upstreamPort)
		if origModify != nil {
			return origModify(resp)
		}
		return nil
	}
	p.rp.ServeHTTP(w, r)
	p.rp.ModifyResponse = origModify
}

// rewriteLocation rewrites upstream Location headers to point to the proxy.
func (p *Proxy) rewriteLocation(resp *http.Response, upstreamPort int) {
	loc := resp.Header.Get("Location")
	if loc == "" {
		return
	}
	upstream := fmt.Sprintf("127.0.0.1:%d", upstreamPort)
	proto := "http"
	if p.listenTLS {
		proto = "https"
	}
	proxyAddr := fmt.Sprintf("localhost:%d", p.listenPort)
	loc = strings.ReplaceAll(loc, "http://"+upstream, proto+"://"+proxyAddr)
	loc = strings.ReplaceAll(loc, "https://"+upstream, proto+"://"+proxyAddr)
	resp.Header.Set("Location", loc)
}
