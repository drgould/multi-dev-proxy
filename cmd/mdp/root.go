package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
	"github.com/derekgould/multi-dev-proxy/internal/tui"
)

var (
	version = "dev"
	commit  = "unknown"
)

var rootCmd = &cobra.Command{
	Use:     "mdp",
	Short:   "Multi-dev proxy — run multiple dev servers behind one port",
	Version: version + " (" + commit + ")",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.RunE = runOrchestrator
	rootCmd.Flags().Int("control-port", 13100, "Control API port")
	rootCmd.Flags().BoolP("daemon", "d", false, "Start daemon without TUI")
	rootCmd.Flags().Bool("stop", false, "Stop the background daemon")
	rootCmd.Flags().String("config", "", "Path to mdp.yaml (auto-detected if not set)")
	rootCmd.Flags().String("host", "0.0.0.0", "Host for proxy listeners")
}

func runOrchestrator(cmd *cobra.Command, args []string) error {
	controlPort, _ := cmd.Flags().GetInt("control-port")
	stop, _ := cmd.Flags().GetBool("stop")
	daemon, _ := cmd.Flags().GetBool("daemon")

	if stop {
		return runStop(controlPort)
	}

	if os.Getenv("_MDP_DAEMON") != "" {
		return runDaemonProcess(cmd, controlPort)
	}

	if !isOrchestratorRunning(controlPort) {
		if err := startDaemon(controlPort); err != nil {
			return err
		}
	}

	if daemon {
		return nil
	}

	return attachTUI(controlPort)
}

func runDaemonProcess(cmd *cobra.Command, controlPort int) error {
	configPath, _ := cmd.Flags().GetString("config")
	host, _ := cmd.Flags().GetString("host")

	var cfg *config.Config
	if configPath == "" {
		if cwd, err := os.Getwd(); err == nil {
			configPath = config.Find(cwd)
		}
	}
	if configPath != "" {
		var err error
		cfg, err = config.Load(configPath)
		if err != nil {
			slog.Warn("failed to load config", "path", configPath, "err", err)
		} else {
			slog.Info("loaded config", "path", configPath)
		}
	}

	orch := orchestrator.New(cfg, host)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctrlSrv, err := orchestrator.StartControlServer(orch, controlPort, cancel)
	if err != nil {
		return fmt.Errorf("start control API: %w", err)
	}

	slog.Info("mdp orchestrator started (daemon)", "control-port", controlPort)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
		slog.Info("shutting down")
	case <-ctx.Done():
	}

	orchCtx, orchCancel := context.WithTimeout(context.Background(), 5*time.Second)
	orch.Shutdown(orchCtx)
	orchCancel()

	ctrlCtx, ctrlCancel := context.WithTimeout(context.Background(), 2*time.Second)
	ctrlSrv.Shutdown(ctrlCtx)
	ctrlCancel()
	return nil
}

func attachTUI(controlPort int) error {
	remote := tui.NewRemoteBackend(controlPort)
	defer remote.Stop()

	tuiModel := tui.New(remote, controlPort)
	p := tea.NewProgram(tuiModel, tea.WithAltScreen(), tea.WithMouseAllMotion())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		p.Quit()
	}()

	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	if m, ok := finalModel.(tui.Model); ok {
		if m.DaemonLost {
			fmt.Println("mdp daemon is no longer running.")
			return nil
		}
		if !m.Detached {
			return runStop(controlPort)
		}
	}
	return nil
}

func runStop(controlPort int) error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(
		fmt.Sprintf("http://127.0.0.1:%d/__mdp/shutdown", controlPort),
		"application/json", nil,
	)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			fmt.Println("Shutdown signal sent via control API")
			cleanupPIDFile()
			return nil
		}
		slog.Warn("shutdown request returned unexpected status", "status", resp.StatusCode)
	}

	pidData, err := os.ReadFile(pidFilePath())
	if err != nil {
		return fmt.Errorf("no orchestrator found (no PID file and control API unreachable)")
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		return fmt.Errorf("invalid PID file: %w", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		cleanupPIDFile()
		return fmt.Errorf("process %d not found", pid)
	}

	if err := signalProcess(proc); err != nil {
		cleanupPIDFile()
		return fmt.Errorf("failed to signal process %d: %w", pid, err)
	}

	fmt.Printf("Sent signal to PID %d, waiting for exit...\n", pid)
	time.Sleep(2 * time.Second)

	cleanupPIDFile()
	fmt.Println("Orchestrator stopped")
	return nil
}

func cleanupPIDFile() {
	os.Remove(pidFilePath())
}
