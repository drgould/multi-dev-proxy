//go:build unix

package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func stateDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mdp")
}

func pidFilePath() string {
	return filepath.Join(stateDir(), "orchestrator.pid")
}

func logFilePath() string {
	return filepath.Join(stateDir(), "orchestrator.log")
}

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
	for _, flag := range []string{"no-tls", "tls-cert", "tls-key", "config", "host"} {
		if f := rootCmd.Flags().Lookup(flag); f != nil && f.Changed {
			args = append(args, "--"+flag, f.Value.String())
		}
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "_MDP_DAEMON=1")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

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

	fmt.Printf("mdp orchestrator started (PID %d, ctrl :%d)\n", pid, controlPort)
	return nil
}

func signalProcess(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
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
