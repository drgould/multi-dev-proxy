package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/registry"
	"github.com/derekgould/multi-dev-proxy/internal/routing"
)

var errAddCert = errors.New("bad cert")

func newTestRegistry(entries ...*registry.ServerEntry) *registry.Registry {
	reg := registry.New()
	for _, e := range entries {
		_ = reg.Register(e)
	}
	return reg
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
}

func TestHealthHandler(t *testing.T) {
	tests := []struct {
		name            string
		entries         []*registry.ServerEntry
		wantOK          bool
		wantServerCount float64
	}{
		{
			name:            "empty registry",
			entries:         nil,
			wantOK:          true,
			wantServerCount: 0,
		},
		{
			name: "one server",
			entries: []*registry.ServerEntry{
				{Name: "app/main", Repo: "app", Port: 3000, PID: 100},
			},
			wantOK:          true,
			wantServerCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := newTestRegistry(tt.entries...)
			handler := HealthHandler(reg)

			req := httptest.NewRequest(http.MethodGet, "/__mdp/health", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", ct)
			}
			var body map[string]any
			decodeJSON(t, rec, &body)
			if body["ok"] != tt.wantOK {
				t.Errorf("ok = %v, want %v", body["ok"], tt.wantOK)
			}
			if body["servers"] != tt.wantServerCount {
				t.Errorf("servers = %v, want %v", body["servers"], tt.wantServerCount)
			}
		})
	}
}

