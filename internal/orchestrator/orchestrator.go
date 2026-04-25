package orchestrator

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/api"
	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/inject"
	"github.com/derekgould/multi-dev-proxy/internal/process"
	"github.com/derekgould/multi-dev-proxy/internal/proxy"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
	"github.com/derekgould/multi-dev-proxy/internal/routing"
	"github.com/derekgould/multi-dev-proxy/internal/ui"
)

// Event represents a state change in the orchestrator.
type Event struct {
	Type string // "proxy_created", "proxy_destroyed", "registered", "deregistered", "default_changed", "group_switched", "service_started", "service_stopped"
	Port int
	Name string
}

// ProxyInstance represents a single managed proxy listener.
type ProxyInstance struct {
	Port       int
	Label      string
	Registry   *registry.Registry
	CookieName string
	Proxy      *proxy.Proxy
	Server     *http.Server
	smartLn    *proxy.SmartListener
	cancel     context.CancelFunc
}

// ManagedService tracks a service process started by the orchestrator.
type ManagedService struct {
	Name   string
	Config config.ServiceConfig
	Group  string
	PID    int
	Port   int
	Status string // "waiting", "starting", "running", "stopped", "failed"
}

// Orchestrator manages proxy instances, services, and groups.
type Orchestrator struct {
	mu          sync.RWMutex
	proxies     map[int]*ProxyInstance
	services    map[string]*ManagedService
	events      chan Event
	broadcaster *ui.Broadcaster
	cfg         *config.Config
	host        string

	sessions     *SessionTracker
	shutdownCh   chan struct{}
	shutdownOnce sync.Once

	certMu sync.RWMutex
	certs  []tls.Certificate // dynamically loaded certs from services
}

// New creates a new Orchestrator.
func New(cfg *config.Config, host string) *Orchestrator {
	if host == "" {
		host = "0.0.0.0"
	}
	return &Orchestrator{
		proxies:     make(map[int]*ProxyInstance),
		services:    make(map[string]*ManagedService),
		events:      make(chan Event, 256),
		broadcaster: ui.NewBroadcaster(),
		cfg:         cfg,
		host:        host,
		sessions:    NewSessionTracker(),
		shutdownCh:  make(chan struct{}),
	}
}

// AddCert loads a TLS certificate from the given paths and adds it to the
// dynamic cert store. Duplicate cert/key pairs are silently ignored.
// When the first cert is added, all proxy listeners automatically start
// accepting TLS connections.
func (o *Orchestrator) AddCert(certPath, keyPath string) error {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("load TLS keypair: %w", err)
	}

	var tlsCfg *tls.Config
	func() {
		o.certMu.Lock()
		defer o.certMu.Unlock()
		for _, existing := range o.certs {
			if certsEqual(existing, cert) {
				return
			}
		}
		o.certs = append(o.certs, cert)
		slog.Info("loaded TLS certificate", "cert", certPath)
		tlsCfg = o.tlsConfigLocked()
	}()

	if tlsCfg == nil {
		return nil // duplicate cert, nothing to update
	}

	// Update all existing proxy listeners with the new TLS config.
	// Lock ordering: mu must be acquired without holding certMu to match
	// createProxyLocked which holds mu then acquires certMu.
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, pi := range o.proxies {
		if pi.smartLn != nil {
			pi.smartLn.SetTLSConfig(tlsCfg)
		}
	}
	return nil
}

// HasCerts returns true if any TLS certificates have been loaded.
func (o *Orchestrator) HasCerts() bool {
	o.certMu.RLock()
	defer o.certMu.RUnlock()
	return len(o.certs) > 0
}

// tlsConfigLocked returns a tls.Config using getCertificate. Must be called
// with certMu held.
func (o *Orchestrator) tlsConfigLocked() *tls.Config {
	if len(o.certs) == 0 {
		return nil
	}
	return &tls.Config{GetCertificate: o.getCertificate}
}

