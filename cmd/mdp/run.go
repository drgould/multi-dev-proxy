package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/detect"
	"github.com/derekgould/multi-dev-proxy/internal/envexpand"
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

	tlsCert, _ := cmd.Flags().GetString("tls-cert")
	tlsKey, _ := cmd.Flags().GetString("tls-key")
	autoTLS, _ := cmd.Flags().GetBool("auto-tls")

	if autoTLS && tlsCert == "" {
		tlsCert, tlsKey = detectMkcertCerts()
		if tlsCert != "" {
			slog.Info("auto-detected mkcert certs", "cert", tlsCert, "key", tlsKey)
		}
	}
	if (tlsCert != "") != (tlsKey != "") {
		return fmt.Errorf("both --tls-cert and --tls-key are required")
	}

	return runSingleMode(cmd, args, controlPort, groupFlag, tlsCert, tlsKey)
}

func runBatchMode(cmd *cobra.Command, controlPort int, groupFlag string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine working directory: %w", err)
	}
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
	clientID := generateClientID()

	portRange, err := ports.ParseRange(cfg.PortRange)
	if err != nil {
		return fmt.Errorf("invalid port range in config: %w", err)
	}

	bt := &batchTracker{}

	type alloc struct {
		name            string
		svc             config.ServiceConfig
		svcGroup        string
		assignedPort    int            // single-port only
		portAssignments map[string]int // multi-port only
	}
	var allocations []alloc
	portMap := envexpand.PortMap{}
	var assignedPorts []int
	for _, svc := range cfg.Services {
		if svc.Port > 0 {
			assignedPorts = append(assignedPorts, svc.Port)
		}
	}

	for name, svc := range cfg.Services {
		if svc.Command == "" && svc.Port == 0 {
			continue
		}
		svcGroup := svc.Group
		if svcGroup == "" {
			svcGroup = group
		}

		if len(svc.Ports) > 0 {
			portAssignments := make(map[string]int)
			for envName, value := range svc.Env {
				if value == "auto" {
					port, err := ports.FindFreePort(portRange, assignedPorts)
					if err != nil {
						return fmt.Errorf("find free port for %q.%s: %w", name, envName, err)
					}
					portAssignments[envName] = port
					assignedPorts = append(assignedPorts, port)
				}
			}
			svcPorts := make(map[string]int, len(portAssignments))
			for k, v := range portAssignments {
				svcPorts[k] = v
			}
			portMap[name] = svcPorts
			allocations = append(allocations, alloc{name, svc, svcGroup, 0, portAssignments})
			continue
		}

		assignedPort := svc.Port
		if assignedPort == 0 {
			assignedPort, err = ports.FindFreePort(portRange, assignedPorts)
			if err != nil {
				return fmt.Errorf("find free port for %q: %w", name, err)
			}
			assignedPorts = append(assignedPorts, assignedPort)
		}
		portMap[name] = map[string]int{"port": assignedPort, "PORT": assignedPort}
		allocations = append(allocations, alloc{name, svc, svcGroup, assignedPort, nil})
	}

	for _, a := range allocations {
		if len(a.svc.Ports) > 0 {
			if err := launchMultiPortBatch(client, controlURL, a.name, a.svc, a.svcGroup, a.portAssignments, portMap, bt, clientID); err != nil {
				return fmt.Errorf("launch multi-port service %q: %w", a.name, err)
			}
			continue
		}

		serverName := fmt.Sprintf("%s/%s", a.svcGroup, a.name)
		if a.svc.Proxy > 0 {
			regPayload := map[string]any{
				"name":      serverName,
				"port":      a.assignedPort,
				"proxyPort": a.svc.Proxy,
				"group":     a.svcGroup,
				"clientID":  clientID,
			}
			if a.svc.Scheme != "" {
				regPayload["scheme"] = a.svc.Scheme
			}
			if a.svc.TLSCert != "" {
				regPayload["tlsCertPath"] = a.svc.TLSCert
				regPayload["tlsKeyPath"] = a.svc.TLSKey
			}
			body, _ := json.Marshal(regPayload)
			resp, err := client.Post(controlURL+"/__mdp/register", "application/json", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("register %q: %w", serverName, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("register %q failed (status %d)", serverName, resp.StatusCode)
			}
		}

		if a.svc.Command != "" {
			env := []string{fmt.Sprintf("PORT=%d", a.assignedPort), "MDP=1"}
			for k, v := range a.svc.Env {
				if v == "auto" {
					continue
				}
				expanded, err := envexpand.Expand(v, portMap)
				if err != nil {
					return fmt.Errorf("service %q env %q: %w", a.name, k, err)
				}
				env = append(env, k+"="+expanded)
			}
			bt.wg.Add(1)
			go runServiceProcess(bt, a.name, a.svc.Command, a.svc.Dir, env, controlURL, serverName)
		}
	}

	slog.Info("batch services started", "group", group)

	hbCtx, hbCancel := context.WithCancel(context.Background())
	defer hbCancel()
	startHeartbeat(hbCtx, controlURL, clientID)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	gone := watchShutdown(controlURL)

	select {
	case <-sigCh:
	case <-gone:
		slog.Warn("orchestrator is shutting down")
	}

	hbCancel()

	bt.signalAll()
	waitDone := make(chan struct{})
	go func() { bt.wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		bt.killAll()
		<-waitDone
	}

	disconnectFromOrchestrator(controlURL, clientID)
	return nil
}

