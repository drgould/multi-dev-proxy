package orchestrator

import (
	"sync"
	"time"
)

// ClientSession tracks a connected mdp run process.
type ClientSession struct {
	ClientID      string
	LastHeartbeat time.Time
}

// SessionTracker manages client sessions for heartbeat-based cleanup.
type SessionTracker struct {
	mu       sync.RWMutex
	sessions map[string]*ClientSession
}

// NewSessionTracker creates a new session tracker.
func NewSessionTracker() *SessionTracker {
	return &SessionTracker{sessions: make(map[string]*ClientSession)}
}

// Touch creates or updates the heartbeat timestamp for a client.
func (st *SessionTracker) Touch(clientID string) {
	if clientID == "" {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[clientID]
	if !ok {
		s = &ClientSession{ClientID: clientID}
		st.sessions[clientID] = s
	}
	s.LastHeartbeat = time.Now()
}

// Remove deletes a client session.
func (st *SessionTracker) Remove(clientID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.sessions, clientID)
}

// StaleIDs returns client IDs whose last heartbeat is older than maxAge.
func (st *SessionTracker) StaleIDs(maxAge time.Duration) []string {
	st.mu.RLock()
	defer st.mu.RUnlock()
	now := time.Now()
	var stale []string
	for id, s := range st.sessions {
		if now.Sub(s.LastHeartbeat) > maxAge {
			stale = append(stale, id)
		}
	}
	return stale
}
