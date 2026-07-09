package tui

import "github.com/derekgould/multi-dev-proxy/internal/orchestrator"

// EventMsg wraps an orchestrator event for the TUI update loop.
type EventMsg orchestrator.Event

// snapshotMsg delivers a freshly fetched snapshot to the model.
type snapshotMsg struct {
	snap orchestrator.Snapshot
}

// actionDoneMsg reports the result of an asynchronously executed backend
// action (group switch or default change).
type actionDoneMsg struct {
	verb   string // "switch", "default"
	target string
	gen    int
	err    error
}

// clearStatusMsg expires the transient status line; stale generations are
// ignored so a newer status is never wiped early.
type clearStatusMsg struct {
	gen int
}

// logSourcesMsg delivers the available log sources.
type logSourcesMsg struct {
	sources []LogSource
	err     error
}

// logChunkMsg delivers one cursor read of the given log source. gen is the
// tail generation the fetch was issued under; a mismatch means the source or
// cursor was reset meanwhile and the response must be discarded.
type logChunkMsg struct {
	id    string
	gen   int
	chunk LogChunk
	err   error
}
