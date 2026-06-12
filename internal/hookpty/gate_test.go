package hookpty

import (
	"fmt"
	"sync"
	"testing"
)

func TestGatePassThrough(t *testing.T) {
	var buf syncBuffer
	g := NewGate(&buf)
	g.Write([]byte("a\n"))
	if got := buf.String(); got != "a\n" {
		t.Fatalf("got %q, want %q", got, "a\n")
	}
}

func TestGateHoldBuffersAndReleaseFlushesInOrder(t *testing.T) {
	var buf syncBuffer
	g := NewGate(&buf)
	g.Write([]byte("before\n"))
	g.Hold()
	g.Write([]byte("one\n"))
	g.Write([]byte("two\n"))
	if got := buf.String(); got != "before\n" {
		t.Fatalf("held writes leaked: %q", got)
	}
	g.Release()
	if got := buf.String(); got != "before\none\ntwo\n" {
		t.Fatalf("flush order wrong: %q", got)
	}
	g.Write([]byte("after\n"))
	if got := buf.String(); got != "before\none\ntwo\nafter\n" {
		t.Fatalf("pass-through after release broken: %q", got)
	}
}

func TestGateIdempotentHoldRelease(t *testing.T) {
	var buf syncBuffer
	g := NewGate(&buf)
	g.Hold()
	g.Hold()
	g.Write([]byte("x"))
	g.Release()
	g.Release()
	if got := buf.String(); got != "x" {
		t.Fatalf("got %q, want %q", got, "x")
	}
}

func TestGateConcurrentWriters(t *testing.T) {
	var buf syncBuffer
	g := NewGate(&buf)
	g.Hold()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				fmt.Fprintf(g, "w%d-%d\n", n, j)
			}
		}(i)
	}
	wg.Wait()
	g.Release()
	if got := len(buf.String()); got == 0 {
		t.Fatal("no output flushed")
	}
}
