package routing

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

func TestParseCookies(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected map[string]string
	}{
		{
			name:     "empty header",
			header:   "",
			expected: map[string]string{},
		},
		{
			name:   "single cookie",
			header: "session=abc123",
			expected: map[string]string{
				"session": "abc123",
			},
		},
		{
			name:   "multiple cookies",
			header: "session=abc123; user=john; theme=dark",
			expected: map[string]string{
				"session": "abc123",
				"user":    "john",
				"theme":   "dark",
			},
		},
		{
			name:   "URL-encoded value",
			header: "__mdp_upstream=" + url.QueryEscape("myapp/feature-branch"),
			expected: map[string]string{
				"__mdp_upstream": "myapp/feature-branch",
			},
		},
		{
			name:   "whitespace trimming",
			header: "  session  =  abc123  ;  user  =  john  ",
			expected: map[string]string{
				"session": "abc123",
				"user":    "john",
			},
		},
		{
			name:     "malformed (no equals)",
			header:   "session; user=john",
			expected: map[string]string{
				"user": "john",
			},
		},
		{
			name:     "empty cookie name (skip)",
			header:   "=value; user=john",
			expected: map[string]string{
				"user": "john",
			},
		},
		{
			name:   "cookie with empty value",
			header: "session=; user=john",
			expected: map[string]string{
				"session": "",
				"user":    "john",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseCookies(tt.header)
			if len(result) != len(tt.expected) {
				t.Errorf("got %d cookies, expected %d", len(result), len(tt.expected))
			}
			for key, expectedVal := range tt.expected {
				if val, ok := result[key]; !ok {
					t.Errorf("missing cookie %q", key)
				} else if val != expectedVal {
					t.Errorf("cookie %q: got %q, expected %q", key, val, expectedVal)
				}
			}
		})
	}
}

func TestCookieNameForPort(t *testing.T) {
	if got := CookieNameForPort(3000); got != "__mdp_upstream_3000" {
		t.Errorf("CookieNameForPort(3000) = %q, want %q", got, "__mdp_upstream_3000")
	}
	if got := CookieNameForPort(4000); got != "__mdp_upstream_4000" {
		t.Errorf("CookieNameForPort(4000) = %q, want %q", got, "__mdp_upstream_4000")
	}
}

