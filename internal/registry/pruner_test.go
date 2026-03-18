package registry

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrunerRemovesDead(t *testing.T) {
	reg := New()
	reg.Register(&ServerEntry{Name: "app/alive", Repo: "app", Port: 1001, PID: 1001})
	reg.Register(&ServerEntry{Name: "app/dead", Repo: "app", Port: 1002, PID: 1002})
	reg.Register(&ServerEntry{Name: "app/also-alive", Repo: "app", Port: 1003, PID: 1003})

	isAlive := func(pid int) bool { return pid != 1002 }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartPruner(ctx, reg, 20*time.Millisecond, isAlive)

	time.Sleep(60 * time.Millisecond)

	if reg.Count() != 2 {
		t.Errorf("expected 2 servers after pruning, got %d", reg.Count())
	}
	if reg.Get("app/dead") != nil {
		t.Error("dead server should have been pruned")
	}
	if reg.Get("app/alive") == nil {
		t.Error("alive server should remain")
	}
}

func TestPrunerStopsOnContextCancel(t *testing.T) {
	reg := New()
	reg.Register(&ServerEntry{Name: "app/main", Repo: "app", Port: 1004, PID: 1004})

	var calls atomic.Int32
	isAlive := func(pid int) bool {
		calls.Add(1)
		return true
	}

	ctx, cancel := context.WithCancel(context.Background())
	StartPruner(ctx, reg, 20*time.Millisecond, isAlive)
	time.Sleep(60 * time.Millisecond)
	cancel()
	time.Sleep(60 * time.Millisecond)
	callsAfterCancel := calls.Load()
	time.Sleep(60 * time.Millisecond)

	if calls.Load() != callsAfterCancel {
		t.Error("pruner continued running after context cancel")
	}
}