func TestServersHandler(t *testing.T) {
	tests := []struct {
		name    string
		entries []*registry.ServerEntry
		want    map[string]map[string]any
	}{
		{
			name:    "empty registry",
			entries: nil,
			want:    map[string]map[string]any{},
		},
		{
			name: "one server grouped by repo",
			entries: []*registry.ServerEntry{
				{Name: "app/main", Repo: "app", Port: 3000, PID: 100},
			},
			want: map[string]map[string]any{
				"app": {
					"app/main": map[string]any{
						"repo": "app",
						"port": float64(3000),
						"pid":  float64(100),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := newTestRegistry(tt.entries...)
			handler := ServersHandler(reg)

			req := httptest.NewRequest(http.MethodGet, "/__mdp/servers", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}

			var body map[string]map[string]map[string]any
			decodeJSON(t, rec, &body)

			if len(tt.entries) == 0 {
				if len(body) != 0 {
					t.Errorf("expected empty result, got %v", body)
				}
				return
			}

			for repo, wantEntries := range tt.want {
				gotEntries, ok := body[repo]
				if !ok {
					t.Errorf("missing repo %q in response", repo)
					continue
				}
				for name, wantFields := range wantEntries {
					fields := wantFields.(map[string]any)
					gotEntry, ok := gotEntries[name]
					if !ok {
						t.Errorf("missing server %q in repo %q", name, repo)
						continue
					}
					if gotEntry["repo"] != fields["repo"] {
						t.Errorf("repo = %v, want %v", gotEntry["repo"], fields["repo"])
					}
					if gotEntry["port"] != fields["port"] {
						t.Errorf("port = %v, want %v", gotEntry["port"], fields["port"])
					}
					if gotEntry["pid"] != fields["pid"] {
						t.Errorf("pid = %v, want %v", gotEntry["pid"], fields["pid"])
					}
				}
			}
		})
	}
}

func TestRegisterHandler(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		body       string
		wantStatus int
		wantError  string
	}{
		{
			name:       "valid registration",
			method:     http.MethodPost,
			body:       `{"name":"app/main","port":3000,"pid":100,"repo":"app"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing name",
			method:     http.MethodPost,
			body:       `{"name":"","port":3000,"pid":100}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "name and port are required",
		},
		{
			name:       "port zero",
			method:     http.MethodPost,
			body:       `{"name":"app/main","port":0,"pid":100}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "name and port are required",
		},
		{
			name:       "invalid JSON",
			method:     http.MethodPost,
			body:       `not json`,
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid JSON body",
		},
		{
			name:       "wrong method GET",
			method:     http.MethodGet,
			body:       `{}`,
			wantStatus: http.StatusMethodNotAllowed,
			wantError:  "method not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := registry.New()
			handler := RegisterHandler(reg, nil)

			req := httptest.NewRequest(tt.method, "/__mdp/register", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			var body map[string]any
			decodeJSON(t, rec, &body)

			if tt.wantError != "" {
				if body["error"] != tt.wantError {
					t.Errorf("error = %q, want %q", body["error"], tt.wantError)
				}
			} else {
				if body["ok"] != true {
					t.Errorf("ok = %v, want true", body["ok"])
				}
				if reg.Get("app/main") == nil {
					t.Error("server not found in registry after registration")
				}
			}
		})
	}
}

func TestDeregisterHandler(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		path        string
		preRegister bool
		wantStatus  int
		wantDeleted any
		wantError   string
	}{
		{
			name:        "existing server",
			method:      http.MethodDelete,
			path:        "/__mdp/register/app%2Fmain",
			preRegister: true,
			wantStatus:  http.StatusOK,
			wantDeleted: true,
		},
		{
			name:        "non-existent server",
			method:      http.MethodDelete,
			path:        "/__mdp/register/unknown%2Fbranch",
			preRegister: false,
			wantStatus:  http.StatusOK,
			wantDeleted: false,
		},
		{
			name:       "wrong method POST",
			method:     http.MethodPost,
			path:       "/__mdp/register/app%2Fmain",
			wantStatus: http.StatusMethodNotAllowed,
			wantError:  "method not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := registry.New()
			if tt.preRegister {
				_ = reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 100})
			}
			handler := DeregisterHandler(reg)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			namePart := strings.TrimPrefix(tt.path, "/__mdp/register/")
			if decoded, err := url.PathUnescape(namePart); err == nil {
				req.SetPathValue("name", decoded)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			var body map[string]any
			decodeJSON(t, rec, &body)

			if tt.wantError != "" {
				if body["error"] != tt.wantError {
					t.Errorf("error = %q, want %q", body["error"], tt.wantError)
				}
			} else {
				if body["ok"] != true {
					t.Errorf("ok = %v, want true", body["ok"])
				}
				if body["deleted"] != tt.wantDeleted {
					t.Errorf("deleted = %v, want %v", body["deleted"], tt.wantDeleted)
				}
			}
		})
	}
}

func TestSwitchHandler(t *testing.T) {
	cookieName := routing.DefaultCookieName

	tests := []struct {
		name        string
		method      string
		path        string
		preRegister bool
		wantStatus  int
		wantCookie  bool
		wantError   string
	}{
		{
			name:        "valid server sets cookie and redirects",
			method:      http.MethodPost,
			path:        "/__mdp/switch/app%2Fmain",
			preRegister: true,
			wantStatus:  http.StatusFound,
			wantCookie:  true,
		},
		{
			name:        "unknown server returns 404",
			method:      http.MethodPost,
			path:        "/__mdp/switch/unknown%2Fbranch",
			preRegister: false,
			wantStatus:  http.StatusNotFound,
			wantError:   "server not found",
		},
		{
			name:       "wrong method GET",
			method:     http.MethodGet,
			path:       "/__mdp/switch/app%2Fmain",
			wantStatus: http.StatusMethodNotAllowed,
			wantError:  "method not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := registry.New()
			if tt.preRegister {
				_ = reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 100})
			}
			handler := SwitchHandler(reg, cookieName, nil, 3000)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			namePart := strings.TrimPrefix(tt.path, "/__mdp/switch/")
			if decoded, err := url.PathUnescape(namePart); err == nil {
				req.SetPathValue("name", decoded)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantCookie {
				cookies := rec.Result().Cookies()
				found := false
				for _, c := range cookies {
					if c.Name == cookieName {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected Set-Cookie header with cookie name %q, not found", cookieName)
				}
				if loc := rec.Header().Get("Location"); loc != "http://localhost:3000/" {
					t.Errorf("Location = %q, want http://localhost:3000/", loc)
				}
				if d := reg.GetDefault(); d != "app/main" {
					t.Errorf("default = %q, want %q", d, "app/main")
				}
			}

			if tt.wantError != "" {
				var body map[string]any
				decodeJSON(t, rec, &body)
				if body["error"] != tt.wantError {
					t.Errorf("error = %q, want %q", body["error"], tt.wantError)
				}
			}
		})
	}
}

func TestDefaultHandler(t *testing.T) {
	t.Run("get empty default", func(t *testing.T) {
		reg := registry.New()
		handler := DefaultHandler(reg)
		req := httptest.NewRequest(http.MethodGet, "/__mdp/default", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var body map[string]string
		decodeJSON(t, rec, &body)
		if body["default"] != "" {
			t.Errorf("default = %q, want empty", body["default"])
		}
	})

	t.Run("delete default", func(t *testing.T) {
		reg := registry.New()
		reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000})
		reg.SetDefault("app/main")
		handler := DefaultHandler(reg)
		req := httptest.NewRequest(http.MethodDelete, "/__mdp/default", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if d := reg.GetDefault(); d != "" {
			t.Errorf("default not cleared: %q", d)
		}
	})
}

func TestDefaultSetHandler(t *testing.T) {
	t.Run("set existing", func(t *testing.T) {
		reg := registry.New()
		reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000})
		handler := DefaultSetHandler(reg)
		req := httptest.NewRequest(http.MethodPost, "/__mdp/default/app%2Fmain", nil)
		req.SetPathValue("name", "app/main")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if d := reg.GetDefault(); d != "app/main" {
			t.Errorf("default = %q, want %q", d, "app/main")
		}
	})

	t.Run("set nonexistent", func(t *testing.T) {
		reg := registry.New()
		handler := DefaultSetHandler(reg)
		req := httptest.NewRequest(http.MethodPost, "/__mdp/default/missing", nil)
		req.SetPathValue("name", "missing")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})
}

func TestConfigHandler(t *testing.T) {
	configFn := func() ConfigResponse {
		return ConfigResponse{
			Port:       3000,
			CookieName: "__mdp_upstream",
			Label:      "frontend",
			Default:    "app/main",
			Siblings: []SiblingProxy{
				{Port: 3001, Label: "backend", CookieName: "__mdp_upstream_3001"},
			},
			Groups: map[string][]string{"dev": {"app/dev", "api/dev"}},
		}
	}
	handler := ConfigHandler(configFn)

	req := httptest.NewRequest(http.MethodGet, "/__mdp/config", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body ConfigResponse
	decodeJSON(t, rec, &body)

	if body.Port != 3000 {
		t.Errorf("port = %d, want 3000", body.Port)
	}
	if body.CookieName != "__mdp_upstream" {
		t.Errorf("cookieName = %q, want __mdp_upstream", body.CookieName)
	}
	if body.Default != "app/main" {
		t.Errorf("default = %q, want app/main", body.Default)
	}
	if len(body.Siblings) != 1 {
		t.Fatalf("expected 1 sibling, got %d", len(body.Siblings))
	}
	if body.Siblings[0].Port != 3001 {
		t.Errorf("sibling port = %d, want 3001", body.Siblings[0].Port)
	}
	if len(body.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(body.Groups))
	}
}

func TestCORSMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := CORSMiddleware(inner)

	t.Run("adds CORS headers to __mdp paths", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/__mdp/health", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
			t.Errorf("Allow-Origin = %q, want http://localhost:3000", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("Allow-Credentials = %q, want true", got)
		}
	})

	t.Run("uses wildcard when no origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/__mdp/config", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("Allow-Origin = %q, want *", got)
		}
	})

	t.Run("OPTIONS preflight returns 204", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/__mdp/register", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	})

	t.Run("no CORS headers on non-__mdp paths", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/app", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("expected no CORS on /app, got Allow-Origin = %q", got)
		}
	})

	t.Run("methods header includes expected values", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/__mdp/servers", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		methods := rec.Header().Get("Access-Control-Allow-Methods")
		for _, m := range []string{"GET", "POST", "DELETE", "OPTIONS"} {
			if !strings.Contains(methods, m) {
				t.Errorf("Allow-Methods = %q, missing %s", methods, m)
			}
		}
	})
}

func TestRegisterHandlerInfersRepo(t *testing.T) {
	reg := registry.New()
	handler := RegisterHandler(reg, nil)

	body := `{"name":"myrepo/feature","port":3000,"pid":100}`
	req := httptest.NewRequest(http.MethodPost, "/__mdp/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	entry := reg.Get("myrepo/feature")
	if entry == nil {
		t.Fatal("expected entry to be registered")
	}
	if entry.Repo != "myrepo" {
		t.Errorf("repo = %q, want myrepo (inferred from name)", entry.Repo)
	}
}

func TestRegisterHandlerDefaultScheme(t *testing.T) {
	reg := registry.New()
	handler := RegisterHandler(reg, nil)

	body := `{"name":"app/main","port":3000,"pid":100}`
	req := httptest.NewRequest(http.MethodPost, "/__mdp/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	entry := reg.Get("app/main")
	if entry.Scheme != "http" {
		t.Errorf("scheme = %q, want http (default)", entry.Scheme)
	}
}

func TestRegisterHandlerLoadsTLSCert(t *testing.T) {
	reg := registry.New()
	var gotCert, gotKey string
	addCert := func(certPath, keyPath string) error {
		gotCert, gotKey = certPath, keyPath
		return nil
	}
	handler := RegisterHandler(reg, addCert)

	body := `{"name":"app/main","port":3000,"scheme":"https","tlsCertPath":"/tmp/c.pem","tlsKeyPath":"/tmp/k.pem"}`
	req := httptest.NewRequest(http.MethodPost, "/__mdp/register", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotCert != "/tmp/c.pem" || gotKey != "/tmp/k.pem" {
		t.Errorf("addCert called with (%q,%q), want (/tmp/c.pem,/tmp/k.pem)", gotCert, gotKey)
	}
}

func TestRegisterHandlerAtomicOnTLSFailure(t *testing.T) {
	reg := registry.New()
	addCert := func(certPath, keyPath string) error {
		return errAddCert
	}
	handler := RegisterHandler(reg, addCert)

	body := `{"name":"app/main","port":3000,"scheme":"https","tlsCertPath":"/tmp/c.pem","tlsKeyPath":"/tmp/k.pem"}`
	req := httptest.NewRequest(http.MethodPost, "/__mdp/register", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if reg.Get("app/main") != nil {
		t.Error("service should not be registered when cert load fails")
	}
}

func TestRegisterHandlerSkipsTLSWhenPathsMissing(t *testing.T) {
	reg := registry.New()
	called := false
	addCert := func(certPath, keyPath string) error {
		called = true
		return nil
	}
	handler := RegisterHandler(reg, addCert)

	body := `{"name":"app/main","port":3000}`
	req := httptest.NewRequest(http.MethodPost, "/__mdp/register", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if called {
		t.Error("addCert should not be called when cert paths are absent")
	}
}

func TestDefaultHandlerMethodNotAllowed(t *testing.T) {
	reg := registry.New()
	handler := DefaultHandler(reg)
	req := httptest.NewRequest(http.MethodPost, "/__mdp/default", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// mockLastPathProvider implements LastPathProvider for testing.
type mockLastPathProvider struct {
	paths map[string]string
}

func (m *mockLastPathProvider) GetLastPath(name string) string {
	if m == nil || m.paths == nil {
		return ""
	}
	return m.paths[name]
}

func TestSwitchHandlerRedirectsToLastPath(t *testing.T) {
	cookieName := routing.DefaultCookieName
	reg := registry.New()
	_ = reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 100})

	lpp := &mockLastPathProvider{paths: map[string]string{
		"app/main": "/dashboard?tab=settings",
	}}
	handler := SwitchHandler(reg, cookieName, lpp, 3000)

	req := httptest.NewRequest(http.MethodPost, "/__mdp/switch/app%2Fmain", nil)
	req.SetPathValue("name", "app/main")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "http://localhost:3000/dashboard?tab=settings" {
		t.Errorf("Location = %q, want http://localhost:3000/dashboard?tab=settings", loc)
	}
}

func TestSwitchHandlerHTTPSService(t *testing.T) {
	cookieName := routing.DefaultCookieName
	reg := registry.New()
	_ = reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 100, Scheme: "https"})

	handler := SwitchHandler(reg, cookieName, nil, 3000)

	req := httptest.NewRequest(http.MethodPost, "/__mdp/switch/app%2Fmain", nil)
	req.SetPathValue("name", "app/main")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if loc := rec.Header().Get("Location"); loc != "https://localhost:3000/" {
		t.Errorf("Location = %q, want https://localhost:3000/", loc)
	}
}

func TestSwitchHandlerNoLastPathDefaultsToRoot(t *testing.T) {
	cookieName := routing.DefaultCookieName
	reg := registry.New()
	_ = reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 100})

	lpp := &mockLastPathProvider{paths: map[string]string{}}
	handler := SwitchHandler(reg, cookieName, lpp, 3000)

	req := httptest.NewRequest(http.MethodPost, "/__mdp/switch/app%2Fmain", nil)
	req.SetPathValue("name", "app/main")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if loc := rec.Header().Get("Location"); loc != "http://localhost:3000/" {
		t.Errorf("Location = %q, want http://localhost:3000/", loc)
	}
}

func TestSwitchHandlerNilLastPathProvider(t *testing.T) {
	cookieName := routing.DefaultCookieName
	reg := registry.New()
	_ = reg.Register(&registry.ServerEntry{Name: "app/main", Repo: "app", Port: 3000, PID: 100})

	handler := SwitchHandler(reg, cookieName, nil, 3000)

	req := httptest.NewRequest(http.MethodPost, "/__mdp/switch/app%2Fmain", nil)
	req.SetPathValue("name", "app/main")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if loc := rec.Header().Get("Location"); loc != "http://localhost:3000/" {
		t.Errorf("Location = %q, want http://localhost:3000/", loc)
	}
}

func TestLastPathHandler(t *testing.T) {
	lpp := &mockLastPathProvider{paths: map[string]string{
		"app/main": "/settings",
	}}
	handler := LastPathHandler(lpp)

	t.Run("known service", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/__mdp/last-path/app%2Fmain", nil)
		req.SetPathValue("name", "app/main")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var body map[string]string
		decodeJSON(t, rec, &body)
		if body["path"] != "/settings" {
			t.Errorf("path = %q, want /settings", body["path"])
		}
	})

	t.Run("unknown service returns empty path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/__mdp/last-path/unknown", nil)
		req.SetPathValue("name", "unknown")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var body map[string]string
		decodeJSON(t, rec, &body)
		if body["path"] != "" {
			t.Errorf("path = %q, want empty", body["path"])
		}
	})

	t.Run("nil provider returns empty path", func(t *testing.T) {
		nilHandler := LastPathHandler(nil)
		req := httptest.NewRequest(http.MethodGet, "/__mdp/last-path/app%2Fmain", nil)
		req.SetPathValue("name", "app/main")
		rec := httptest.NewRecorder()
		nilHandler.ServeHTTP(rec, req)

		var body map[string]string
		decodeJSON(t, rec, &body)
		if body["path"] != "" {
			t.Errorf("path = %q, want empty", body["path"])
		}
	})
}

func TestDefaultSetHandlerMethodNotAllowed(t *testing.T) {
	reg := registry.New()
	handler := DefaultSetHandler(reg)
	req := httptest.NewRequest(http.MethodGet, "/__mdp/default/app/main", nil)
	req.SetPathValue("name", "app/main")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
