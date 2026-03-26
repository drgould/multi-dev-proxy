package orchestrator

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"log/slog"
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
	Type string // "proxy_created", "registered", "deregistered", "default_changed", "group_switched", "service_started", "service_stopped"
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
	cancel     context.CancelFunc
}

// ManagedService tracks a service process started by the orchestrator.
type ManagedService struct {
	Name    string
	Config  config.ServiceConfig
	Group   string
	PID     int
	Port    int
	Status  string // "starting", "running", "stopped", "failed"
}

// Orchestrator manages proxy instances, services, and groups.
type Orchestrator struct {
	mu       sync.RWMutex
	proxies  map[int]*ProxyInstance
	services map[string]*ManagedService
	events   chan Event
	cfg      *config.Config
	tlsCert  string
	tlsKey   string
	useTLS   bool
	host     string
}

// New creates a new Orchestrator.
func New(cfg *config.Config, useTLS bool, tlsCert, tlsKey, host string) *Orchestrator {
	if host == "" {
		host = "0.0.0.0"
	}
	return &Orchestrator{
		proxies:  make(map[int]*ProxyInstance),
		services: make(map[string]*ManagedService),
		events:   make(chan Event, 256),
		cfg:      cfg,
		tlsCert:  tlsCert,
		tlsKey:   tlsKey,
		useTLS:   useTLS,
		host:     host,
	}
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
	prx := proxy.NewProxy(reg, port, o.useTLS, cookieName)
	inj := inject.New()
	prx.SetModifyResponse(inj.ModifyResponse)

	configFn := func() api.ConfigResponse {
		resp := api.ConfigResponse{
			Port:       port,
			CookieName: cookieName,
			Label:      label,
			Default:    reg.GetDefault(),
			Groups:     o.Groups(),
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
	mux.HandleFunc("POST /__mdp/register", api.RegisterHandler(reg))
	mux.HandleFunc("DELETE /__mdp/register/{name...}", api.DeregisterHandler(reg))
	mux.HandleFunc("POST /__mdp/switch/{name...}", api.SwitchHandler(reg, cookieName))
	mux.HandleFunc("GET /__mdp/switch", ui.SwitchPageHandler(reg))
	mux.HandleFunc("GET /__mdp/widget.js", ui.WidgetHandler())
	mux.HandleFunc("GET /__mdp/default", api.DefaultHandler(reg))
	mux.HandleFunc("DELETE /__mdp/default", api.DefaultHandler(reg))
	mux.HandleFunc("POST /__mdp/default/{name...}", api.DefaultSetHandler(reg))
	mux.HandleFunc("GET /__mdp/config", api.ConfigHandler(configFn))
	mux.HandleFunc("POST /__mdp/groups/{name}/switch", func(w http.ResponseWriter, r *http.Request) {
		gname := r.PathValue("name")
		w.Header().Set("Content-Type", "application/json")
		if err := o.SwitchGroup(gname); err != nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"error":%q}`, err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	})
	mux.Handle("/", prx)

	addr := fmt.Sprintf("%s:%d", o.host, port)
	srv := &http.Server{
		Addr:     addr,
		Handler:  api.CORSMiddleware(mux),
		ErrorLog: log.New(io.Discard, "", 0),
	}

	ctx, cancel := context.WithCancel(context.Background())
	registry.StartPruner(ctx, reg, 10*time.Second, process.IsProcessAlive)

	go func() {
		var err error
		if o.useTLS && o.tlsCert != "" {
			cert, loadErr := tls.LoadX509KeyPair(o.tlsCert, o.tlsKey)
			if loadErr != nil {
				slog.Error("load TLS keypair", "err", loadErr, "port", port)
				return
			}
			srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			slog.Error("proxy listener failed", "port", port, "err", err)
		}
	}()

	proto := "http"
	if o.useTLS {
		proto = "https"
	}
	slog.Info("proxy started",
		"addr", fmt.Sprintf("%s://%s", proto, addr),
		"cookie", cookieName,
	)

	pi := &ProxyInstance{
		Port:       port,
		Label:      label,
		Registry:   reg,
		CookieName: cookieName,
		Proxy:      prx,
		Server:     srv,
		cancel:     cancel,
	}
	o.proxies[port] = pi
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
	Servers    []*registry.ServerEntry
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
		Groups: o.Groups(),
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
