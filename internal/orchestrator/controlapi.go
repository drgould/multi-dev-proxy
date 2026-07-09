package orchestrator

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/api"
	"github.com/derekgould/multi-dev-proxy/internal/process"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
	"github.com/derekgould/multi-dev-proxy/internal/statedir"
	"github.com/derekgould/multi-dev-proxy/internal/ui"
)

// ControlAPI handles the orchestrator HTTP control endpoints.
type ControlAPI struct {
	orch          *Orchestrator
	shutdownFn    func()
	dashboardPort int    // 0 if the dashboard server is not running
	logDir        string // where log files are looked up; overridable in tests
}

// NewControlAPI creates a new control API handler.
func NewControlAPI(orch *Orchestrator, shutdownFn func()) *ControlAPI {
	return &ControlAPI{orch: orch, shutdownFn: shutdownFn, logDir: statedir.Dir()}
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
	mux.HandleFunc("GET /__mdp/logs", c.handleListLogs)
	mux.HandleFunc("GET /__mdp/logs/{id}", c.handleTailLog)
	mux.HandleFunc("POST /__mdp/servers/stop", c.handleStopServer)
	mux.HandleFunc("GET /__mdp/peers", c.handlePeerLookup)
	mux.HandleFunc("POST /__mdp/heartbeat", c.handleHeartbeat)
	mux.HandleFunc("POST /__mdp/disconnect", c.handleDisconnect)
	mux.HandleFunc("GET /__mdp/shutdown/watch", c.handleShutdownWatch)
	mux.HandleFunc("POST /__mdp/shutdown", c.handleShutdown)
	mux.HandleFunc("GET /__mdp/events", api.SSEHandler(c.orch.Broadcaster()))
	return c.corsMiddleware(mux)
}

// corsMiddleware allows the dashboard (served on a different local port) to
// call the control API, while rejecting other cross-origin browser requests.
// Reflecting an arbitrary Origin with credentials would let any page the user
// visits drive the control API (CSRF: switch/stop/shutdown) and read log
// output cross-origin (exfiltration) — the loopback bind is no defense since
// the request comes from the user's own browser. Non-browser clients (the CLI,
// TUI, curl) send no Origin and are unaffected.
func (c *ControlAPI) corsMiddleware(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	if c.dashboardPort > 0 {
		allowed[fmt.Sprintf("http://localhost:%d", c.dashboardPort)] = true
		allowed[fmt.Sprintf("http://127.0.0.1:%d", c.dashboardPort)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if !allowed[origin] {
				http.Error(w, "cross-origin request forbidden", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
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
		"ok":            true,
		"proxies":       len(c.orch.ListProxies()),
		"dashboardPort": c.dashboardPort,
	})
}

func (c *ControlAPI) handleListProxies(w http.ResponseWriter, r *http.Request) {
	proxies := c.orch.ListProxies()
	type proxyJSON struct {
		Port       int              `json:"port"`
		Label      string           `json:"label"`
		CookieName string           `json:"cookieName"`
		Default    string           `json:"default"`
		Servers    []map[string]any `json:"servers"`
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
	Name        string            `json:"name"`
	Port        int               `json:"port"`
	PID         int               `json:"pid"`
	ProxyPort   int               `json:"proxyPort"`
	Group       string            `json:"group"`
	Repo        string            `json:"repo"`
	Scheme      string            `json:"scheme"`
	TLSCertPath string            `json:"tlsCertPath"`
	TLSKeyPath  string            `json:"tlsKeyPath"`
	ClientID    string            `json:"clientID"`
	Env         map[string]string `json:"env"`
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
		Env:         body.Env,
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
			c.orch.shutdownIfEmpty(pi)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted})
}

// handleStopServer gracefully stops the process behind a registered server.
// The signal runs asynchronously (a graceful stop can take seconds); the
// deregistration then flows through the existing supervisor/pruner paths.
func (c *ControlAPI) handleStopServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	// Read PIDs from registry copies (List locks internally) rather than off a
	// live *ServerEntry pointer, which would race with UpdatePID. Scan proxies
	// in port order and take the first live PID so the choice is deterministic
	// when a name is registered on more than one proxy.
	proxies := c.orch.ListProxies()
	sort.Slice(proxies, func(i, j int) bool { return proxies[i].Port < proxies[j].Port })
	pid := 0
	found := false
	for _, pi := range proxies {
		for _, entry := range pi.Registry.List() {
			if entry.Name != body.Name {
				continue
			}
			found = true
			if entry.PID > 0 && pid == 0 {
				pid = entry.PID
			}
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown server"})
		return
	}
	if pid == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "server has no PID (externally managed)"})
		return
	}
	go func() {
		if err := process.GracefulStop(pid, 5*time.Second); err != nil {
			slog.Warn("graceful stop failed", "name", body.Name, "pid", pid, "err", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "pid": pid})
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
	writeJSON(w, http.StatusOK, c.orch.GroupsForRepo(r.URL.Query().Get("repo")))
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

// handlePeerLookup answers cross-repo @-references by returning the matching
// service's port and exposed env vars. Lookup keys are (group, repo, service);
// service may be the bare service name or "<group>/<service>" form (the way
// it was registered by mdp run).
//
// `kind` ("port" or "env") tells the handler which form the caller is using.
// For "port", a service with multiple registrations (multi-port) is ambiguous
// because each registration has a different backend Port — 409 Conflict
// surfaces this loudly instead of silently returning whichever entry the
// proxy map iterates first. For "env", all matches share the same Env map
// (it's the parent service's resolved env), so the first match is sufficient.
func (c *ControlAPI) handlePeerLookup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	group, repo, service := q.Get("group"), q.Get("repo"), q.Get("service")
	if group == "" || repo == "" || service == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "group, repo, and service query params are required"})
		return
	}
	kind := q.Get("kind")
	if kind == "" {
		kind = "port"
	}
	entries := c.orch.findPeers(group, repo, service)
	if len(entries) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "peer not found"})
		return
	}
	if kind == "port" && len(entries) > 1 {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("service %q registers on %d proxies; bare .port ref is ambiguous — use @<repo>.%s.env.<KEY> instead", service, len(entries), service),
		})
		return
	}
	e := entries[0]
	writeJSON(w, http.StatusOK, map[string]any{
		"port": e.Port,
		"env":  e.Env,
	})
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
// dashboardPort is the port the dashboard web UI is serving on (0 if it
// failed to start); it is reported on the health endpoint so clients can
// discover the running daemon's actual dashboard URL.
func StartControlServer(orch *Orchestrator, port, dashboardPort int, shutdownFn func()) (*http.Server, error) {
	capi := NewControlAPI(orch, shutdownFn)
	capi.dashboardPort = dashboardPort
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
