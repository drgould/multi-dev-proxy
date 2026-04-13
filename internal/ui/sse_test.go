package ui

import (
	"testing"
	"time"
)

func TestBroadcasterSubscribeNotify(t *testing.T) {
	b := NewBroadcaster()
	ch, unsub := b.Subscribe()
	defer unsub()

	b.Notify()

	select {
	case <-ch:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected notification within 500ms")
	}
}

func TestBroadcasterMultipleSubscribers(t *testing.T) {
	b := NewBroadcaster()
	ch1, unsub1 := b.Subscribe()
	defer unsub1()
	ch2, unsub2 := b.Subscribe()
	defer unsub2()

	b.Notify()

	for i, ch := range []<-chan struct{}{ch1, ch2} {
		select {
		case <-ch:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("subscriber %d did not receive notification", i)
		}
	}
}

func TestBroadcasterUnsubscribe(t *testing.T) {
	b := NewBroadcaster()
	ch, unsub := b.Subscribe()
	unsub()

	b.Notify()

	select {
	case <-ch:
		t.Fatal("unsubscribed channel should not receive")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestBroadcasterDebounce(t *testing.T) {
	b := NewBroadcaster()
	ch, unsub := b.Subscribe()
	defer unsub()

	// Fire many notifications rapidly
	for i := 0; i < 10; i++ {
		b.Notify()
	}

	// Should receive exactly one notification (debounced)
	select {
	case <-ch:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected at least one notification")
	}

	// Should not receive another immediately
	select {
	case <-ch:
		t.Fatal("debounce should coalesce rapid notifications")
	case <-time.After(200 * time.Millisecond):
	}
}
