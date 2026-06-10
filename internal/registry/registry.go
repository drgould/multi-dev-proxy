package registry

import (
	"errors"
	"sync"
	"time"
)

// ServerEntry represents a registered dev server.
type ServerEntry struct {
	Name                string
	Repo                string
	Group               string // group this service belongs to (typically git branch)
	Port                int
	PID                 int
	Scheme              string // "http" or "https"; defaults to "http"
	TLSCertPath         string // optional: cert file path forwarded by mdp run
	TLSKeyPath          string // optional: key file path forwarded by mdp run
	ClientID            string // identifies the mdp run process that registered this server
	RegisteredAt        time.Time
	ConsecutiveFailures int               // TCP liveness check failure counter (PID=0 servers only)
	HealthCheck         func() bool       // optional; nil falls back to TCPCheck(Port)
	Env                 map[string]string // resolved env vars exposed for cross-repo @-references
}

// EffectiveScheme returns the entry's scheme with the empty value defaulted
// to "http". Registration paths disagree on whether they store "" or "http";
// this method is the canonical home for that defaulting.
func (e *ServerEntry) EffectiveScheme() string {
	if e.Scheme == "" {
		return "http"
	}
	return e.Scheme
}

// Registry holds all registered dev servers in memory.
type Registry struct {
	mu            sync.RWMutex
	servers       map[string]*ServerEntry
	defaultServer string
	notify        func() // optional; see SetNotify
}

// New creates a new empty Registry.
func New() *Registry {
	return &Registry{servers: make(map[string]*ServerEntry)}
}

// SetNotify sets a callback invoked after every externally-visible mutation
// (register/deregister/default/PID changes). SSE-driven UIs rely on it to
// learn about mutations that bypass the orchestrator (per-proxy API handlers,
// the dead-server pruner). Must be called before the registry is shared
// across goroutines.
func (r *Registry) SetNotify(fn func()) {
	r.notify = fn
}

// notifyChange invokes the notify callback if set. Call without holding r.mu.
func (r *Registry) notifyChange() {
	if r.notify != nil {
		r.notify()
	}
}

// Register adds or replaces a server entry. Returns error if validation fails.
func (r *Registry) Register(entry *ServerEntry) error {
	if entry.Name == "" {
		return errors.New("name is required")
	}
	if entry.Port <= 0 {
		return errors.New("port must be positive")
	}
	if entry.RegisteredAt.IsZero() {
		entry.RegisteredAt = time.Now()
	}
	r.mu.Lock()
	prev := r.servers[entry.Name]
	r.servers[entry.Name] = entry
	// Notify only when something UI-visible changed — an identical
	// re-register would make every SSE client refetch identical state.
	// Compared under the lock: entry is published into the map above, so a
	// concurrent UpdatePID may write through the same pointer.
	changed := prev == nil || prev.Repo != entry.Repo || prev.Group != entry.Group ||
		prev.Port != entry.Port || prev.PID != entry.PID ||
		prev.EffectiveScheme() != entry.EffectiveScheme()
	r.mu.Unlock()
	if changed {
		r.notifyChange()
	}
	return nil
}

// Deregister removes a server entry. Returns true if it existed.
// Clears the default if the deregistered server was the default.
func (r *Registry) Deregister(name string) bool {
	r.mu.Lock()
	_, exists := r.servers[name]
	delete(r.servers, name)
	if r.defaultServer == name {
		r.defaultServer = ""
	}
	r.mu.Unlock()
	if exists {
		r.notifyChange()
	}
	return exists
}

// Get returns the entry for the given name, or nil.
func (r *Registry) Get(name string) *ServerEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.servers[name]
}

// List returns snapshot copies of all server entries (order not guaranteed).
func (r *Registry) List() []ServerEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := make([]ServerEntry, 0, len(r.servers))
	for _, e := range r.servers {
		entries = append(entries, *e)
	}
	return entries
}

// ListGroupedByRepo returns snapshot copies of servers grouped by their Repo field.
func (r *Registry) ListGroupedByRepo() map[string][]ServerEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	groups := make(map[string][]ServerEntry)
	for _, e := range r.servers {
		groups[e.Repo] = append(groups[e.Repo], *e)
	}
	return groups
}

// Count returns the number of registered servers.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.servers)
}

// SetDefault sets the default upstream server. Returns error if the server is not registered.
func (r *Registry) SetDefault(name string) error {
	r.mu.Lock()
	if _, ok := r.servers[name]; !ok {
		r.mu.Unlock()
		return errors.New("server not found: " + name)
	}
	changed := r.defaultServer != name
	r.defaultServer = name
	r.mu.Unlock()
	if changed {
		r.notifyChange()
	}
	return nil
}

// ClearDefault removes the default upstream setting.
func (r *Registry) ClearDefault() {
	r.mu.Lock()
	changed := r.defaultServer != ""
	r.defaultServer = ""
	r.mu.Unlock()
	if changed {
		r.notifyChange()
	}
}

// GetDefault returns the current default upstream server name, or "".
func (r *Registry) GetDefault() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultServer
}

// UpdatePID sets the PID for an existing server entry. Returns true if the entry existed.
func (r *Registry) UpdatePID(name string, pid int) bool {
	r.mu.Lock()
	e, ok := r.servers[name]
	changed := ok && e.PID != pid
	if ok {
		e.PID = pid
	}
	r.mu.Unlock()
	if changed {
		r.notifyChange()
	}
	return ok
}

// IncrementFailures increments the consecutive failure counter for the named server.
// Returns the new count, or 0 if the server doesn't exist.
func (r *Registry) IncrementFailures(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.servers[name]; ok {
		e.ConsecutiveFailures++
		return e.ConsecutiveFailures
	}
	return 0
}

// ResetFailures resets the consecutive failure counter for the named server.
func (r *Registry) ResetFailures(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.servers[name]; ok {
		e.ConsecutiveFailures = 0
	}
}

// DeregisterByClientID removes all servers registered by the given client.
// Returns the names of removed entries. Clears default if it was removed.
func (r *Registry) DeregisterByClientID(clientID string) []string {
	if clientID == "" {
		return nil
	}
	r.mu.Lock()
	var removed []string
	for name, e := range r.servers {
		if e.ClientID == clientID {
			delete(r.servers, name)
			removed = append(removed, name)
			if r.defaultServer == name {
				r.defaultServer = ""
			}
		}
	}
	r.mu.Unlock()
	if len(removed) > 0 {
		r.notifyChange()
	}
	return removed
}
