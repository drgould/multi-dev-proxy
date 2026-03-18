package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/derekgould/multi-dev-proxy/internal/detect"
	"github.com/derekgould/multi-dev-proxy/internal/ports"
	"github.com/derekgould/multi-dev-proxy/internal/process"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a command through the proxy",
	RunE:  runRun,
	Args:  cobra.ArbitraryArgs,
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().IntP("proxy-port", "P", 3000, "Proxy port to connect to")
	runCmd.Flags().String("repo", "", "Repository name override")
	runCmd.Flags().String("name", "", "Server name override (default: repo/branch)")
	runCmd.Flags().String("port-range", "10000-60000", "Range of ports for proxied services")
}

func runRun(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no command specified — usage: mdp run <command> [args...]")
	}

	proxyPort, _ := cmd.Flags().GetInt("proxy-port")
	repoOverride, _ := cmd.Flags().GetString("repo")
	nameOverride, _ := cmd.Flags().GetString("name")
	portRangeStr, _ := cmd.Flags().GetString("port-range")

	if envPort := os.Getenv("MDP_PROXY_PORT"); envPort != "" && !cmd.Flags().Changed("proxy-port") {
		fmt.Sscanf(envPort, "%d", &proxyPort)
	}

	portRange, err := ports.ParseRange(portRangeStr)
	if err != nil {
		return fmt.Errorf("invalid --port-range: %w", err)
	}

	proxyURL := fmt.Sprintf("http://localhost:%d", proxyPort)
	proxyRunning := isProxyRunning(proxyURL)

	if !proxyRunning {
		slog.Info("no proxy detected, starting in solo mode", "proxy-port", proxyPort)
		return runSolo(args)
	}

	slog.Info("proxy detected, starting in proxy mode", "proxy-port", proxyPort)

	cwd, _ := os.Getwd()
	serverName := nameOverride
	if serverName == "" {
		repo := repoOverride
		if repo == "" {
			repo = detect.DetectRepo(cwd)
		}
		branch := detect.DetectBranch(cwd)
		serverName = detect.ServerName(repo, branch)
	}

	assignedPort, err := ports.FindFreePort(portRange, nil)
	if err != nil {
		return fmt.Errorf("find free port: %w", err)
	}

	mgr := process.New()
	ctx := context.Background()
	opts := process.RunOpts{
		ProxyURL:     proxyURL,
		ServerName:   serverName,
		AssignedPort: assignedPort,
		ProxyTimeout: 3 * time.Second,
	}

	code, err := mgr.Run(ctx, args, opts)
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

func isProxyRunning(proxyURL string) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(proxyURL + "/__mdp/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func runSolo(args []string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %q: %w", args[0], err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-sigCh:
		cmd.Process.Signal(syscall.SIGTERM)
		<-done
	case err := <-done:
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				os.Exit(ee.ExitCode())
			}
			return err
		}
	}
	return nil
}
