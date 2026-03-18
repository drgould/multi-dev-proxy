package routing

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

// CookieName is the cookie used to track the active upstream server.
const CookieName = "__mdp_upstream"

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

// ResolveUpstream picks the upstream server based on registry state and cookie.
//   - 0 servers → {nil, false} (show empty switch page inline)
//   - 1 server  → {that server, false} (auto-route, no cookie needed)
//   - N servers + valid cookie → {matched server, false}
//   - N servers + no/stale cookie → {nil, true} (redirect to switch page)
func ResolveUpstream(reg *registry.Registry, cookieHeader string) ResolveResult {
	count := reg.Count()
	if count == 0 {
		return ResolveResult{}
	}
	entries := reg.List()
	if count == 1 {
		return ResolveResult{Entry: entries[0]}
	}
	// Multiple servers — check cookie
	cookies := ParseCookies(cookieHeader)
	preferred := cookies[CookieName]
	if preferred != "" {
		if entry := reg.Get(preferred); entry != nil {
			return ResolveResult{Entry: entry}
		}
	}
	// No valid cookie → redirect
	return ResolveResult{Redirect: true}
}

// MakeSetCookie creates the Set-Cookie header value for the given server name.
// Server names like "repo/branch" are URL-encoded.
func MakeSetCookie(serverName string) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    url.QueryEscape(serverName),
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	}
}
