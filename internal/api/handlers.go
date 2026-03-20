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

// SwitchHandler handles POST /__mdp/switch/{name}
func SwitchHandler(reg *registry.Registry) http.HandlerFunc {
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
		cookie := routing.MakeSetCookie(decodedName)
		http.SetCookie(w, cookie)
		http.Redirect(w, r, "/", http.StatusFound)
	}
}
