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
func StartPruner(ctx context.Context, reg *Registry, interval time.Duration, isAlive func(pid int) bool, tcpCheck func(port int) bool) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneOnce(reg, isAlive, tcpCheck)
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
		if e.PID > 0 {
			if !isAlive(e.PID) {
				reg.Deregister(e.Name)
				slog.Info("pruned dead server", "name", e.Name, "pid", e.PID)
			}
			continue
		}

		// PID=0 with clientID: handled by session pruner, skip here
		if e.ClientID != "" {
			continue
		}

		// PID=0, no clientID: use TCP liveness check with grace period
		if time.Since(e.RegisteredAt) < tcpGracePeriod {
			continue
		}

		if tcpCheck(e.Port) {
			reg.ResetFailures(e.Name)
		} else {
			failures := reg.IncrementFailures(e.Name)
			if failures >= tcpFailThreshold {
				reg.Deregister(e.Name)
				slog.Info("pruned unreachable server", "name", e.Name, "port", e.Port, "failures", failures)
			}
		}
	}
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
