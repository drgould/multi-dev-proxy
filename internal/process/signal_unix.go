//go:build unix

package process

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// SetProcessGroup configures cmd to run in its own process group.
// This allows killing the entire process tree via KillProcessGroup.
func SetProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// KillProcessGroup sends SIGTERM to the process group with the given PID,
// waits up to timeout, then sends SIGKILL if still alive.
func KillProcessGroup(pid int, timeout time.Duration) error {
	// Send SIGTERM to the entire process group (negative PID = group)
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("SIGTERM to process group %d: %w", pid, err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !IsProcessAlive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Timeout — force kill
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	return nil
}

// IsProcessAlive returns true if a process with the given PID exists.
func IsProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}

// GracefulStop sends SIGTERM to a single process, waits for timeout, then SIGKILL.
func GracefulStop(pid int, timeout time.Duration) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("SIGTERM to pid %d: %w", pid, err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !IsProcessAlive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}
