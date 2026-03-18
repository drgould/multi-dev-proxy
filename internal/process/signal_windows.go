//go:build windows

package process

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// SetProcessGroup on Windows sets CREATE_NEW_PROCESS_GROUP.
func SetProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags = syscall.CREATE_NEW_PROCESS_GROUP
}

// KillProcessGroup on Windows uses taskkill /F /T to kill process tree.
func KillProcessGroup(pid int, timeout time.Duration) error {
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid))
	return cmd.Run()
}

// IsProcessAlive returns true if a process with the given PID exists.
func IsProcessAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Windows, FindProcess always succeeds; OpenProcess with 0 access checks existence
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(p.Pid))
	if err != nil {
		return false
	}
	syscall.CloseHandle(handle)
	return true
}

// GracefulStop on Windows uses taskkill.
func GracefulStop(pid int, timeout time.Duration) error {
	return KillProcessGroup(pid, timeout)
}
