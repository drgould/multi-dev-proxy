package main

import (
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/ports"
)

func TestPickPortReusesRememberedFreePort(t *testing.T) {
	r := ports.PortRange{Start: 10000, End: 60000}
	finderCalled := false
	finder := func(ports.PortRange, []int) (int, error) {
		finderCalled = true
		return 99999, nil
	}
	isFree := func(int) bool { return true }

	remembered := map[string]int{"api": 12345}
	picked := map[string]int{}

	got, err := pickPort(finder, isFree, remembered, picked, "api", r, nil)
	if err != nil {
		t.Fatalf("pickPort: %v", err)
	}
	if got != 12345 {
		t.Errorf("got port %d, want remembered 12345", got)
	}
	if finderCalled {
		t.Error("finder was called even though the remembered port was free")
	}
	if picked["api"] != 12345 {
		t.Errorf("picked[api] = %d, want 12345", picked["api"])
	}
}

func TestPickPortFallsBackWhenRememberedTaken(t *testing.T) {
	r := ports.PortRange{Start: 10000, End: 60000}
	finder := func(ports.PortRange, []int) (int, error) { return 23456, nil }
	isFree := func(int) bool { return false } // remembered port is in use

	remembered := map[string]int{"api": 12345}
	picked := map[string]int{}

	got, err := pickPort(finder, isFree, remembered, picked, "api", r, nil)
	if err != nil {
		t.Fatalf("pickPort: %v", err)
	}
	if got != 23456 {
		t.Errorf("got port %d, want freshly allocated 23456", got)
	}
	if picked["api"] != 23456 {
		t.Errorf("picked[api] = %d, want 23456", picked["api"])
	}
}

func TestPickPortFallsBackWhenRememberedExcluded(t *testing.T) {
	r := ports.PortRange{Start: 10000, End: 60000}
	finder := func(ports.PortRange, []int) (int, error) { return 23456, nil }
	isFree := func(int) bool { return true }

	remembered := map[string]int{"api": 12345}
	picked := map[string]int{}

	// 12345 is free but already claimed by another service this run.
	got, err := pickPort(finder, isFree, remembered, picked, "api", r, []int{12345})
	if err != nil {
		t.Fatalf("pickPort: %v", err)
	}
	if got != 23456 {
		t.Errorf("got port %d, want freshly allocated 23456", got)
	}
}

func TestPickPortFallsBackWhenRememberedOutOfRange(t *testing.T) {
	// Range was narrowed since the port was remembered; the old port is free
	// but no longer inside the configured range, so it must not be reused.
	r := ports.PortRange{Start: 20000, End: 25000}
	finderCalled := false
	finder := func(ports.PortRange, []int) (int, error) {
		finderCalled = true
		return 21000, nil
	}
	isFree := func(int) bool { return true }

	remembered := map[string]int{"api": 12345} // below r.Start
	picked := map[string]int{}

	got, err := pickPort(finder, isFree, remembered, picked, "api", r, nil)
	if err != nil {
		t.Fatalf("pickPort: %v", err)
	}
	if !finderCalled {
		t.Error("finder was not called for an out-of-range remembered port")
	}
	if got != 21000 {
		t.Errorf("got port %d, want freshly allocated 21000", got)
	}
}

func TestPickPortNilMapsAllocateAndDontPanic(t *testing.T) {
	r := ports.PortRange{Start: 10000, End: 60000}
	finder := func(ports.PortRange, []int) (int, error) { return 34567, nil }
	isFree := func(int) bool { return true }

	// Stable ports disabled: both maps nil. Must not panic and must allocate.
	got, err := pickPort(finder, isFree, nil, nil, "api", r, nil)
	if err != nil {
		t.Fatalf("pickPort: %v", err)
	}
	if got != 34567 {
		t.Errorf("got port %d, want 34567", got)
	}
}
