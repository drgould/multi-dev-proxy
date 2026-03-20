package registry

import (
	"context"
	"log/slog"
	"time"
)

// StartPruner launches a goroutine that removes dead servers from the registry.
// isAlive is injected for testability — pass process.IsProcessAlive in production.
func StartPruner(ctx context.Context, reg *Registry, interval time.Duration, isAlive func(pid int) bool) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneOnce(reg, isAlive)
			}
		}
	}()
}

func pruneOnce(reg *Registry, isAlive func(int) bool) {
	for _, e := range reg.List() {
		if e.PID <= 0 {
			continue // no PID — externally managed, skip liveness check
		}
		if !isAlive(e.PID) {
			reg.Deregister(e.Name)
			slog.Info("pruned dead server", "name", e.Name, "pid", e.PID)
		}
	}
}