func certsEqual(a, b tls.Certificate) bool {
	if len(a.Certificate) == 0 || len(b.Certificate) == 0 {
		return false
	}
	if len(a.Certificate[0]) != len(b.Certificate[0]) {
		return false
	}
	for i := range a.Certificate[0] {
		if a.Certificate[0][i] != b.Certificate[0][i] {
			return false
		}
	}
	return true
}

func (o *Orchestrator) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	o.certMu.RLock()
	defer o.certMu.RUnlock()
	if len(o.certs) == 0 {
		return nil, fmt.Errorf("no TLS certificates loaded")
	}
	if hello.ServerName != "" {
		for i := range o.certs {
			if err := hello.SupportsCertificate(&o.certs[i]); err == nil {
				return &o.certs[i], nil
			}
		}
	}
	return &o.certs[0], nil
}

// Events returns the event channel for TUI subscription.
func (o *Orchestrator) Events() <-chan Event {
	return o.events
}

func (o *Orchestrator) emit(e Event) {
	select {
	case o.events <- e:
	default:
	}
	o.broadcaster.Notify()
}

// Broadcaster returns the SSE event broadcaster for wiring into HTTP handlers.
func (o *Orchestrator) Broadcaster() *ui.Broadcaster {
	return o.broadcaster
}

// EnsureProxy returns an existing proxy instance or creates a new one.
func (o *Orchestrator) EnsureProxy(port int) (*ProxyInstance, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if pi, ok := o.proxies[port]; ok {
		return pi, nil
	}
	return o.createProxyLocked(port, "")
}

