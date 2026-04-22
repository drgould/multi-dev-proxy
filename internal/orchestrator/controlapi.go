package orchestrator

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/derekgould/multi-dev-proxy/internal/api"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
	"github.com/derekgould/multi-dev-proxy/internal/ui"
)

// ControlAPI handles the orchestrator HTTP control endpoints.
type ControlAPI struct {
	orch       *Orchestrator
	shutdownFn func()
}

// NewControlAPI creates a new control API handler.
func NewControlAPI(orch *Orchestrator, shutdownFn func()) *ControlAPI {
	return &ControlAPI{orch: orch, shutdownFn: shutdownFn}
}

// Handler returns the http.Handler for the control API.
func (c *ControlAPI) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__mdp/health", c.handleHealth)
	mux.HandleFunc("GET /__mdp/proxies", c.handleListProxies)
	mux.HandleFunc("POST /__mdp/register", c.handleRegister)
	mux.HandleFunc("DELETE /__mdp/register/{name...}", c.handleDeregister)
	mux.HandleFunc("PATCH /__mdp/register/{name...}", c.handleUpdatePID)
	mux.HandleFunc("POST /__mdp/proxies/{port}/default/{name...}", c.handleSetDefault)
	mux.HandleFunc("DELETE /__mdp/proxies/{port}/default", c.handleClearDefault)
	mux.HandleFunc("GET /__mdp/groups", c.handleListGroups)
	mux.HandleFunc("POST /__mdp/groups/{name}/switch", c.handleSwitchGroup)
	mux.HandleFunc("GET /__mdp/services", c.handleListServices)
	mux.HandleFunc("POST /__mdp/heartbeat", c.handleHeartbeat)
	mux.HandleFunc("POST /__mdp/disconnect", c.handleDisconnect)
	mux.HandleFunc("GET /__mdp/shutdown/watch", c.handleShutdownWatch)
	mux.HandleFunc("POST /__mdp/shutdown", c.handleShutdown)
	mux.HandleFunc("GET /__mdp/events", api.SSEHandler(c.orch.Broadcaster()))
	return corsMiddleware(mux)
}

// corsMiddleware adds CORS headers to allow the dashboard (on a different port)
// to call the control API.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (c *ControlAPI) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"proxies": len(c.orch.ListProxies()),
	})
}

