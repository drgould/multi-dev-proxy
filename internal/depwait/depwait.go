// Package depwait coordinates service startup ordering based on declared
// dependencies. Each service gets a State; callers wait on their
// dependencies' states before launching, then close their own state when
// ready (or set Err to propagate failure to dependents).
package depwait

import (
	"context"
	"fmt"
	"time"
)

// State tracks whether a single service is ready yet. Done is closed exactly
// once by the service's own goroutine. Err, if non-nil when Done is closed,
// signals that the service failed to start or become ready.
type State struct {
	Done chan struct{}
	Err  error
}

// NewStates returns a State per name, keyed by the same name.
func NewStates(names []string) map[string]*State {
	m := make(map[string]*State, len(names))
	for _, n := range names {
		m[n] = &State{Done: make(chan struct{})}
	}
	return m
}

// Wait blocks until each named dependency's Done channel closes, or timeout
// elapses per dependency, or ctx is canceled. If any dependency's Err is set
// at close time, Wait returns an error wrapping it.
func Wait(ctx context.Context, states map[string]*State, deps []string, timeout time.Duration) error {
	for _, name := range deps {
		state, ok := states[name]
		if !ok {
			return fmt.Errorf("unknown dependency %q", name)
		}
		select {
		case <-state.Done:
			if state.Err != nil {
				return fmt.Errorf("dependency %q failed: %w", name, state.Err)
			}
		case <-time.After(timeout):
			return fmt.Errorf("dependency %q not ready after %s", name, timeout)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// TCPReady polls check(port) for every port on the poll interval until all
// return true, timeout elapses, or ctx is canceled. Useful for confirming a
// just-started service is actually accepting connections.
func TCPReady(ctx context.Context, ports []int, timeout, poll time.Duration, check func(int) bool) error {
	if len(ports) == 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		allReady := true
		for _, p := range ports {
			if !check(p) {
				allReady = false
				break
			}
		}
		if allReady {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not ready on ports %v after %s", ports, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
