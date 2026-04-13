package ui

import (
	"sync"
	"time"
)

// Broadcaster fans out state-change notifications to multiple SSE connections.
type Broadcaster struct {
	mu      sync.Mutex
	clients map[chan struct{}]struct{}
	timer   *time.Timer
}

// NewBroadcaster creates a new Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		clients: make(map[chan struct{}]struct{}),
	}
}

// Subscribe returns a channel that receives a signal on state change,
// and an unsubscribe function to call when done.
func (b *Broadcaster) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.clients, ch)
		b.mu.Unlock()
	}
}

// Notify signals all subscribers that state has changed.
// Debounces rapid events within a 100ms window.
func (b *Broadcaster) Notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer = time.AfterFunc(100*time.Millisecond, b.broadcast)
}

func (b *Broadcaster) broadcast() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- struct{}{}:
		default: // don't block on slow clients
		}
	}
}
