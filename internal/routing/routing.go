package routing

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

// DefaultCookieName is the default cookie used to track the active upstream server.
const DefaultCookieName = "__mdp_upstream"

// CookieNameForPort returns a port-scoped cookie name to avoid collisions
// when multiple proxies run on the same host.
func CookieNameForPort(port int) string {
	return fmt.Sprintf("__mdp_upstream_%d", port)
}

// ParseCookies parses a Cookie header string into a name→value map.
// Values are URL-decoded.
func ParseCookies(header string) map[string]string {
	cookies := make(map[string]string)
	if header == "" {
		return cookies
	}
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		idx := strings.IndexByte(part, '=')
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(part[:idx])
		value := strings.TrimSpace(part[idx+1:])
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
		if name != "" {
			cookies[name] = value
		}
	}
	return cookies
}

// ResolveResult is the result of upstream resolution.
type ResolveResult struct {
	Entry    *registry.ServerEntry // nil if no upstream
	Redirect bool                  // true if should redirect to /__mdp/switch
}

// QueryParamName is the query parameter that overrides cookie-based routing.
// This enables per-iframe and per-tab routing without shared cookie conflicts.
const QueryParamName = "__mdp_upstream"

// ResolveUpstream picks the upstream server based on registry state, query param, cookie, and default.
//   - 0 servers → {nil, false} (show empty switch page inline)
//   - 1 server  → {that server, false} (auto-route, no cookie needed)
//   - query param match → {matched server, false} (highest priority)
//   - N servers + valid cookie → {matched server, false}
//   - N servers + valid default → {default server, false}
//   - N servers + no cookie/default → {nil, true} (redirect to switch page)
func ResolveUpstream(reg *registry.Registry, cookieHeader, cookieName, defaultServer, queryUpstream string) ResolveResult {
	count := reg.Count()
	if count == 0 {
		return ResolveResult{}
	}
	entries := reg.List()
	if count == 1 {
		return ResolveResult{Entry: entries[0]}
	}

	// Query param takes highest priority — enables per-iframe routing.
	if queryUpstream != "" {
		if entry := reg.Get(queryUpstream); entry != nil {
			return ResolveResult{Entry: entry}
		}
	}

	cookies := ParseCookies(cookieHeader)
	preferred := cookies[cookieName]
	if preferred != "" {
		if entry := reg.Get(preferred); entry != nil {
			return ResolveResult{Entry: entry}
		}
	}

	if defaultServer != "" {
		if entry := reg.Get(defaultServer); entry != nil {
			return ResolveResult{Entry: entry}
		}
	}

	return ResolveResult{Redirect: true}
}

// MakeSetCookie creates the Set-Cookie header value for the given server name.
// Server names like "repo/branch" are URL-encoded.
func MakeSetCookie(cookieName, serverName string) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    url.QueryEscape(serverName),
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	}
}
