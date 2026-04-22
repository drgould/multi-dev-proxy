package depwait

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitReturnsWhenAllDepsReady(t *testing.T) {
	states := NewStates([]string{"a", "b"})
	close(states["a"].Done)
	close(states["b"].Done)

	if err := Wait(context.Background(), states, []string{"a", "b"}, time.Second); err != nil {
		t.Fatalf("Wait error: %v", err)
	}
}

func TestWaitPropagatesDepError(t *testing.T) {
	states := NewStates([]string{"a"})
	states["a"].Err = errors.New("launch failed")
	close(states["a"].Done)

	err := Wait(context.Background(), states, []string{"a"}, time.Second)
	if err == nil || !errors.Is(err, states["a"].Err) {
		t.Fatalf("Wait error = %v; want wrapping %v", err, states["a"].Err)
	}
}

func TestWaitTimesOut(t *testing.T) {
	states := NewStates([]string{"a"})
	err := Wait(context.Background(), states, []string{"a"}, 30*time.Millisecond)
	if err == nil {
		t.Fatal("Wait should time out")
	}
}

func TestWaitCanceledByCtx(t *testing.T) {
	states := NewStates([]string{"a"})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := Wait(ctx, states, []string{"a"}, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v; want context.Canceled", err)
	}
}

func TestTCPReadyReturnsWhenAllCheckPass(t *testing.T) {
	var calls atomic.Int32
	check := func(p int) bool {
		calls.Add(1)
		return true
	}
	if err := TCPReady(context.Background(), []int{1000, 1001}, time.Second, 10*time.Millisecond, check); err != nil {
		t.Fatalf("TCPReady error: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 checks, got %d", calls.Load())
	}
}

func TestTCPReadyTimesOut(t *testing.T) {
	check := func(int) bool { return false }
	err := TCPReady(context.Background(), []int{1000}, 30*time.Millisecond, 10*time.Millisecond, check)
	if err == nil {
		t.Fatal("TCPReady should time out")
	}
}

func TestTCPReadyNoPortsIsSuccess(t *testing.T) {
	if err := TCPReady(context.Background(), nil, time.Second, 10*time.Millisecond, func(int) bool { return false }); err != nil {
		t.Fatalf("TCPReady error: %v", err)
	}
}