func TestResolveUpstream(t *testing.T) {
	const cookie = DefaultCookieName

	tests := []struct {
		name             string
		registrySetup    func() *registry.Registry
		cookieHeader     string
		defaultServer    string
		expectedName     string
		expectedRedirect bool
	}{
		{
			name: "0 servers",
			registrySetup: func() *registry.Registry {
				return registry.New()
			},
			expectedRedirect: false,
		},
		{
			name: "1 server auto-route",
			registrySetup: func() *registry.Registry {
				reg := registry.New()
				reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 1234})
				return reg
			},
			expectedName:     "app/main",
			expectedRedirect: false,
		},
		{
			name: "2 servers with valid cookie",
			registrySetup: func() *registry.Registry {
				reg := registry.New()
				reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 1234})
				reg.Register(&registry.ServerEntry{Name: "app/feature", Repo: "app", Port: 3001, PID: 1235})
				return reg
			},
			cookieHeader:     DefaultCookieName + "=" + url.QueryEscape("app/feature"),
			expectedName:     "app/feature",
			expectedRedirect: false,
		},
		{
			name: "2 servers with stale cookie",
			registrySetup: func() *registry.Registry {
				reg := registry.New()
				reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 1234})
				reg.Register(&registry.ServerEntry{Name: "app/feature", Repo: "app", Port: 3001, PID: 1235})
				return reg
			},
			cookieHeader:     DefaultCookieName + "=" + url.QueryEscape("app/deleted"),
			expectedRedirect: true,
		},
		{
			name: "2 servers with no cookie",
			registrySetup: func() *registry.Registry {
				reg := registry.New()
				reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 1234})
				reg.Register(&registry.ServerEntry{Name: "app/feature", Repo: "app", Port: 3001, PID: 1235})
				return reg
			},
			expectedRedirect: true,
		},
		{
			name: "3 servers with valid cookie",
			registrySetup: func() *registry.Registry {
				reg := registry.New()
				reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 1234})
				reg.Register(&registry.ServerEntry{Name: "app/feature", Repo: "app", Port: 3001, PID: 1235})
				reg.Register(&registry.ServerEntry{Name: "app/staging", Repo: "app", Port: 3002, PID: 1236})
				return reg
			},
			cookieHeader:     DefaultCookieName + "=" + url.QueryEscape("app/staging"),
			expectedName:     "app/staging",
			expectedRedirect: false,
		},
		{
			name: "multiple cookies with valid mdp cookie",
			registrySetup: func() *registry.Registry {
				reg := registry.New()
				reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 1234})
				reg.Register(&registry.ServerEntry{Name: "app/feature", Repo: "app", Port: 3001, PID: 1235})
				return reg
			},
			cookieHeader:     "session=xyz; " + DefaultCookieName + "=" + url.QueryEscape("app/feature") + "; user=john",
			expectedName:     "app/feature",
			expectedRedirect: false,
		},
		{
			name: "default upstream used when no cookie",
			registrySetup: func() *registry.Registry {
				reg := registry.New()
				reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 1234})
				reg.Register(&registry.ServerEntry{Name: "app/feature", Repo: "app", Port: 3001, PID: 1235})
				return reg
			},
			defaultServer:    "app/main",
			expectedName:     "app/main",
			expectedRedirect: false,
		},
		{
			name: "cookie takes priority over default",
			registrySetup: func() *registry.Registry {
				reg := registry.New()
				reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 1234})
				reg.Register(&registry.ServerEntry{Name: "app/feature", Repo: "app", Port: 3001, PID: 1235})
				return reg
			},
			cookieHeader:     DefaultCookieName + "=" + url.QueryEscape("app/feature"),
			defaultServer:    "app/main",
			expectedName:     "app/feature",
			expectedRedirect: false,
		},
		{
			name: "stale default ignored",
			registrySetup: func() *registry.Registry {
				reg := registry.New()
				reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 1234})
				reg.Register(&registry.ServerEntry{Name: "app/feature", Repo: "app", Port: 3001, PID: 1235})
				return reg
			},
			defaultServer:    "app/deleted",
			expectedRedirect: true,
		},
		{
			name: "stale cookie falls through to valid default",
			registrySetup: func() *registry.Registry {
				reg := registry.New()
				reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 1234})
				reg.Register(&registry.ServerEntry{Name: "app/feature", Repo: "app", Port: 3001, PID: 1235})
				return reg
			},
			cookieHeader:     DefaultCookieName + "=" + url.QueryEscape("app/deleted"),
			defaultServer:    "app/main",
			expectedName:     "app/main",
			expectedRedirect: false,
		},
		{
			name: "custom cookie name",
			registrySetup: func() *registry.Registry {
				reg := registry.New()
				reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 1234})
				reg.Register(&registry.ServerEntry{Name: "app/feature", Repo: "app", Port: 3001, PID: 1235})
				return reg
			},
			cookieHeader:     "__mdp_upstream_4000=" + url.QueryEscape("app/feature"),
			expectedName:     "app/feature",
			expectedRedirect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := tt.registrySetup()
			cookieName := cookie
			if tt.name == "custom cookie name" {
				cookieName = "__mdp_upstream_4000"
			}
			result := ResolveUpstream(reg, tt.cookieHeader, cookieName, tt.defaultServer, "")

			if result.Redirect != tt.expectedRedirect {
				t.Errorf("redirect: got %v, expected %v", result.Redirect, tt.expectedRedirect)
			}

			if tt.expectedName == "" {
				if result.Entry != nil {
					t.Errorf("entry: got %v, expected nil", result.Entry)
				}
			} else {
				if result.Entry == nil {
					t.Errorf("entry: got nil, expected name %q", tt.expectedName)
				} else if result.Entry.Name != tt.expectedName {
					t.Errorf("entry name: got %q, expected %q", result.Entry.Name, tt.expectedName)
				}
			}
		})
	}
}

func TestMakeSetCookie(t *testing.T) {
	tests := []struct {
		name       string
		cookieName string
		serverName string
		checkFn    func(*http.Cookie) bool
	}{
		{
			name:       "simple name",
			cookieName: DefaultCookieName,
			serverName: "app",
			checkFn: func(c *http.Cookie) bool {
				return c.Name == DefaultCookieName &&
					c.Value == "app" &&
					c.Path == "/" &&
					c.SameSite == http.SameSiteLaxMode
			},
		},
		{
			name:       "name with slash (URL-encoded)",
			cookieName: DefaultCookieName,
			serverName: "myapp/feature-branch",
			checkFn: func(c *http.Cookie) bool {
				return c.Name == DefaultCookieName &&
					c.Value == url.QueryEscape("myapp/feature-branch") &&
					c.Path == "/" &&
					c.SameSite == http.SameSiteLaxMode
			},
		},
		{
			name:       "custom cookie name",
			cookieName: "__mdp_upstream_4000",
			serverName: "myapp/feature-branch",
			checkFn: func(c *http.Cookie) bool {
				return c.Name == "__mdp_upstream_4000" &&
					c.Value == url.QueryEscape("myapp/feature-branch")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cookie := MakeSetCookie(tt.cookieName, tt.serverName)
			if !tt.checkFn(cookie) {
				t.Errorf("cookie validation failed: %+v", cookie)
			}
		})
	}
}

func TestCookieRoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		serverName string
	}{
		{
			name:       "simple name",
			serverName: "app",
		},
		{
			name:       "name with slash",
			serverName: "myapp/feature-branch",
		},
		{
			name:       "name with special chars",
			serverName: "repo/feature@v1.0+build",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cookie := MakeSetCookie(DefaultCookieName, tt.serverName)
			cookies := ParseCookies(DefaultCookieName + "=" + cookie.Value)
			decoded := cookies[DefaultCookieName]

			if decoded != tt.serverName {
				t.Errorf("round-trip failed: got %q, expected %q", decoded, tt.serverName)
			}
		})
	}
}
