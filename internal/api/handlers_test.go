package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/registry"
	"github.com/derekgould/multi-dev-proxy/internal/routing"
)

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
			wantError:  "name, port, and pid are required",
		},
		{
			name:       "port zero",
			method:     http.MethodPost,
			body:       `{"name":"app/main","port":0,"pid":100}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "name, port, and pid are required",
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
			handler := RegisterHandler(reg)

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
			handler := SwitchHandler(reg)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantCookie {
				cookies := rec.Result().Cookies()
				found := false
				for _, c := range cookies {
					if c.Name == routing.CookieName {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected Set-Cookie header with cookie name %q, not found", routing.CookieName)
				}
				if loc := rec.Header().Get("Location"); loc != "/" {
					t.Errorf("Location = %q, want /", loc)
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
