package tui

type statusLevel int

const (
	statusNone statusLevel = iota
	statusOK
	statusWarn
	statusErr
)

// status is the transient message shown in the reserved status line.
type status struct {
	level statusLevel
	text  string
	gen   int
}

// pendingAction is the in-flight backend action; further activations are
// ignored until it completes.
type pendingAction struct {
	item Item
	gen  int
	verb string // "switch", "default", "stop"
}