func (o *Orchestrator) createProxyLocked(port int, label string) (*ProxyInstance, error) {
	reg := registry.New()
	cookieName := routing.CookieNameForPort(port)
	prx := proxy.NewProxy(reg, port, cookieName)
	inj := inject.New()
	prx.SetModifyResponse(inj.ModifyResponse)

	ctx, cancel := context.WithCancel(context.Background())
	pi := &ProxyInstance{
		Port:       port,
		Label:      label,
		Registry:   reg,
		CookieName: cookieName,
		Proxy:      prx,
		cancel:     cancel,
	}

	configFn := func() api.ConfigResponse {
		o.mu.RLock()
		defer o.mu.RUnlock()
		resp := api.ConfigResponse{
			Port:       port,
			CookieName: cookieName,
			Label:      label,
			Default:    reg.GetDefault(),
			Groups:     o.groupsLocked(),
		}
		for _, pi := range o.proxies {
			if pi.Port == port {
				continue
			}
			resp.Siblings = append(resp.Siblings, api.SiblingProxy{
				Port:       pi.Port,
				Label:      pi.Label,
				CookieName: pi.CookieName,
			})
		}
		return resp
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /__mdp/health", api.HealthHandler(reg))
	mux.HandleFunc("GET /__mdp/servers", api.ServersHandler(reg))
	mux.HandleFunc("POST /__mdp/register", api.RegisterHandler(reg, o.AddCert))
	mux.HandleFunc("DELETE /__mdp/register/{name...}", api.DeregisterHandler(reg, func() {
		o.shutdownIfEmpty(pi)
	}))
	mux.HandleFunc("POST /__mdp/switch/{name...}", api.SwitchHandler(reg, cookieName, prx, port))
	mux.HandleFunc("GET /__mdp/last-path/{name...}", api.LastPathHandler(prx))
	mux.HandleFunc("GET /__mdp/switch", ui.SwitchPageHandler())
	mux.HandleFunc("GET /__mdp/widget.js", ui.WidgetHandler())
	mux.HandleFunc("GET /__mdp/sw.js", ui.ServiceWorkerHandler())
	mux.HandleFunc("GET /__mdp/events", api.SSEHandler(o.broadcaster))
	mux.HandleFunc("GET /__mdp/default", api.DefaultHandler(reg))
	mux.HandleFunc("DELETE /__mdp/default", api.DefaultHandler(reg))
	mux.HandleFunc("POST /__mdp/default/{name...}", api.DefaultSetHandler(reg))
	mux.HandleFunc("GET /__mdp/config", api.ConfigHandler(configFn))
	mux.HandleFunc("POST /__mdp/groups/{name}/switch", func(w http.ResponseWriter, r *http.Request) {
		gname := r.PathValue("name")
		w.Header().Set("Content-Type", "application/json")
		if err := o.SwitchGroup(gname); err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	mux.Handle("/", prx)

	addr := fmt.Sprintf("%s:%d", o.host, port)
	srv := &http.Server{
		Handler:  api.CORSMiddleware(mux),
		ErrorLog: log.New(io.Discard, "", 0),
	}

	ln, listenErr := net.Listen("tcp", addr)
	if listenErr != nil {
		cancel()
		return nil, fmt.Errorf("listen on %s: %w", addr, listenErr)
	}

	// Smart listener handles both HTTP and HTTPS on the same port.
	// TLS is enabled dynamically when services register with certs.
	o.certMu.RLock()
	tlsCfg := o.tlsConfigLocked()
	o.certMu.RUnlock()
	smartLn := proxy.NewSmartListener(ln, tlsCfg)

	go func() {
		// ShutdownProxy closes the listener synchronously before the async
		// Server.Shutdown sets the internal shutting-down flag, so Serve can
		// return net.ErrClosed instead of http.ErrServerClosed. Treat both as
		// expected shutdown signals.
		if err := srv.Serve(smartLn); err != nil && err != http.ErrServerClosed && !errors.Is(err, net.ErrClosed) {
			slog.Error("proxy listener failed", "port", port, "err", err)
		}
	}()

	slog.Info("proxy started",
		"addr", addr,
		"cookie", cookieName,
	)

	pi.Server = srv
	pi.smartLn = smartLn
	o.proxies[port] = pi
	registry.StartPruner(ctx, reg, 10*time.Second, process.IsProcessAlive, registry.TCPCheck, func() {
		o.shutdownIfEmpty(pi)
	})
	o.emit(Event{Type: "proxy_created", Port: port})
	return pi, nil
}

// Register adds a server entry to the proxy on the given port.
func (o *Orchestrator) Register(proxyPort int, entry *registry.ServerEntry) error {
	pi, err := o.EnsureProxy(proxyPort)
	if err != nil {
		return err
	}
	if err := pi.Registry.Register(entry); err != nil {
		return err
	}
	o.emit(Event{Type: "registered", Port: proxyPort, Name: entry.Name})
	return nil
}

// Heartbeat updates the heartbeat timestamp for a client session.
func (o *Orchestrator) Heartbeat(clientID string) {
	o.sessions.Touch(clientID)
}

// Disconnect removes a client session and deregisters all its servers.
func (o *Orchestrator) Disconnect(clientID string) int {
	if clientID == "" {
		return 0
	}
	o.sessions.Remove(clientID)
	total := 0
	for _, pi := range o.ListProxies() {
		removed := pi.Registry.DeregisterByClientID(clientID)
		for _, name := range removed {
			o.emit(Event{Type: "deregistered", Port: pi.Port, Name: name})
		}
		total += len(removed)
		if len(removed) > 0 {
			o.shutdownIfEmpty(pi)
		}
	}
	if total > 0 {
		slog.Info("client disconnected", "clientID", clientID, "removed", total)
	}
	return total
}

// ShutdownProxy tears down the proxy listening on the given port and releases
// the port. Safe to call for a port that has no proxy (no-op). The listener
// is closed synchronously so the port is immediately rebindable by a fresh
// EnsureProxy. In-flight requests drain asynchronously via http.Server.Shutdown
// so callers don't block.
func (o *Orchestrator) ShutdownProxy(port int) {
	o.mu.Lock()
	pi, ok := o.proxies[port]
	if !ok {
		o.mu.Unlock()
		return
	}
	delete(o.proxies, port)
	pi.cancel()
	o.mu.Unlock()

	// Close the listener synchronously so the port is released before we
	// return — a concurrent EnsureProxy on the same port must be able to
	// net.Listen without racing the goroutine below.
	if pi.smartLn != nil {
		_ = pi.smartLn.Close()
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = pi.Server.Shutdown(ctx)
	}()
	slog.Info("proxy shut down (empty)", "port", port)
	o.emit(Event{Type: "proxy_destroyed", Port: port})
}

// shutdownIfEmpty shuts the proxy down if its registry has no servers.
func (o *Orchestrator) shutdownIfEmpty(pi *ProxyInstance) {
	if pi.Registry.Count() == 0 {
		o.ShutdownProxy(pi.Port)
	}
}

// ShutdownCh returns a channel that is closed when the orchestrator shuts down.
func (o *Orchestrator) ShutdownCh() <-chan struct{} {
	return o.shutdownCh
}

// StartSessionPruner launches a goroutine that cleans up stale client sessions.
func (o *Orchestrator) StartSessionPruner(ctx context.Context, interval, maxAge time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, id := range o.sessions.StaleIDs(maxAge) {
					o.Disconnect(id)
				}
			}
		}
	}()
}

