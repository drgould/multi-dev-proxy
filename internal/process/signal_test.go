package process

import (
	"os"
	"testing"
)

func TestIsProcessAlive(t *testing.T) {
	// Current process should be alive
	if !IsProcessAlive(os.Getpid()) {
		t.Error("expected current process to be alive")
	}
	// PID 999999 should not exist (very unlikely to be real)
	if IsProcessAlive(999999) {
		t.Skip("PID 999999 happens to exist, skipping")
	}
}
