package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/registry"
	"github.com/derekgould/multi-dev-proxy/internal/routing"
)

// serverEntryJSON is the JSON shape for a registered server.
type serverEntryJSON struct {
	Repo         string    `json:"repo"`
	Group        string    `json:"group,omitempty"`
	Port         int       `json:"port"`
	PID          int       `json:"pid"`
	RegisteredAt time.Time `json:"registeredAt"`
}

// registerBody is the expected JSON body for POST /__mdp/register.
type registerBody struct {
	Name        string `json:"name"`
	Port        int    `json:"port"`
	PID         int    `json:"pid"`
	Repo        string `json:"repo"`
	Group       string `json:"group"`
	Scheme      string `json:"scheme"`
	TLSCertPath string `json:"tlsCertPath"`
	TLSKeyPath  string `json:"tlsKeyPath"`
}

// TLSUpgradeFunc is called when a server registers with TLS cert paths.
// The proxy can use this to dynamically upgrade to HTTPS.
type TLSUpgradeFunc func(certPath, keyPath string)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func urlDecode(s string) (string, error) {
	return url.PathUnescape(s)
}

// HealthHandler handles GET /__mdp/health
func HealthHandler(reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"servers": reg.Count(),
		})
	}
}

// ServersHandler handles GET /__mdp/servers
// Returns servers grouped by repo.
func ServersHandler(reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		grouped := reg.ListGroupedByRepo()
		result := make(map[string]map[string]serverEntryJSON)
		for repo, entries := range grouped {
			result[repo] = make(map[string]serverEntryJSON)
			for _, e := range entries {
				result[repo][e.Name] = serverEntryJSON{
					Repo:         e.Repo,
					Group:        e.Group,
					Port:         e.Port,
					PID:          e.PID,
					RegisteredAt: e.RegisteredAt,
				}
			}
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// RegisterHandler handles POST /__mdp/register
func RegisterHandler(reg *registry.Registry, onTLSUpgrade ...TLSUpgradeFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var body registerBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		if body.Name == "" || body.Port <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and port are required"})
			return
		}
		repo := body.Repo
		if repo == "" {
			if idx := strings.LastIndex(body.Name, "/"); idx >= 0 {
				repo = body.Name[:idx]
			} else {
				repo = body.Name
			}
		}
		scheme := body.Scheme
		if scheme == "" {
			scheme = "http"
		}
		entry := &registry.ServerEntry{
			Name:        body.Name,
			Repo:        repo,
			Group:       body.Group,
			Port:        body.Port,
			PID:         body.PID,
			Scheme:      scheme,
			TLSCertPath: body.TLSCertPath,
			TLSKeyPath:  body.TLSKeyPath,
		}
		if err := reg.Register(entry); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if body.TLSCertPath != "" && body.TLSKeyPath != "" && len(onTLSUpgrade) > 0 && onTLSUpgrade[0] != nil {
			onTLSUpgrade[0](body.TLSCertPath, body.TLSKeyPath)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// DeregisterHandler handles DELETE /__mdp/register/{name}
func DeregisterHandler(reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		name := r.PathValue("name")
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		decodedName, err := urlDecode(name)
		if err != nil {
			decodedName = name
		}
		deleted := reg.Deregister(decodedName)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted})
	}
}

// SwitchHandler handles POST /__mdp/switch/{name}.
// Sets both a cookie (for browser per-tab routing) and the registry default
// (for cookie-less clients like dev-server proxies and curl).
func SwitchHandler(reg *registry.Registry, cookieName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		name := r.PathValue("name")
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		decodedName, err := urlDecode(name)
		if err != nil {
			decodedName = name
		}
		if reg.Get(decodedName) == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
			return
		}
		cookie := routing.MakeSetCookie(cookieName, decodedName)
		http.SetCookie(w, cookie)
		_ = reg.SetDefault(decodedName)
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

// DefaultHandler handles GET/POST/DELETE /__mdp/default.
func DefaultHandler(reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]string{"default": reg.GetDefault()})
		case http.MethodDelete:
			reg.ClearDefault()
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	}
}

// DefaultSetHandler handles POST /__mdp/default/{name}.
func DefaultSetHandler(reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		name := r.PathValue("name")
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		decodedName, err := urlDecode(name)
		if err != nil {
			decodedName = name
		}
		if err := reg.SetDefault(decodedName); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// ConfigResponse is the JSON shape for GET /__mdp/config.
type ConfigResponse struct {
	Port       int             `json:"port"`
	CookieName string          `json:"cookieName"`
	Label      string          `json:"label"`
	Default    string          `json:"default"`
	Siblings   []SiblingProxy  `json:"siblings,omitempty"`
	Groups     map[string][]string `json:"groups,omitempty"`
}

// SiblingProxy describes another proxy managed by the same orchestrator.
type SiblingProxy struct {
	Port       int    `json:"port"`
	Label      string `json:"label"`
	CookieName string `json:"cookieName"`
}

// ConfigHandlerFunc is a function providing ConfigResponse dynamically.
type ConfigHandlerFunc func() ConfigResponse

// ConfigHandler handles GET /__mdp/config.
func ConfigHandler(getConfig ConfigHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, getConfig())
	}
}

// CORSMiddleware adds permissive CORS headers to /__mdp/* responses.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__mdp/") {
			origin := r.Header.Get("Origin")
			if origin == "" {
				origin = "*"
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