// UpdatePID updates the PID for a named server across all proxies.
func (o *Orchestrator) UpdatePID(name string, pid int) bool {
	updated := false
	for _, pi := range o.ListProxies() {
		if pi.Registry.UpdatePID(name, pid) {
			updated = true
		}
	}
	return updated
}

// SetDefault sets the default upstream on a specific proxy port.
func (o *Orchestrator) SetDefault(proxyPort int, name string) error {
	o.mu.RLock()
	pi, ok := o.proxies[proxyPort]
	o.mu.RUnlock()
	if !ok {
		return fmt.Errorf("proxy on port %d not found", proxyPort)
	}
	if err := pi.Registry.SetDefault(name); err != nil {
		return err
	}
	o.emit(Event{Type: "default_changed", Port: proxyPort, Name: name})
	return nil
}

// ClearDefault clears the default upstream on a specific proxy port.
func (o *Orchestrator) ClearDefault(proxyPort int) error {
	o.mu.RLock()
	pi, ok := o.proxies[proxyPort]
	o.mu.RUnlock()
	if !ok {
		return fmt.Errorf("proxy on port %d not found", proxyPort)
	}
	pi.Registry.ClearDefault()
	o.emit(Event{Type: "default_changed", Port: proxyPort})
	return nil
}

// Groups builds groups dynamically from registered services across all proxies.
func (o *Orchestrator) Groups() map[string][]string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.groupsLocked()
}

func (o *Orchestrator) groupsLocked() map[string][]string {
	groups := make(map[string][]string)
	for _, pi := range o.proxies {
		for _, entry := range pi.Registry.List() {
			if entry.Group != "" {
				groups[entry.Group] = append(groups[entry.Group], entry.Name)
			}
		}
	}
	return groups
}

// findPeer searches every proxy's registry for a service matching the given
// (group, repo, service) tuple. The service argument may be the bare service
// name as it appears in mdp.yaml (e.g. "api") or the registered "<group>/<svc>"
// form. Returns nil if no match exists.
func (o *Orchestrator) findPeer(group, repo, service string) *registry.ServerEntry {
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, pi := range o.proxies {
		for _, entry := range pi.Registry.List() {
			if entry.Group != group || entry.Repo != repo {
				continue
			}
			if entry.Name == service || entry.Name == group+"/"+service {
				e := entry
				return &e
			}
		}
	}
	return nil
}