func launchMultiPortBatch(client *http.Client, controlURL, name string, svc config.ServiceConfig, group string, portAssignments map[string]int, portMap envexpand.PortMap, bt *batchTracker, clientID string) error {
	var registered []string
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
		regPayload := map[string]any{
			"name":      serverName,
			"port":      port,
			"proxyPort": pm.Proxy,
			"group":     group,
			"clientID":  clientID,
		}
		if svc.Scheme != "" {
			regPayload["scheme"] = svc.Scheme
		}
		if svc.TLSCert != "" {
			regPayload["tlsCertPath"] = svc.TLSCert
			regPayload["tlsKeyPath"] = svc.TLSKey
		}
		body, _ := json.Marshal(regPayload)
		resp, err := client.Post(controlURL+"/__mdp/register", "application/json", bytes.NewReader(body))
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("register %q failed (status %d)", serverName, resp.StatusCode)
		}
		registered = append(registered, serverName)
	}

	if svc.Command != "" {
		env := []string{"MDP=1"}
		for k, v := range svc.Env {
			if v == "auto" {
				if port, ok := portAssignments[k]; ok {
					env = append(env, fmt.Sprintf("%s=%d", k, port))
				}
				continue
			}
			expanded, err := envexpand.Expand(v, portMap)
			if err != nil {
				return fmt.Errorf("service %q env %q: %w", name, k, err)
			}
			env = append(env, k+"="+expanded)
		}
		namesCopy := make([]string, len(registered))
		copy(namesCopy, registered)
		bt.wg.Add(1)
		go runServiceProcess(bt, name, svc.Command, svc.Dir, env, controlURL, namesCopy...)
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

var colorMu sync.Mutex
var colorIndex int

func nextColor() string {
	colorMu.Lock()
	defer colorMu.Unlock()
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

type batchTracker struct {
	mu   sync.Mutex
	cmds []*exec.Cmd
	wg   sync.WaitGroup
}

func (bt *batchTracker) add(cmd *exec.Cmd) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.cmds = append(bt.cmds, cmd)
}

func (bt *batchTracker) signalAll() {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	for _, cmd := range bt.cmds {
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
		}
	}
}

