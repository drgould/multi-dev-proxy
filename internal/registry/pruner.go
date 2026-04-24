package registry

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"
)

// StartPruner launches a goroutine that removes dead servers from the registry.
// isAlive is injected for testability — pass process.IsProcessAlive in production.
// tcpCheck is used as a fallback for servers with no PID (externally managed).
// onTick, if non-nil, is invoked after each prune pass (useful for reacting to
// state changes such as an empty registry).
func StartPruner(ctx context.Context, reg *Registry, interval time.Duration, isAlive func(pid int) bool, tcpCheck func(port int) bool, onTick func()) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneOnce(reg, isAlive, tcpCheck)
				if onTick != nil {
					onTick()
				}
			}
		}
	}()
}

const (
	tcpGracePeriod   = 30 * time.Second
	tcpFailThreshold = 3
)

func pruneOnce(reg *Registry, isAlive func(int) bool, tcpCheck func(int) bool) {
	for _, e := range reg.List() {
		// Pure session-owned entries (no PID to track) are the session
		// pruner's responsibility.
		if e.PID == 0 && e.ClientID != "" {
			continue
		}

		// If the process is still running, the entry is live regardless of
		// whether the port is responsive yet (the service may still be
		// starting). Reset failures so a later check starts clean.
		if e.PID > 0 && isAlive(e.PID) {
			reg.ResetFailures(e.Name)
			continue
		}

		// Process is gone (or was never tracked). Fall back to the liveness
		// probe. Grace period prevents flapping on just-registered entries
		// and gives detached processes time to hand off to their child
		// (e.g. `docker compose up -d` exiting while containers come up).
		if time.Since(e.RegisteredAt) < tcpGracePeriod {
			continue
		}

		if probe(e, tcpCheck) {
			// Port/health check passes — keep the entry. For detached
			// services this is what keeps the proxy alive after the
			// foreground process has exited.
			reg.ResetFailures(e.Name)
			continue
		}

		failures := reg.IncrementFailures(e.Name)
		if failures >= tcpFailThreshold {
			reg.Deregister(e.Name)
			slog.Info("pruned unreachable server", "name", e.Name, "pid", e.PID, "port", e.Port, "failures", failures)
		}
	}
}

func probe(e ServerEntry, tcpCheck func(int) bool) bool {
	if e.HealthCheck != nil {
		return e.HealthCheck()
	}
	return tcpCheck(e.Port)
}

// TCPCheck attempts a TCP connection to localhost on the given port.
func TCPCheck(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
