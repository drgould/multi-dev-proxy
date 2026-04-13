package orchestrator

import (
	"testing"
	"time"
)

func TestSessionTrackerTouch(t *testing.T) {
	st := NewSessionTracker()
	st.Touch("client-1")

	if stale := st.StaleIDs(time.Minute); len(stale) != 0 {
		t.Errorf("freshly touched session should not be stale, got %v", stale)
	}
}

func TestSessionTrackerRemove(t *testing.T) {
	st := NewSessionTracker()
	st.Touch("client-1")
	st.Remove("client-1")

	// Removed session should not appear in stale list
	if stale := st.StaleIDs(0); len(stale) != 0 {
		t.Errorf("removed session should not appear, got %v", stale)
	}
}

func TestSessionTrackerStaleIDs(t *testing.T) {
	st := NewSessionTracker()

	// Manually create a stale session
	st.mu.Lock()
	st.sessions["stale-client"] = &ClientSession{
		ClientID:      "stale-client",
		LastHeartbeat: time.Now().Add(-60 * time.Second),
	}
	st.mu.Unlock()

	st.Touch("fresh-client")

	stale := st.StaleIDs(30 * time.Second)
	if len(stale) != 1 || stale[0] != "stale-client" {
		t.Errorf("expected [stale-client], got %v", stale)
	}
}

func TestSessionTrackerTouchUpdates(t *testing.T) {
	st := NewSessionTracker()

	// Create initially stale session
	st.mu.Lock()
	st.sessions["client-1"] = &ClientSession{
		ClientID:      "client-1",
		LastHeartbeat: time.Now().Add(-60 * time.Second),
	}
	st.mu.Unlock()

	// Touch should refresh it
	st.Touch("client-1")

	if stale := st.StaleIDs(30 * time.Second); len(stale) != 0 {
		t.Errorf("touched session should not be stale, got %v", stale)
	}
}

func TestSessionTrackerEmptyClientID(t *testing.T) {
	st := NewSessionTracker()
	st.Touch("") // should be a no-op
	st.Remove("") // should be a no-op

	if stale := st.StaleIDs(0); len(stale) != 0 {
		t.Errorf("empty clientID should not create session, got %v", stale)
	}
}