func (c *ControlAPI) handleListProxies(w http.ResponseWriter, r *http.Request) {
	proxies := c.orch.ListProxies()
	type proxyJSON struct {
		Port       int                      `json:"port"`
		Label      string                   `json:"label"`
		CookieName string                   `json:"cookieName"`
		Default    string                   `json:"default"`
		Servers    []map[string]any         `json:"servers"`
	}
	result := make([]proxyJSON, 0, len(proxies))
	for _, pi := range proxies {
		servers := pi.Registry.List()
		srvList := make([]map[string]any, 0, len(servers))
		for _, s := range servers {
			srvList = append(srvList, map[string]any{
				"name":  s.Name,
				"port":  s.Port,
				"pid":   s.PID,
				"group": s.Group,
			})
		}
		result = append(result, proxyJSON{
			Port:       pi.Port,
			Label:      pi.Label,
			CookieName: pi.CookieName,
			Default:    pi.Registry.GetDefault(),
			Servers:    srvList,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

type controlRegisterBody struct {
	Name        string `json:"name"`
	Port        int    `json:"port"`
	PID         int    `json:"pid"`
	ProxyPort   int    `json:"proxyPort"`
	Group       string `json:"group"`
	Repo        string `json:"repo"`
	Scheme      string `json:"scheme"`
	TLSCertPath string `json:"tlsCertPath"`
	TLSKeyPath  string `json:"tlsKeyPath"`
	ClientID    string `json:"clientID"`
}

func (c *ControlAPI) handleRegister(w http.ResponseWriter, r *http.Request) {
	var body controlRegisterBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if body.Name == "" || body.Port <= 0 || body.ProxyPort <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name, port, and proxyPort are required"})
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
	// Bind the proxy port up front so a port-bind failure doesn't leave the
	// cert store mutated by a subsequent AddCert call.
	if _, err := c.orch.EnsureProxy(body.ProxyPort); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Load TLS cert before registering so a bad cert doesn't leave the
	// service half-registered with scheme=https but no listener cert.
	if body.TLSCertPath != "" && body.TLSKeyPath != "" {
		if err := c.orch.AddCert(body.TLSCertPath, body.TLSKeyPath); err != nil {
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
		ClientID:    body.ClientID,
	}
	if err := c.orch.Register(body.ProxyPort, entry); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if body.ClientID != "" {
		c.orch.Heartbeat(body.ClientID)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (c *ControlAPI) handleDeregister(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	deleted := false
	for _, pi := range c.orch.ListProxies() {
		if pi.Registry.Deregister(name) {
			deleted = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted})
}

func (c *ControlAPI) handleUpdatePID(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	var body struct {
		PID int `json:"pid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if body.PID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pid must be positive"})
		return
	}
	updated := c.orch.UpdatePID(name, body.PID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": updated})
}

func (c *ControlAPI) handleSetDefault(w http.ResponseWriter, r *http.Request) {
	portStr := r.PathValue("port")
	name := r.PathValue("name")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid port"})
		return
	}
	if err := c.orch.SetDefault(port, name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (c *ControlAPI) handleClearDefault(w http.ResponseWriter, r *http.Request) {
	portStr := r.PathValue("port")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid port"})
		return
	}
	if err := c.orch.ClearDefault(port); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (c *ControlAPI) handleListGroups(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, c.orch.Groups())
}

func (c *ControlAPI) handleSwitchGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := c.orch.SwitchGroup(name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (c *ControlAPI) handleListServices(w http.ResponseWriter, r *http.Request) {
	services := c.orch.ListServices()
	type svcJSON struct {
		Name   string `json:"name"`
		Group  string `json:"group"`
		PID    int    `json:"pid"`
		Port   int    `json:"port"`
		Status string `json:"status"`
	}
	result := make([]svcJSON, 0, len(services))
	for _, s := range services {
		result = append(result, svcJSON{
			Name:   s.Name,
			Group:  s.Group,
			PID:    s.PID,
			Port:   s.Port,
			Status: s.Status,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (c *ControlAPI) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientID string `json:"clientID"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if body.ClientID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "clientID is required"})
		return
	}
	c.orch.Heartbeat(body.ClientID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (c *ControlAPI) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientID string `json:"clientID"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if body.ClientID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "clientID is required"})
		return
	}
	removed := c.orch.Disconnect(body.ClientID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": removed})
}

func (c *ControlAPI) handleShutdownWatch(w http.ResponseWriter, r *http.Request) {
	select {
	case <-c.orch.ShutdownCh():
		writeJSON(w, http.StatusOK, map[string]bool{"shutting_down": true})
	case <-r.Context().Done():
		// client disconnected
	}
}

func (c *ControlAPI) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	if c.shutdownFn != nil {
		go c.shutdownFn()
	}
}

// StartDashboardServer starts the dashboard web UI on the given port.
// controlPort is the port of the control API that the dashboard JS will call.
func StartDashboardServer(controlPort, dashboardPort int) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", ui.DashboardHandler(controlPort))

	addr := fmt.Sprintf("127.0.0.1:%d", dashboardPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dashboard listen on %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler:  mux,
		ErrorLog: log.New(io.Discard, "", 0),
	}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("dashboard serve failed", "addr", addr, "err", err)
		}
	}()
	slog.Info("dashboard started", "url", fmt.Sprintf("http://localhost:%d", dashboardPort))
	return srv, nil
}

// StartControlServer starts the control API server on the given port.
func StartControlServer(orch *Orchestrator, port int, shutdownFn func()) (*http.Server, error) {
	capi := NewControlAPI(orch, shutdownFn)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("control API listen on %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler:  capi.Handler(),
		ErrorLog: log.New(io.Discard, "", 0),
	}

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("control API serve failed", "addr", addr, "err", err)
		}
	}()

	slog.Info("control API started", "addr", addr)
	return srv, nil
}
