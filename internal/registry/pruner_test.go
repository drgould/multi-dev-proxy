package registry

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrunerRemovesDead(t *testing.T) {
	reg := New()
	pastGrace := time.Now().Add(-60 * time.Second)
	reg.Register(&ServerEntry{Name: "app/alive", Repo: "app", Port: 1001, PID: 1001, RegisteredAt: pastGrace})
	reg.Register(&ServerEntry{Name: "app/dead", Repo: "app", Port: 1002, PID: 1002, RegisteredAt: pastGrace})
	reg.Register(&ServerEntry{Name: "app/also-alive", Repo: "app", Port: 1003, PID: 1003, RegisteredAt: pastGrace})

	isAlive := func(pid int) bool { return pid != 1002 }
	// Port 1002 is also dead — distinguishes "truly dead" from "detached".
	tcpCheck := func(port int) bool { return port != 1002 }

	// Tick past the failure threshold so the dead/unreachable entry deregisters.
	for i := 0; i < tcpFailThreshold; i++ {
		pruneOnce(reg, isAlive, tcpCheck)
	}

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

// A batch-mode entry carries both a PID and a clientID. When the tracked
// process exits but the port is still serving (detached case), the registry
// pruner must keep the entry alive — the session pruner alone would not
// notice until the `mdp run` client disconnected.
func TestPrunerKeepsBatchEntryWhenProcessDeadButPortAlive(t *testing.T) {
	reg := New()
	reg.Register(&ServerEntry{
		Name:         "app/batch-detached",
		Repo:         "app",
		Port:         5010,
		PID:          99999,
		ClientID:     "batch-run-session",
		RegisteredAt: time.Now().Add(-60 * time.Second),
	})

	isAlive := func(pid int) bool { return false }
	tcpAlive := func(port int) bool { return true }

	for i := 0; i < 10; i++ {
		pruneOnce(reg, isAlive, tcpAlive)
	}

	if reg.Get("app/batch-detached") == nil {
		t.Fatal("batch-mode detached service should persist while port is reachable")
	}
}

// Detached services (process exits but port still serves) must be kept in the
// registry — this is the whole point of the health-check fallback.
func TestPrunerKeepsEntryWhenProcessDeadButProbePasses(t *testing.T) {
	reg := New()
	reg.Register(&ServerEntry{
		Name:         "app/detached",
		Repo:         "app",
		Port:         5000,
		PID:          99999,
		RegisteredAt: time.Now().Add(-60 * time.Second),
	})

	isAlive := func(pid int) bool { return false }       // process exited
	tcpAlive := func(port int) bool { return true }      // but port still up

	for i := 0; i < 10; i++ {
		pruneOnce(reg, isAlive, tcpAlive)
	}

	if reg.Get("app/detached") == nil {
		t.Fatal("detached service should persist while port is reachable")
	}
	if got := reg.Get("app/detached").ConsecutiveFailures; got != 0 {
		t.Errorf("failure counter should stay at 0, got %d", got)
	}
}

func TestPrunerUsesCustomHealthCheck(t *testing.T) {
	reg := New()
	reg.Register(&ServerEntry{
		Name:         "app/custom",
		Repo:         "app",
		Port:         5001,
		PID:          0,
		RegisteredAt: time.Now().Add(-60 * time.Second),
		HealthCheck:  func() bool { return false },
	})

	isAlive := func(pid int) bool { return true }
	tcpAlive := func(port int) bool { return true } // default TCP would say healthy

	for i := 0; i < tcpFailThreshold; i++ {
		pruneOnce(reg, isAlive, tcpAlive)
	}

	if reg.Get("app/custom") != nil {
		t.Error("custom health check returning false should override default TCP probe and trigger pruning")
	}
}

// Detached service whose port later goes away must be pruned after the
// failure threshold.
func TestPrunerDeregistersDetachedWhenPortDies(t *testing.T) {
	reg := New()
	reg.Register(&ServerEntry{
		Name:         "app/detached",
		Repo:         "app",
		Port:         5002,
		PID:          99999,
		RegisteredAt: time.Now().Add(-60 * time.Second),
	})

	isAlive := func(pid int) bool { return false }
	var alive bool
	tcpCheck := func(port int) bool { return alive }

	// Port up initially — entry should persist across many ticks.
	alive = true
	for i := 0; i < 5; i++ {
		pruneOnce(reg, isAlive, tcpCheck)
	}
	if reg.Get("app/detached") == nil {
		t.Fatal("entry should still exist while port is reachable")
	}

	// Port goes down — after threshold consecutive failures, prune.
	alive = false
	for i := 0; i < tcpFailThreshold; i++ {
		pruneOnce(reg, isAlive, tcpCheck)
	}
	if reg.Get("app/detached") != nil {
		t.Error("entry should be pruned once port stops responding")
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
	tcpAlive := func(port int) bool { return true }

	ctx, cancel := context.WithCancel(context.Background())
	StartPruner(ctx, reg, 20*time.Millisecond, isAlive, tcpAlive, nil)
	time.Sleep(60 * time.Millisecond)
	cancel()
	time.Sleep(60 * time.Millisecond)
	callsAfterCancel := calls.Load()
	time.Sleep(60 * time.Millisecond)

	if calls.Load() != callsAfterCancel {
		t.Error("pruner continued running after context cancel")
	}
}

func TestPrunerTCPCheckPrunesUnreachable(t *testing.T) {
	reg := New()
	// Register a PID=0 server with old timestamp (past grace period)
	reg.Register(&ServerEntry{
		Name:         "ext/server",
		Repo:         "ext",
		Port:         9999,
		PID:          0,
		RegisteredAt: time.Now().Add(-60 * time.Second),
	})

	isAlive := func(pid int) bool { return true }
	tcpDead := func(port int) bool { return false }

	// Run pruneOnce 3 times to exceed failure threshold
	for i := 0; i < tcpFailThreshold; i++ {
		pruneOnce(reg, isAlive, tcpDead)
		if i < tcpFailThreshold-1 && reg.Get("ext/server") == nil {
			t.Fatalf("server pruned too early after %d failures", i+1)
		}
	}

	if reg.Get("ext/server") != nil {
		t.Error("unreachable PID=0 server should have been pruned after 3 failures")
	}
}

func TestPrunerTCPCheckGracePeriod(t *testing.T) {
	reg := New()
	// Register a PID=0 server with recent timestamp (within grace period)
	reg.Register(&ServerEntry{
		Name: "ext/new",
		Repo: "ext",
		Port: 9998,
		PID:  0,
	})

	isAlive := func(pid int) bool { return true }
	tcpDead := func(port int) bool { return false }

	// Run pruneOnce many times — server should survive due to grace period
	for i := 0; i < 10; i++ {
		pruneOnce(reg, isAlive, tcpDead)
	}

	if reg.Get("ext/new") == nil {
		t.Error("recently registered PID=0 server should NOT be pruned during grace period")
	}
}

func TestPrunerTCPCheckResetsOnSuccess(t *testing.T) {
	reg := New()
	reg.Register(&ServerEntry{
		Name:         "ext/flaky",
		Repo:         "ext",
		Port:         9997,
		PID:          0,
		RegisteredAt: time.Now().Add(-60 * time.Second),
	})

	isAlive := func(pid int) bool { return true }
	tcpDead := func(port int) bool { return false }
	tcpAlive := func(port int) bool { return true }

	// Fail twice
	pruneOnce(reg, isAlive, tcpDead)
	pruneOnce(reg, isAlive, tcpDead)

	// Succeed once — should reset counter
	pruneOnce(reg, isAlive, tcpAlive)

	// Fail twice more — should NOT be pruned (counter was reset)
	pruneOnce(reg, isAlive, tcpDead)
	pruneOnce(reg, isAlive, tcpDead)

	if reg.Get("ext/flaky") == nil {
		t.Error("server should survive after failure counter was reset by successful check")
	}

	// One more failure should trigger pruning (3 consecutive)
	pruneOnce(reg, isAlive, tcpDead)

	if reg.Get("ext/flaky") != nil {
		t.Error("server should be pruned after 3 consecutive failures")
	}
}

func TestPrunerSkipsClientOwnedServers(t *testing.T) {
	reg := New()
	reg.Register(&ServerEntry{
		Name:         "client/svc",
		Repo:         "client",
		Port:         9996,
		PID:          0,
		ClientID:     "some-client-id",
		RegisteredAt: time.Now().Add(-60 * time.Second),
	})

	isAlive := func(pid int) bool { return true }
	tcpDead := func(port int) bool { return false }

	// Run prune many times — client-owned server should never be TCP-checked or removed
	for i := 0; i < 10; i++ {
		pruneOnce(reg, isAlive, tcpDead)
	}

	if reg.Get("client/svc") == nil {
		t.Error("client-owned PID=0 server should NOT be pruned by registry pruner")
	}
}
