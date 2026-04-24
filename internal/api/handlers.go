package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/registry"
	"github.com/derekgould/multi-dev-proxy/internal/routing"
)

// LastPathProvider returns the last visited path for a given service name.
type LastPathProvider interface {
	GetLastPath(name string) string
}

// serverEntryJSON is the JSON shape for a registered server.
type serverEntryJSON struct {
	Repo         string    `json:"repo"`
	Group        string    `json:"group,omitempty"`
	Port         int       `json:"port"`
	PID          int       `json:"pid"`
	Scheme       string    `json:"scheme"`
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
				scheme := e.Scheme
				if scheme == "" {
					scheme = "http"
				}
				result[repo][e.Name] = serverEntryJSON{
					Repo:         e.Repo,
					Group:        e.Group,
					Port:         e.Port,
					PID:          e.PID,
					Scheme:       scheme,
					RegisteredAt: e.RegisteredAt,
				}
			}
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// CertLoader loads a TLS keypair into the proxy's cert store. Optional
// dependency for RegisterHandler — when nil, cert paths in the request are
// stored on the registry entry but not loaded.
type CertLoader func(certPath, keyPath string) error

// RegisterHandler handles POST /__mdp/register. If addCert is non-nil and the
// request includes both tlsCertPath and tlsKeyPath, the cert is loaded into
// the proxy's TLS store so the listener can serve HTTPS for the new service.
func RegisterHandler(reg *registry.Registry, addCert CertLoader) http.HandlerFunc {
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
		// Load TLS cert before registering so a bad cert doesn't leave the
		// service half-registered with scheme=https but no listener cert.
		if addCert != nil && body.TLSCertPath != "" && body.TLSKeyPath != "" {
			if err := addCert(body.TLSCertPath, body.TLSKeyPath); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "load TLS cert: " + err.Error()})
				return
			}
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
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// DeregisterHandler handles DELETE /__mdp/register/{name}.
// onDeregistered, if non-nil, is invoked after a successful deregister so the
// caller can react (e.g. shut down an empty proxy).
func DeregisterHandler(reg *registry.Registry, onDeregistered func()) http.HandlerFunc {
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
		if deleted && onDeregistered != nil {
			onDeregistered()
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted})
	}
}

// SwitchHandler handles POST /__mdp/switch/{name}.
// Sets both a cookie (for browser per-tab routing) and the registry default
// (for cookie-less clients like dev-server proxies and curl).
// Redirects to the last visited path for the target service using the
// appropriate scheme (http/https) based on the target service's TLS config.
func SwitchHandler(reg *registry.Registry, cookieName string, lpp LastPathProvider, listenPort int) http.HandlerFunc {
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
		entry := reg.Get(decodedName)
		if entry == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
			return
		}
		cookie := routing.MakeSetCookie(cookieName, decodedName)
		http.SetCookie(w, cookie)
		_ = reg.SetDefault(decodedName)

		path := "/"
		if lpp != nil {
			if last := lpp.GetLastPath(decodedName); last != "" {
				path = last
			}
		}

		// Build absolute URL with the correct scheme for the target service.
		// Services that registered with TLS certs have their certs loaded into
		// the proxy's SmartListener, so the proxy can serve HTTPS for them.
		scheme := entry.Scheme
		if scheme == "" {
			scheme = "http"
		}
		redirectTo := fmt.Sprintf("%s://localhost:%d%s", scheme, listenPort, path)
		http.Redirect(w, r, redirectTo, http.StatusFound)
	}
}

// LastPathHandler handles GET /__mdp/last-path/{name...}.
// Returns the last visited path for the given service.
func LastPathHandler(lpp LastPathProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		decodedName, err := urlDecode(name)
		if err != nil {
			decodedName = name
		}
		path := ""
		if lpp != nil {
			path = lpp.GetLastPath(decodedName)
		}
		writeJSON(w, http.StatusOK, map[string]string{"path": path})
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
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
