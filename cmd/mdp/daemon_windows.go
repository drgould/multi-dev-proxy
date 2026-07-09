//go:build windows

package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/process"
)

func startDaemon(controlPort int) error {
	dir := stateDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	logFile, err := os.OpenFile(logFilePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		logFile.Close()
		return fmt.Errorf("find executable: %w", err)
	}

	args := []string{exe, "--control-port", strconv.Itoa(controlPort)}
	for _, flag := range []string{"config", "host", "dashboard-port"} {
		if f := rootCmd.Flags().Lookup(flag); f != nil && f.Changed {
			args = append(args, "--"+flag, f.Value.String())
		}
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "_MDP_DAEMON=1")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("start daemon: %w", err)
	}
	logFile.Close()

	pid := cmd.Process.Pid
	if err := os.WriteFile(pidFilePath(), []byte(strconv.Itoa(pid)), 0644); err != nil {
		slog.Warn("failed to write PID file", "err", err)
	}

	if err := waitForHealth(controlPort, 5*time.Second); err != nil {
		slog.Warn("daemon may not have started correctly", "err", err)
	}

	msg := fmt.Sprintf("mdp orchestrator started (PID %d, ctrl :%d", pid, controlPort)
	if dashboardPort, ok := fetchDashboardPort(controlPort); ok && dashboardPort > 0 {
		msg += fmt.Sprintf(", dashboard http://localhost:%d", dashboardPort)
	}
	fmt.Println(msg + ")")
	return nil
}

func signalProcess(proc *os.Process) error {
	return proc.Kill()
}

// detachProcAttr returns the SysProcAttr that detaches a re-exec'd child into
// its own process group, so it survives the parent exiting.
func detachProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// signalDetachedRun stops a detached `mdp run` supervisor. Windows has no
// deliverable SIGTERM for a detached, console-less process, and killing only the
// supervisor would orphan its service children — so terminate the whole process
// tree (taskkill /T) instead.
func signalDetachedRun(pid int) error {
	return process.KillProcessGroup(pid, 10*time.Second)
}

func waitForHealth(controlPort int, timeout time.Duration) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/__mdp/health", controlPort))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("health check timed out after %s", timeout)
}
