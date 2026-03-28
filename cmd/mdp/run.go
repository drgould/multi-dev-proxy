package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/detect"
	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
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
	runCmd.Flags().String("tls-cert", "", "TLS certificate file (forwarded to proxy for HTTPS)")
	runCmd.Flags().String("tls-key", "", "TLS key file (forwarded to proxy for HTTPS)")
	runCmd.Flags().Bool("auto-tls", false, "Auto-detect TLS certs from mkcert")
	runCmd.Flags().String("group", "", "Group name override (default: git branch)")
	runCmd.Flags().String("env", "PORT", "Environment variable name for the assigned port")
	runCmd.Flags().Int("control-port", 13100, "Orchestrator control port")
}

func runRun(cmd *cobra.Command, args []string) error {
	controlPort, _ := cmd.Flags().GetInt("control-port")
	groupFlag, _ := cmd.Flags().GetString("group")

	if len(args) == 0 {
		return runBatchMode(cmd, controlPort, groupFlag)
	}
	return runSingleMode(cmd, args, controlPort, groupFlag)
}

func runBatchMode(cmd *cobra.Command, controlPort int, groupFlag string) error {
	cwd, _ := os.Getwd()
	configPath := config.Find(cwd)
	if configPath == "" {
		return fmt.Errorf("no command specified and no mdp.yaml found — usage: mdp run [-- command]")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	group := groupFlag
	if group == "" {
		group = orchestrator.DetectGroup(filepath.Dir(configPath))
	}

	if !isOrchestratorRunning(controlPort) {
		return fmt.Errorf("no orchestrator running on port %d — start one with `mdp` or `mdp -d` first", controlPort)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	controlURL := fmt.Sprintf("http://127.0.0.1:%d", controlPort)

	portRange, _ := ports.ParseRange(cfg.PortRange)

	for name, svc := range cfg.Services {
		if svc.Command == "" && svc.Port == 0 {
			continue
		}

		svcGroup := svc.Group
		if svcGroup == "" {
			svcGroup = group
		}

		if len(svc.Ports) > 0 {
			if err := registerMultiPortBatch(client, controlURL, name, svc, svcGroup, portRange); err != nil {
				return fmt.Errorf("register multi-port service %q: %w", name, err)
			}
			continue
		}

		serverName := fmt.Sprintf("%s/%s", svcGroup, name)
		assignedPort := svc.Port
		if assignedPort == 0 {
			assignedPort, err = ports.FindFreePort(portRange, nil)
			if err != nil {
				return fmt.Errorf("find free port for %q: %w", name, err)
			}
		}

		if svc.Proxy > 0 {
			body, _ := json.Marshal(map[string]any{
				"name":      serverName,
				"port":      assignedPort,
				"proxyPort": svc.Proxy,
				"group":     svcGroup,
			})
			resp, err := client.Post(controlURL+"/__mdp/register", "application/json", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("register %q: %w", serverName, err)
			}
			resp.Body.Close()
		}

		if svc.Command != "" {
			env := []string{fmt.Sprintf("PORT=%d", assignedPort), "MDP=1"}
			for k, v := range svc.Env {
				if v != "auto" {
					env = append(env, k+"="+v)
				}
			}
			go runServiceProcess(name, svc.Command, svc.Dir, env)
		}
	}

	slog.Info("batch services started", "group", group)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	healthURL := fmt.Sprintf("http://127.0.0.1:%d/__mdp/health", controlPort)
	gone := watchHealth(healthURL)

	select {
	case <-sigCh:
	case <-gone:
		slog.Warn("orchestrator is no longer reachable, shutting down")
	}
	return nil
}

func registerMultiPortBatch(client *http.Client, controlURL, name string, svc config.ServiceConfig, group string, portRange ports.PortRange) error {
	portAssignments := make(map[string]int)
	for envName, value := range svc.Env {
		if value == "auto" {
			port, err := ports.FindFreePort(portRange, nil)
			if err != nil {
				return err
			}
			portAssignments[envName] = port
		}
	}

	for _, pm := range svc.Ports {
		port, ok := portAssignments[pm.Env]
		if !ok {
			continue
		}
		serviceName := pm.Name
		if serviceName == "" {
			serviceName = pm.Env
		}
		serverName := fmt.Sprintf("%s/%s", group, serviceName)
		body, _ := json.Marshal(map[string]any{
			"name":      serverName,
			"port":      port,
			"proxyPort": pm.Proxy,
			"group":     group,
		})
		resp, err := client.Post(controlURL+"/__mdp/register", "application/json", bytes.NewReader(body))
		if err != nil {
			return err
		}
		resp.Body.Close()
	}

	if svc.Command != "" {
		env := []string{"MDP=1"}
		for k, v := range svc.Env {
			if v == "auto" {
				if port, ok := portAssignments[k]; ok {
					env = append(env, fmt.Sprintf("%s=%d", k, port))
				}
			} else {
				env = append(env, k+"="+v)
			}
		}
		go runServiceProcess(name, svc.Command, svc.Dir, env)
	}

	return nil
}

var serviceColors = []string{
	"1;34",  // blue
	"1;32",  // green
	"1;35",  // purple
	"1;33",  // yellow
	"1;31",  // red
	"0;96",  // teal
	"1;95",  // pink
	"1;36",  // cyan
	"0;93",  // bright yellow
	"0;92",  // bright green
	"0;94",  // bright blue
	"0;91",  // bright red
	"0;95",  // bright magenta
	"0;33",  // dark yellow / orange
	"0;35",  // dark magenta
	"0;36",  // dark cyan
	"0;34",  // dark blue
	"0;32",  // dark green
	"38;5;208", // orange 256-color
	"38;5;171", // orchid
	"38;5;81",  // sky blue
	"38;5;214", // gold
	"38;5;157", // mint
	"38;5;204", // coral
}

var colorIndex int

func nextColor() string {
	c := serviceColors[colorIndex%len(serviceColors)]
	colorIndex++
	return c
}

type prefixWriter struct {
	prefix string
	out    *os.File
	buf    []byte
}

func newPrefixWriter(label string, color string, out *os.File) *prefixWriter {
	maxLen := 12
	if len(label) > maxLen {
		label = label[:maxLen]
	}
	prefix := fmt.Sprintf("\033[%sm%-*s\033[0m ", color, maxLen, label)
	return &prefixWriter{prefix: prefix, out: out}
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := w.buf[:idx]
		w.buf = w.buf[idx+1:]
		fmt.Fprintf(w.out, "%s%s\n", w.prefix, line)
	}
	return len(p), nil
}

func (w *prefixWriter) Flush() {
	if len(w.buf) > 0 {
		fmt.Fprintf(w.out, "%s%s\n", w.prefix, w.buf)
		w.buf = nil
	}
}

func runServiceProcess(name, command, dir string, env []string) {
	color := nextColor()
	pw := newPrefixWriter(name, color, os.Stdout)
	pwErr := newPrefixWriter(name, color, os.Stderr)

	parts := strings.Fields(command)
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Env = append(os.Environ(), env...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = pw
	cmd.Stderr = pwErr
	if err := cmd.Run(); err != nil {
		slog.Error("service process exited", "name", name, "command", command, "err", err)
	}
	pw.Flush()
	pwErr.Flush()
}

func runSingleMode(cmd *cobra.Command, args []string, controlPort int, groupFlag string) error {
	proxyPort, _ := cmd.Flags().GetInt("proxy-port")
	repoOverride, _ := cmd.Flags().GetString("repo")
	nameOverride, _ := cmd.Flags().GetString("name")
	portRangeStr, _ := cmd.Flags().GetString("port-range")
	tlsCert, _ := cmd.Flags().GetString("tls-cert")
	tlsKey, _ := cmd.Flags().GetString("tls-key")
	autoTLS, _ := cmd.Flags().GetBool("auto-tls")
	envVar, _ := cmd.Flags().GetString("env")

	if autoTLS && tlsCert == "" {
		tlsCert, tlsKey = detectMkcertCerts()
		if tlsCert != "" {
			slog.Info("auto-detected mkcert certs", "cert", tlsCert, "key", tlsKey)
		}
	}
	if (tlsCert != "") != (tlsKey != "") {
		return fmt.Errorf("both --tls-cert and --tls-key are required")
	}

	if envPort := os.Getenv("MDP_PROXY_PORT"); envPort != "" && !cmd.Flags().Changed("proxy-port") {
		fmt.Sscanf(envPort, "%d", &proxyPort)
	}

	portRange, err := ports.ParseRange(portRangeStr)
	if err != nil {
		return fmt.Errorf("invalid --port-range: %w", err)
	}

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

	group := groupFlag
	if group == "" {
		group = detect.DetectBranch(cwd)
	}

	assignedPort, err := ports.FindFreePort(portRange, nil)
	if err != nil {
		return fmt.Errorf("find free port: %w", err)
	}

	scheme := "http"
	if tlsCert != "" {
		scheme = "https"
	}

	if isOrchestratorRunning(controlPort) {
		client := &http.Client{Timeout: 5 * time.Second}
		body, _ := json.Marshal(map[string]any{
			"name":      serverName,
			"port":      assignedPort,
			"proxyPort": proxyPort,
			"group":     group,
			"scheme":    scheme,
		})
		resp, err := client.Post(
			fmt.Sprintf("http://127.0.0.1:%d/__mdp/register", controlPort),
			"application/json",
			bytes.NewReader(body),
		)
		if err != nil {
			return fmt.Errorf("register %q with orchestrator: %w", serverName, err)
		}
		resp.Body.Close()
		slog.Info("registered with orchestrator", "name", serverName, "proxy", proxyPort)
		healthURL := fmt.Sprintf("http://127.0.0.1:%d/__mdp/health", controlPort)
		return runSoloWithHealth(args, envVar, assignedPort, healthURL)
	} else {
		proxyURL, proxyRunning := detectProxy(proxyPort)
		if !proxyRunning {
			slog.Info("no proxy detected, starting in solo mode", "proxy-port", proxyPort)
			return runSolo(args, envVar, assignedPort)
		}
		slog.Info("proxy detected, starting in proxy mode", "url", proxyURL)

		mgr := process.New()
		ctx := context.Background()
		opts := process.RunOpts{
			ProxyURL:     proxyURL,
			ServerName:   serverName,
			AssignedPort: assignedPort,
			Scheme:       scheme,
			TLSCertPath:  tlsCert,
			TLSKeyPath:   tlsKey,
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
}

func isOrchestratorRunning(controlPort int) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/__mdp/health", controlPort))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func detectProxy(port int) (string, bool) {
	client := &http.Client{
		Timeout: 500 * time.Millisecond,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	for _, scheme := range []string{"https", "http"} {
		url := fmt.Sprintf("%s://localhost:%d", scheme, port)
		resp, err := client.Get(url + "/__mdp/health")
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return url, true
		}
	}
	return "", false
}

func detectMkcertCerts() (string, string) {
	out, err := exec.Command("mkcert", "-CAROOT").Output()
	if err != nil {
		return "", ""
	}
	caRoot := strings.TrimSpace(string(out))
	if caRoot == "" {
		return "", ""
	}
	certPath := filepath.Join(caRoot, "localhost.pem")
	keyPath := filepath.Join(caRoot, "localhost-key.pem")
	if _, err := os.Stat(certPath); err != nil {
		certPath = filepath.Join(caRoot, "rootCA.pem")
		if _, err := os.Stat(certPath); err != nil {
			return "", ""
		}
	}
	if _, err := os.Stat(keyPath); err != nil {
		return "", ""
	}
	return certPath, keyPath
}

func watchHealth(healthURL string) <-chan struct{} {
	gone := make(chan struct{})
	client := &http.Client{Timeout: 2 * time.Second}
	go func() {
		failures := 0
		for {
			time.Sleep(3 * time.Second)
			resp, err := client.Get(healthURL)
			if err != nil || resp.StatusCode != http.StatusOK {
				failures++
				if resp != nil {
					resp.Body.Close()
				}
				if failures >= 3 {
					close(gone)
					return
				}
				continue
			}
			resp.Body.Close()
			failures = 0
		}
	}()
	return gone
}

func runSoloWithHealth(args []string, envVar string, port int, healthURL string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%d", envVar, port), "MDP=1")

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %q: %w", args[0], err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	gone := watchHealth(healthURL)

	select {
	case <-sigCh:
		cmd.Process.Signal(syscall.SIGTERM)
		<-done
	case <-gone:
		slog.Warn("proxy is no longer reachable, shutting down")
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
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

func runSolo(args []string, envVar string, port int) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%d", envVar, port))

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