func (bt *batchTracker) killAll() {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	for _, cmd := range bt.cmds {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
}

func runServiceProcess(bt *batchTracker, name, command, dir string, env []string, controlURL string, serverNames ...string) {
	defer bt.wg.Done()
	color := nextColor()
	pw := newPrefixWriter(name, color, os.Stdout)
	pwErr := newPrefixWriter(name, color, os.Stderr)

	parts := strings.Fields(command)
	if len(parts) == 0 {
		slog.Error("empty command", "name", name)
		return
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Env = append(os.Environ(), env...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = pw
	cmd.Stderr = pwErr

	bt.add(cmd)
	if err := cmd.Start(); err != nil {
		slog.Error("service process failed to start", "name", name, "command", command, "err", err)
		return
	}
	for _, sn := range serverNames {
		updatePIDWithOrchestrator(controlURL, sn, cmd.Process.Pid)
	}
	if err := cmd.Wait(); err != nil {
		slog.Error("service process exited", "name", name, "command", command, "err", err)
	}
	pw.Flush()
	pwErr.Flush()
}

func runSingleMode(cmd *cobra.Command, args []string, controlPort int, groupFlag, tlsCert, tlsKey string) error {
	proxyPort, _ := cmd.Flags().GetInt("proxy-port")
	repoOverride, _ := cmd.Flags().GetString("repo")
	nameOverride, _ := cmd.Flags().GetString("name")
	portRangeStr, _ := cmd.Flags().GetString("port-range")
	envVar, _ := cmd.Flags().GetString("env")

	if envPort := os.Getenv("MDP_PROXY_PORT"); envPort != "" && !cmd.Flags().Changed("proxy-port") {
		fmt.Sscanf(envPort, "%d", &proxyPort)
	}

	portRange, err := ports.ParseRange(portRangeStr)
	if err != nil {
		return fmt.Errorf("invalid --port-range: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine working directory: %w", err)
	}
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
		clientID := generateClientID()
		client := &http.Client{Timeout: 5 * time.Second}
		regPayload := map[string]any{
			"name":      serverName,
			"port":      assignedPort,
			"proxyPort": proxyPort,
			"group":     group,
			"scheme":    scheme,
			"clientID":  clientID,
		}
		if tlsCert != "" {
			regPayload["tlsCertPath"] = tlsCert
			regPayload["tlsKeyPath"] = tlsKey
		}
		body, _ := json.Marshal(regPayload)
		resp, err := client.Post(
			fmt.Sprintf("http://127.0.0.1:%d/__mdp/register", controlPort),
			"application/json",
			bytes.NewReader(body),
		)
		if err != nil {
			return fmt.Errorf("register %q with orchestrator: %w", serverName, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("register %q failed (status %d)", serverName, resp.StatusCode)
		}
		slog.Info("registered with orchestrator", "name", serverName, "proxy", proxyPort)
		controlURL := fmt.Sprintf("http://127.0.0.1:%d", controlPort)
		return runProxied(args, envVar, assignedPort, controlURL, serverName, clientID)
	} else {
		proxyURL, proxyRunning := detectProxy(proxyPort)
		if !proxyRunning {
			slog.Info("no proxy detected, starting in solo mode", "proxy-port", proxyPort)
			return runSolo(args)
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
		return "", ""
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

func runProxied(args []string, envVar string, port int, controlURL string, serverName string, clientID string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%d", envVar, port), "MDP=1")

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %q: %w", args[0], err)
	}
	updatePIDWithOrchestrator(controlURL, serverName, cmd.Process.Pid)

	hbCtx, hbCancel := context.WithCancel(context.Background())
	defer hbCancel()
	startHeartbeat(hbCtx, controlURL, clientID)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	gone := watchShutdown(controlURL)

	select {
	case <-sigCh:
		hbCancel()
		disconnectFromOrchestrator(controlURL, clientID)
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	case <-gone:
		slog.Warn("orchestrator is shutting down")
		hbCancel()
		disconnectFromOrchestrator(controlURL, clientID)
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	case err := <-done:
		hbCancel()
		disconnectFromOrchestrator(controlURL, clientID)
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				os.Exit(ee.ExitCode())
			}
			return err
		}
	}
	return nil
}

func updatePIDWithOrchestrator(controlURL, serverName string, pid int) {
	if controlURL == "" || serverName == "" || pid <= 0 {
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	body, _ := json.Marshal(map[string]int{"pid": pid})
	req, err := http.NewRequest(
		http.MethodPatch,
		controlURL+"/__mdp/register/"+url.PathEscape(serverName),
		bytes.NewReader(body),
	)
	if err != nil {
		slog.Warn("update PID: bad request URL", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("update PID with orchestrator failed", "err", err)
		return
	}
	resp.Body.Close()
	slog.Debug("updated PID with orchestrator", "name", serverName, "pid", pid)
}

func generateClientID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func startHeartbeat(ctx context.Context, controlURL, clientID string) {
	if controlURL == "" || clientID == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{"clientID": clientID})
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				req, err := http.NewRequestWithContext(ctx, http.MethodPost,
					controlURL+"/__mdp/heartbeat", bytes.NewReader(body))
				if err != nil {
					continue
				}
				req.Header.Set("Content-Type", "application/json")
				resp, err := client.Do(req)
				if err != nil {
					slog.Debug("heartbeat failed", "err", err)
					continue
				}
				resp.Body.Close()
			}
		}
	}()
}

func watchShutdown(controlURL string) <-chan struct{} {
	gone := make(chan struct{})
	go func() {
		client := &http.Client{Timeout: 0} // no timeout for long-poll
		failures := 0
		for {
			resp, err := client.Get(controlURL + "/__mdp/shutdown/watch")
			if resp != nil {
				resp.Body.Close()
			}
			if err == nil {
				// Intentional: any successful HTTP response from the watch endpoint
				// is treated as a shutdown signal for this client session.
				close(gone)
				return
			}
			failures++
			if failures >= 3 {
				// Orchestrator unreachable
				close(gone)
				return
			}
			time.Sleep(time.Second)
		}
	}()
	return gone
}

func disconnectFromOrchestrator(controlURL, clientID string) {
	if controlURL == "" || clientID == "" {
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	body, _ := json.Marshal(map[string]string{"clientID": clientID})
	req, err := http.NewRequest(http.MethodPost, controlURL+"/__mdp/disconnect", bytes.NewReader(body))
	if err != nil {
		slog.Debug("disconnect: bad request URL", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		slog.Debug("disconnect from orchestrator failed", "err", err)
		return
	}
	resp.Body.Close()
	slog.Info("disconnected from orchestrator", "clientID", clientID)
}

func deregisterFromOrchestrator(controlURL, serverName string) {
	if controlURL == "" || serverName == "" {
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(
		http.MethodDelete,
		controlURL+"/__mdp/register/"+url.PathEscape(serverName),
		nil,
	)
	if err != nil {
		slog.Debug("deregister: bad request URL", "err", err)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		slog.Debug("deregister from orchestrator failed", "err", err)
		return
	}
	resp.Body.Close()
	slog.Info("deregistered from orchestrator", "name", serverName)
}

func runSolo(args []string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

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
