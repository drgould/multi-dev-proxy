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

func TestResolveUpstream(t *testing.T) {
	tests := []struct {
		name             string
		registrySetup    func() *registry.Registry
		cookieHeader     string
		expectedName     string
		expectedRedirect bool
	}{
		{
			name: "0 servers",
			registrySetup: func() *registry.Registry {
				return registry.New()
			},
			cookieHeader:     "",
			expectedName:     "",
			expectedRedirect: false,
		},
		{
			name: "1 server auto-route",
			registrySetup: func() *registry.Registry {
				reg := registry.New()
				reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 1234})
				return reg
			},
			cookieHeader:     "",
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
			cookieHeader:     "__mdp_upstream=" + url.QueryEscape("app/feature"),
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
			cookieHeader:     "__mdp_upstream=" + url.QueryEscape("app/deleted"),
			expectedName:     "",
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
			cookieHeader:     "",
			expectedName:     "",
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
			cookieHeader:     "__mdp_upstream=" + url.QueryEscape("app/staging"),
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
			cookieHeader:     "session=xyz; __mdp_upstream=" + url.QueryEscape("app/feature") + "; user=john",
			expectedName:     "app/feature",
			expectedRedirect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := tt.registrySetup()
			result := ResolveUpstream(reg, tt.cookieHeader)

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
		serverName string
		checkFn    func(*http.Cookie) bool
	}{
		{
			name:       "simple name",
			serverName: "app",
			checkFn: func(c *http.Cookie) bool {
				return c.Name == CookieName &&
					c.Value == "app" &&
					c.Path == "/" &&
					c.SameSite == http.SameSiteLaxMode
			},
		},
		{
			name:       "name with slash (URL-encoded)",
			serverName: "myapp/feature-branch",
			checkFn: func(c *http.Cookie) bool {
				return c.Name == CookieName &&
					c.Value == url.QueryEscape("myapp/feature-branch") &&
					c.Path == "/" &&
					c.SameSite == http.SameSiteLaxMode
			},
		},
		{
			name:       "name with special chars",
			serverName: "app/feature@v1.0",
			checkFn: func(c *http.Cookie) bool {
				return c.Name == CookieName &&
					c.Value == url.QueryEscape("app/feature@v1.0") &&
					c.Path == "/" &&
					c.SameSite == http.SameSiteLaxMode
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cookie := MakeSetCookie(tt.serverName)
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
			// Encode
			cookie := MakeSetCookie(tt.serverName)

			// Decode
			cookies := ParseCookies(CookieName + "=" + cookie.Value)
			decoded := cookies[CookieName]

			if decoded != tt.serverName {
				t.Errorf("round-trip failed: got %q, expected %q", decoded, tt.serverName)
			}
		})
	}
}
