package hookpty

import (
	"io"
	"sync"
)

// Gate sits in front of a terminal stream. While held (a hook has focus of
// the user's terminal), writes are buffered in memory instead of printed;
// Release flushes the buffer in arrival order and resumes pass-through.
// Safe for concurrent writers.
type Gate struct {
	mu   sync.Mutex
	out  io.Writer
	held bool
	buf  []byte
}

func NewGate(out io.Writer) *Gate {
	return &Gate{out: out}
}

func (g *Gate) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.held {
		g.buf = append(g.buf, p...)
		return len(p), nil
	}
	return g.out.Write(p)
}

// Hold starts buffering writes. Idempotent.
func (g *Gate) Hold() {
	g.mu.Lock()
	g.held = true
	g.mu.Unlock()
}

// Release flushes buffered writes and resumes pass-through. Idempotent.
func (g *Gate) Release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.held = false
	if len(g.buf) > 0 {
		g.out.Write(g.buf)
		g.buf = nil
	}
}
