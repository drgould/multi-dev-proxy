package registry

import (
	"errors"
	"sync"
	"time"
)

// ServerEntry represents a registered dev server.
type ServerEntry struct {
	Name         string
	Repo         string
	Port         int
	PID          int
	Scheme       string // "http" or "https"; defaults to "http"
	TLSCertPath  string // optional: cert file path forwarded by mdp run
	TLSKeyPath   string // optional: key file path forwarded by mdp run
	RegisteredAt time.Time
}

// Registry holds all registered dev servers in memory.
type Registry struct {
	mu      sync.RWMutex
	servers map[string]*ServerEntry
}

// New creates a new empty Registry.
func New() *Registry {
	return &Registry{servers: make(map[string]*ServerEntry)}
}

// Register adds or replaces a server entry. Returns error if validation fails.
func (r *Registry) Register(entry *ServerEntry) error {
	if entry.Name == "" {
		return errors.New("name is required")
	}
	if entry.Port <= 0 {
		return errors.New("port must be positive")
	}
	if entry.PID <= 0 {
		return errors.New("pid must be positive")
	}
	if entry.RegisteredAt.IsZero() {
		entry.RegisteredAt = time.Now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.servers[entry.Name] = entry
	return nil
}

// Deregister removes a server entry. Returns true if it existed.
func (r *Registry) Deregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.servers[name]
	delete(r.servers, name)
	return exists
}

// Get returns the entry for the given name, or nil.
func (r *Registry) Get(name string) *ServerEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.servers[name]
}

// List returns all server entries as a slice (order not guaranteed).
func (r *Registry) List() []*ServerEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := make([]*ServerEntry, 0, len(r.servers))
	for _, e := range r.servers {
		entries = append(entries, e)
	}
	return entries
}

// ListGroupedByRepo returns servers grouped by their Repo field.
func (r *Registry) ListGroupedByRepo() map[string][]*ServerEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	groups := make(map[string][]*ServerEntry)
	for _, e := range r.servers {
		groups[e.Repo] = append(groups[e.Repo], e)
	}
	return groups
}

// Count returns the number of registered servers.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.servers)
}