// SwitchGroup sets the default upstream on every proxy that has a service
// in the named group.
func (o *Orchestrator) SwitchGroup(groupName string) error {
	o.mu.RLock()
	defer o.mu.RUnlock()
	switched := 0
	for _, pi := range o.proxies {
		for _, entry := range pi.Registry.List() {
			if entry.Group == groupName {
				_ = pi.Registry.SetDefault(entry.Name)
				switched++
				break
			}
		}
	}
	if switched == 0 {
		return fmt.Errorf("no services found in group %q", groupName)
	}
	o.emit(Event{Type: "group_switched", Name: groupName})
	return nil
}

// ProxySnapshot is a snapshot of a proxy instance for TUI rendering.
type ProxySnapshot struct {
	Port       int
	Label      string
	CookieName string
	Default    string
	Servers    []registry.ServerEntry
}

// ServiceSnapshot is a snapshot of a managed service.
type ServiceSnapshot struct {
	Name   string
	Group  string
	PID    int
	Port   int
	Status string
}

// Snapshot captures the current state for TUI rendering.
type Snapshot struct {
	Proxies  []ProxySnapshot
	Services []ServiceSnapshot
	Groups   map[string][]string
}

// Snapshot returns the current state for rendering.
func (o *Orchestrator) Snapshot() Snapshot {
	o.mu.RLock()
	defer o.mu.RUnlock()

	snap := Snapshot{
		Groups: o.groupsLocked(),
	}
	for _, pi := range o.proxies {
		snap.Proxies = append(snap.Proxies, ProxySnapshot{
			Port:       pi.Port,
			Label:      pi.Label,
			CookieName: pi.CookieName,
			Default:    pi.Registry.GetDefault(),
			Servers:    pi.Registry.List(),
		})
	}
	for _, svc := range o.services {
		snap.Services = append(snap.Services, ServiceSnapshot{
			Name:   svc.Name,
			Group:  svc.Group,
			PID:    svc.PID,
			Port:   svc.Port,
			Status: svc.Status,
		})
	}
	return snap
}

// ListProxies returns all proxy instances (for control API).
func (o *Orchestrator) ListProxies() []*ProxyInstance {
	o.mu.RLock()
	defer o.mu.RUnlock()
	result := make([]*ProxyInstance, 0, len(o.proxies))
	for _, pi := range o.proxies {
		result = append(result, pi)
	}
	return result
}

// GetProxy returns the proxy instance for the given port, or nil.
func (o *Orchestrator) GetProxy(port int) *ProxyInstance {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.proxies[port]
}

// SetService records a managed service.
func (o *Orchestrator) SetService(name string, svc *ManagedService) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.services[name] = svc
	o.emit(Event{Type: "service_started", Name: name})
}

// ServiceStatus returns the status of a managed service. ok is false if the
// service is not tracked.
func (o *Orchestrator) ServiceStatus(name string) (string, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	svc, ok := o.services[name]
	if !ok {
		return "", false
	}
	return svc.Status, true
}

// UpdateServiceStatus updates a managed service status.
func (o *Orchestrator) UpdateServiceStatus(name, status string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if svc, ok := o.services[name]; ok {
		svc.Status = status
		if status == "stopped" || status == "failed" {
			o.emit(Event{Type: "service_stopped", Name: name})
		}
	}
}

// ListServices returns all managed services.
func (o *Orchestrator) ListServices() []*ManagedService {
	o.mu.RLock()
	defer o.mu.RUnlock()
	result := make([]*ManagedService, 0, len(o.services))
	for _, svc := range o.services {
		result = append(result, svc)
	}
	return result
}

// Shutdown gracefully shuts down all proxies and managed services.
func (o *Orchestrator) Shutdown(ctx context.Context) {
	o.shutdownOnce.Do(func() { close(o.shutdownCh) })

	o.mu.Lock()
	defer o.mu.Unlock()

	for port, pi := range o.proxies {
		pi.cancel()
		if err := pi.Server.Shutdown(ctx); err != nil {
			slog.Error("proxy shutdown error", "port", port, "err", err)
		}
	}
	o.proxies = make(map[int]*ProxyInstance)
	slog.Info("all proxies shut down")
}
