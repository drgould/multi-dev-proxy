package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/depwait"
	"github.com/derekgould/multi-dev-proxy/internal/detect"
	"github.com/derekgould/multi-dev-proxy/internal/envexpand"
	"github.com/derekgould/multi-dev-proxy/internal/envexport"
	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
	"github.com/derekgould/multi-dev-proxy/internal/ports"
	"github.com/derekgould/multi-dev-proxy/internal/process"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

// batchRuntime bundles the readiness knobs threaded through launchBatchService.
// Keeping these per-call (not package-level) avoids data races between test
// cleanups and in-flight launch goroutines.
type batchRuntime struct {
	readyTimeout time.Duration
	readyPoll    time.Duration
	tcpCheck     func(int) bool
}

func defaultBatchRuntime() batchRuntime {
	return batchRuntime{
		readyTimeout: 60 * time.Second,
		readyPoll:    200 * time.Millisecond,
		tcpCheck:     registry.TCPCheck,
	}
}

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
	runCmd.Flags().String("log-split", "", `Demultiplex combined-stream logs. Values: "compose" (docker-compose format) or "regex:<pattern>" with named captures 'name' and 'msg'.`)
	runCmd.Flags().StringArray("link", nil, "Override the lookup group for cross-repo @<repo>.* env refs: repo=group (repeatable, last-wins per repo). Used when a peer service runs in a different group than the caller (e.g. backend on main, frontend on a feature branch).")
}

// parseLinks converts repeated `--link repo=group` values into a map. Empty
// repo or group is rejected with a clear error. Last value wins on duplicate
// repo keys.
func parseLinks(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, v := range values {
		idx := strings.IndexByte(v, '=')
		if idx <= 0 || idx == len(v)-1 {
			return nil, fmt.Errorf("--link %q must be in form repo=group", v)
		}
		repo := strings.TrimSpace(v[:idx])
		group := strings.TrimSpace(v[idx+1:])
		if repo == "" || group == "" {
			return nil, fmt.Errorf("--link %q must be in form repo=group", v)
		}
		out[repo] = group
	}
	return out, nil
}

func runRun(cmd *cobra.Command, args []string) error {
	controlPort, _ := cmd.Flags().GetInt("control-port")
	groupFlag, _ := cmd.Flags().GetString("group")
	linkValues, _ := cmd.Flags().GetStringArray("link")
	linkMap, err := parseLinks(linkValues)
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return runBatchMode(cmd, controlPort, groupFlag, linkMap)
	}

	tlsCert, _ := cmd.Flags().GetString("tls-cert")
	tlsKey, _ := cmd.Flags().GetString("tls-key")
	autoTLS, _ := cmd.Flags().GetBool("auto-tls")
	logSplitFlag, _ := cmd.Flags().GetString("log-split")

	if autoTLS && tlsCert == "" {
		tlsCert, tlsKey = detectMkcertCerts()
		if tlsCert != "" {
			slog.Info("auto-detected mkcert certs", "cert", tlsCert, "key", tlsKey)
		}
	}
	if (tlsCert != "") != (tlsKey != "") {
		return fmt.Errorf("both --tls-cert and --tls-key are required")
	}
	logSplit, err := config.ParseLogSplitFlag(logSplitFlag)
	if err != nil {
		return err
	}

	return runSingleMode(cmd, args, controlPort, groupFlag, tlsCert, tlsKey, logSplit)
}

func runBatchMode(cmd *cobra.Command, controlPort int, groupFlag string, linkMap map[string]string) error {
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

	var allocations []batchAlloc
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
			envProtocols := svc.EnvProtocols()
			portAssignments := make(map[string]int)
			for envName, value := range svc.Env {
				if value.Ref == "" && value.Value == "auto" {
					finder := ports.FindFreePort
					if envProtocols[envName] == "udp" {
						finder = ports.FindFreeUDPPort
					}
					port, err := finder(portRange, assignedPorts)
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
			allocations = append(allocations, batchAlloc{name: name, svc: svc, svcGroup: svcGroup, portAssignments: portAssignments, portProtocols: envProtocols})
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
		allocations = append(allocations, batchAlloc{name: name, svc: svc, svcGroup: svcGroup, assignedPort: assignedPort})
	}

	repo := detect.DetectRepo(filepath.Dir(configPath))

	// Build a resolver per allocation so that services with a `group:` override
	// query the orchestrator under their own group, not the workspace's
	// top-level group. The watcher already keys on a.svcGroup, so a mismatched
	// startup resolver would silently produce stale env values that never
	// self-correct.
	allocResolvers := make([]envexpand.Resolver, len(allocations))
	for i := range allocations {
		allocResolvers[i] = newPeerResolver(client, controlURL, allocations[i].svcGroup, linkMap)
	}
	globalResolver := newPeerResolver(client, controlURL, group, linkMap)

	if err := exportBatchEnvFiles(cfg, allocations, portMap, allocResolvers, globalResolver); err != nil {
		return err
	}

	batchCtx, batchCancel := context.WithCancel(context.Background())
	defer batchCancel()

	names := make([]string, 0, len(allocations))
	for _, a := range allocations {
		names = append(names, a.name)
	}
	states := depwait.NewStates(names)

	rt := defaultBatchRuntime()
	for i := range allocations {
		bt.wg.Add(1)
		go launchBatchService(batchCtx, bt, client, controlURL, clientID, repo, &allocations[i], states, rt, portMap, allocResolvers[i], linkMap)
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

	// Cancel the batch context so any launch goroutines still blocked in
	// depwait.Wait or depwait.TCPReady return immediately instead of holding
	// shutdown hostage for the full per-dep readiness timeout.
	batchCancel()
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

// batchAlloc holds a service's resolved port allocations prior to launch.
type batchAlloc struct {
	name            string
	svc             config.ServiceConfig
	svcGroup        string
	assignedPort    int               // single-port only
	portAssignments map[string]int    // multi-port only
	portProtocols   map[string]string // env → "tcp"/"udp"; only populated for multi-port
	env             []string          // populated by exportBatchEnvFiles before fan-out
}

// launchBatchService is the per-service batch-mode launcher: it waits for the
// service's declared dependencies, registers upstreams with the orchestrator,
// starts the process, polls TCP readiness, and signals its depwait.State.
// Runs inside bt.wg so shutdown blocks until each service's cmd exits.
//
// If the service references cross-repo peers via @<repo>.<svc>... refs, this
// function also supervises peer state and restarts the cmd whenever a watched
// peer's port or env value changes.
func launchBatchService(
	ctx context.Context,
	bt *batchTracker,
	client *http.Client,
	controlURL, clientID, repo string,
	a *batchAlloc,
	states map[string]*depwait.State,
	rt batchRuntime,
	portMap envexpand.PortMap,
	resolver envexpand.Resolver,
	linkMap map[string]string,
) {
	defer bt.wg.Done()
	state := states[a.name]
	// state.Done must close when readiness is determined — not when the
	// process exits — so dependents unblock as soon as this service is ready.
	var readyOnce sync.Once
	signalReady := func() { readyOnce.Do(func() { close(state.Done) }) }
	defer signalReady()

	if err := depwait.Wait(ctx, states, a.svc.DependsOn, rt.readyTimeout); err != nil {
		slog.Error("service aborted waiting on deps", "name", a.name, "err", err)
		state.Err = err
		return
	}

	type regEntry struct {
		serverName string
		port       int
		proxyPort  int
	}
	var registrations []regEntry
	var probePorts []int
	if len(a.svc.Ports) > 0 {
		for _, pm := range a.svc.Ports {
			port, ok := a.portAssignments[pm.Env]
			if !ok {
				continue
			}
			if pm.Proxy > 0 {
				serviceName := pm.Name
				if serviceName == "" {
					serviceName = pm.Env
				}
				registrations = append(registrations, regEntry{
					serverName: fmt.Sprintf("%s/%s", a.svcGroup, serviceName),
					port:       port,
					proxyPort:  pm.Proxy,
				})
			}
			if pm.Protocol == "udp" {
				continue
			}
			probePorts = append(probePorts, port)
		}
	} else {
		serverName := fmt.Sprintf("%s/%s", a.svcGroup, a.name)
		if a.svc.Proxy > 0 {
			registrations = append(registrations, regEntry{
				serverName: serverName,
				port:       a.assignedPort,
				proxyPort:  a.svc.Proxy,
			})
		}
		if a.assignedPort > 0 {
			probePorts = append(probePorts, a.assignedPort)
		}
	}

	registerAll := func() ([]string, error) {
		registered := make([]string, 0, len(registrations))
		envMap := envSliceToMap(a.env)
		for _, r := range registrations {
			payload := map[string]any{
				"name":      r.serverName,
				"port":      r.port,
				"proxyPort": r.proxyPort,
				"group":     a.svcGroup,
				"repo":      repo,
				"clientID":  clientID,
				"env":       envMap,
			}
			if a.svc.Scheme != "" {
				payload["scheme"] = a.svc.Scheme
			}
			if a.svc.TLSCert != "" {
				payload["tlsCertPath"] = a.svc.TLSCert
				payload["tlsKeyPath"] = a.svc.TLSKey
			}
			body, _ := json.Marshal(payload)
			resp, err := client.Post(controlURL+"/__mdp/register", "application/json", bytes.NewReader(body))
			if err != nil {
				return registered, err
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return registered, fmt.Errorf("register %q failed (status %d)", r.serverName, resp.StatusCode)
			}
			registered = append(registered, r.serverName)
		}
		return registered, nil
	}
	deregisterAll := func(names []string) {
		for _, sn := range names {
			deregisterFromOrchestrator(controlURL, sn)
		}
	}

	if a.svc.Command == "" {
		// External upstream (mdp isn't starting a process). Register upfront
		// and probe TCP so dependents only unblock once the externally-managed
		// service is actually reachable.
		registered, err := registerAll()
		if err != nil {
			slog.Error("register failed", "name", a.name, "err", err)
			state.Err = err
			deregisterAll(registered)
			return
		}
		if len(probePorts) > 0 {
			if err := depwait.TCPReady(ctx, probePorts, rt.readyTimeout, rt.readyPoll, rt.tcpCheck); err != nil {
				slog.Error("external service not ready", "name", a.name, "err", err)
				state.Err = err
			}
		}
		return
	}

	env := a.env

	color := nextColor()
	pw := newPrefixWriter(a.name, color, os.Stdout)
	pwErr := newPrefixWriter(a.name, color, os.Stderr)

	// If log_split is enabled, demultiplex combined output into per-sub-service
	// colored lanes. Hooks keep the outer service prefix — only the main
	// command's stdout/stderr get wrapped.
	var stdoutW, stderrW io.Writer = pw, pwErr
	splitter, err := newLogSplitterFromConfig(a.svc.LogSplit, a.name)
	if err != nil {
		slog.Error("invalid log_split config", "name", a.name, "err", err)
		state.Err = err
		return
	}
	if splitter != nil {
		stdoutW = newSplitWriter(pw, os.Stdout, splitter)
		stderrW = newSplitWriter(pwErr, os.Stderr, splitter)
	}

	// Run setup before registering so routing never points at a service
	// whose setup is still running (or has failed).
	for i, raw := range a.svc.Setup {
		parts, err := orchestrator.SplitHookArgs(raw)
		if err != nil {
			slog.Error("setup hook parse failed", "name", a.name, "step", i+1, "cmd", raw, "err", err)
			state.Err = err
			return
		}
		if len(parts) == 0 {
			continue
		}
		slog.Info("service hook", "name", a.name, "phase", "setup", "step", i+1, "cmd", raw)
		h := exec.CommandContext(ctx, parts[0], parts[1:]...)
		h.Env = append(os.Environ(), env...)
		if a.svc.Dir != "" {
			h.Dir = a.svc.Dir
		}
		h.Stdout = pw
		h.Stderr = pwErr
		if err := h.Run(); err != nil {
			slog.Error("setup hook failed", "name", a.name, "step", i+1, "cmd", raw, "err", err)
			state.Err = err
			return
		}
	}

	registered, err := registerAll()
	if err != nil {
		slog.Error("register failed", "name", a.name, "err", err)
		state.Err = err
		deregisterAll(registered)
		return
	}

	cmd, err := startBatchCommand(bt, a.svc.Command, a.svc.Dir, env, stdoutW, stderrW)
	if err != nil {
		slog.Error("service process failed to start", "name", a.name, "command", a.svc.Command, "err", err)
		state.Err = err
		deregisterAll(registered)
		return
	}
	for _, sn := range registered {
		updatePIDWithOrchestrator(controlURL, sn, cmd.Process.Pid)
	}

	// Poll TCP readiness so dependents only unblock once this service is
	// actually accepting connections.
	if len(probePorts) > 0 {
		if err := depwait.TCPReady(ctx, probePorts, rt.readyTimeout, rt.readyPoll, rt.tcpCheck); err != nil {
			slog.Error("service not ready", "name", a.name, "err", err)
			state.Err = err
			// Fall through to wait for the cmd — leave it running so logs
			// still stream and shutdown can clean it up normally.
		}
	}

	// Signal dependents now; the rest of this goroutine just drains the cmd
	// and (if this service has cross-repo peers) restarts it on peer change.
	signalReady()

	superviseProcess(ctx, cmd, bt, client, controlURL, a, registered, registerAll, portMap, resolver, linkMap, pw, pwErr)

	for i, raw := range a.svc.Shutdown {
		parts, err := orchestrator.SplitHookArgs(raw)
		if err != nil {
			slog.Warn("shutdown hook parse failed", "name", a.name, "step", i+1, "cmd", raw, "err", err)
			continue
		}
		if len(parts) == 0 {
			continue
		}
		slog.Info("service hook", "name", a.name, "phase", "shutdown", "step", i+1, "cmd", raw)
		hCtx, hCancel := context.WithTimeout(context.Background(), shutdownHookTimeout)
		h := exec.CommandContext(hCtx, parts[0], parts[1:]...)
		h.Env = append(os.Environ(), a.env...)
		if a.svc.Dir != "" {
			h.Dir = a.svc.Dir
		}
		h.Stdout = pw
		h.Stderr = pwErr
		if err := h.Run(); err != nil {
			slog.Warn("shutdown hook failed", "name", a.name, "step", i+1, "cmd", raw, "err", err)
		}
		hCancel()
	}

	if sw, ok := stdoutW.(*splitWriter); ok {
		sw.Flush()
	}
	if sw, ok := stderrW.(*splitWriter); ok {
		sw.Flush()
	}
	pw.Flush()
	pwErr.Flush()
}

const shutdownHookTimeout = 30 * time.Second

// peerWatchInterval is how often supervisor goroutines poll the orchestrator
// for cross-repo peer state changes. Package-level so tests can shorten it.
var peerWatchInterval = 2 * time.Second

// superviseProcess waits for cmd to exit, restarting it whenever a watched
// cross-repo peer's port or env value changes. Returns when the cmd exits
// without a peer-triggered restart, or when ctx is cancelled.
func superviseProcess(
	ctx context.Context,
	cmd *exec.Cmd,
	bt *batchTracker,
	client *http.Client,
	controlURL string,
	a *batchAlloc,
	registered []string,
	registerAll func() ([]string, error),
	portMap envexpand.PortMap,
	resolver envexpand.Resolver,
	linkMap map[string]string,
	pw, pwErr *prefixWriter,
) {
	peerRefs := extractPeerRefs(a.svc)
	// Seed peerRefs with the values we resolved at startup so the watcher
	// only fires on a *change*, not on first sight.
	if len(peerRefs) > 0 {
		_, peerRefs = refreshPeerRefs(client, controlURL, a.svcGroup, linkMap, peerRefs)
	}

	for {
		watchCtx, watchCancel := context.WithCancel(ctx)
		peerCh := make(chan []peerRef, 1)
		if len(peerRefs) > 0 {
			go watchPeerRefs(watchCtx, client, controlURL, a.svcGroup, linkMap, peerRefs, peerWatchInterval, peerCh)
		}

		cmdExit := make(chan error, 1)
		go func(c *exec.Cmd) { cmdExit <- c.Wait() }(cmd)

		var restart bool
		select {
		case <-ctx.Done():
			watchCancel()
			cmd.Process.Signal(syscall.SIGTERM)
			<-cmdExit
		case waitErr := <-cmdExit:
			watchCancel()
			if waitErr != nil {
				slog.Error("service process exited", "name", a.name, "command", a.svc.Command, "err", waitErr)
			}
		case updated := <-peerCh:
			watchCancel()
			slog.Info("peer changed; restarting service", "name", a.name)
			cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-cmdExit:
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
				<-cmdExit
			}
			peerRefs = updated
			restart = true
		}

		if !restart {
			return
		}

		newEnv, err := buildBatchEnv(*a, portMap, resolver)
		if err != nil {
			slog.Error("rebuild env failed; not restarting", "name", a.name, "err", err)
			return
		}
		a.env = newEnv
		if a.svc.EnvFile != "" {
			if err := envexport.WritePerService(a.svc.EnvFile, newEnv); err != nil {
				slog.Warn("rewrite env file failed", "name", a.name, "err", err)
			}
		}
		if _, err := registerAll(); err != nil {
			slog.Error("re-register failed; not restarting", "name", a.name, "err", err)
			return
		}
		newCmd, err := startBatchCommand(bt, a.svc.Command, a.svc.Dir, newEnv, pw, pwErr)
		if err != nil {
			slog.Error("restart failed", "name", a.name, "err", err)
			return
		}
		for _, sn := range registered {
			updatePIDWithOrchestrator(controlURL, sn, newCmd.Process.Pid)
		}
		cmd = newCmd
	}
}

// buildBatchEnv builds the environment for a batch-mode service. resolver is
// invoked for cross-repo @-references; pass nil to forbid them (any @ ref
// without an inline default will then error).
func buildBatchEnv(a batchAlloc, portMap envexpand.PortMap, resolver envexpand.Resolver) ([]string, error) {
	env := []string{"MDP=1"}
	if len(a.svc.Ports) == 0 && a.assignedPort > 0 {
		env = append(env, fmt.Sprintf("PORT=%d", a.assignedPort))
	}
	for k, entry := range a.svc.Env {
		if entry.Ref != "" {
			val, err := envexpand.LookupRefWith(entry.Ref, entry.DefaultValue(), entry.HasDefault(), portMap, nil, resolver)
			if err != nil {
				if entry.HasDefault() {
					env = append(env, k+"="+entry.DefaultValue())
					continue
				}
				if envexpand.IsCrossRepoBareRef(entry.Ref) {
					// Cross-repo peer not running and no default — omit
					// (graceful degradation per user spec).
					slog.Warn("peer ref unresolved; omitting env var", "service", a.name, "key", k, "ref", entry.Ref)
					continue
				}
				return nil, fmt.Errorf("env %s.%s: %w", a.name, k, err)
			}
			env = append(env, k+"="+val)
			continue
		}
		if entry.Value == "auto" {
			if port, ok := a.portAssignments[k]; ok {
				env = append(env, fmt.Sprintf("%s=%d", k, port))
			}
			continue
		}
		expanded, err := envexpand.ExpandWith(entry.Value, portMap, nil, resolver)
		if err != nil {
			return nil, fmt.Errorf("env expansion for %s.%s: %w", a.name, k, err)
		}
		env = append(env, k+"="+expanded)
	}
	return env, nil
}

// exportBatchEnvFiles builds each allocation's env up front and writes the
// global + per-service env files before any service launches. Env is stored
// on allocations[i].env for the launch goroutine to consume.
//
// allocResolvers[i] is the cross-repo @-ref resolver for allocations[i] (built
// from that allocation's own group so a per-service `group:` override resolves
// against the right peers). globalResolver is used for global env entries,
// which sit at the workspace level and use the top-level group. Either may be
// nil to disable cross-repo resolution.
func exportBatchEnvFiles(cfg *config.Config, allocations []batchAlloc, portMap envexpand.PortMap, allocResolvers []envexpand.Resolver, globalResolver envexpand.Resolver) error {
	envMap := envexpand.EnvMap{}
	for i, a := range allocations {
		var r envexpand.Resolver
		if i < len(allocResolvers) {
			r = allocResolvers[i]
		}
		env, err := buildBatchEnv(a, portMap, r)
		if err != nil {
			return err
		}
		allocations[i].env = env
		envMap[a.name] = envSliceToMap(env)
	}
	if cfg.Global.EnvFile != "" {
		if err := envexport.WriteGlobalWith(cfg.Global.EnvFile, cfg.Global.Env, portMap, envMap, globalResolver); err != nil {
			return fmt.Errorf("write global env file: %w", err)
		}
	}
	for _, a := range allocations {
		if a.svc.EnvFile == "" {
			continue
		}
		if err := envexport.WritePerService(a.svc.EnvFile, a.env); err != nil {
			return fmt.Errorf("write env file for %s: %w", a.name, err)
		}
	}
	return nil
}

func envSliceToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

// startBatchCommand starts the service process and registers it with bt.
// Returns the started *exec.Cmd; the caller is responsible for cmd.Wait().
func startBatchCommand(bt *batchTracker, command, dir string, env []string, stdout, stderr io.Writer) (*exec.Cmd, error) {
	parts, err := orchestrator.SplitHookArgs(command)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Env = append(os.Environ(), env...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	bt.add(cmd)
	return cmd, nil
}

var serviceColors = []string{
	"1;34",     // blue
	"1;32",     // green
	"1;35",     // purple
	"1;33",     // yellow
	"1;31",     // red
	"0;96",     // teal
	"1;95",     // pink
	"1;36",     // cyan
	"0;93",     // bright yellow
	"0;92",     // bright green
	"0;94",     // bright blue
	"0;91",     // bright red
	"0;95",     // bright magenta
	"0;33",     // dark yellow / orange
	"0;35",     // dark magenta
	"0;36",     // dark cyan
	"0;34",     // dark blue
	"0;32",     // dark green
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
	out    io.Writer
	buf    []byte
}

// prefixMinWidth is the minimum padded width of a service-name prefix. Labels
// shorter than this are right-padded for alignment; longer labels expand past
// it rather than being truncated — truncation hides real service names (e.g.
// `api-feature-a` becoming `api-feature-`).
const prefixMinWidth = 12

func newPrefixWriter(label string, color string, out io.Writer) *prefixWriter {
	prefix := fmt.Sprintf("\033[%sm%-*s\033[0m ", color, prefixMinWidth, label)
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

// ansiSeqRe matches ANSI CSI escape sequences (SGR colors, cursor codes,
// etc.). Stripped from the pipe-prefix portion of a line before name matching
// so colorized compose output (TTY / `--ansi=always`) still matches.
var ansiSeqRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// composeNameRe matches the bare container-name portion of a compose prefix
// after ANSI codes have been stripped. Compose pads the name with spaces to
// align the pipe across containers, so trailing whitespace is expected.
var composeNameRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*)\s+$`)

// prefixParser extracts a sub-label and message-start index from a line.
// Returns ok=false when the line doesn't match — callers route non-matching
// lines to the outer prefix.
type prefixParser func(line []byte) (name string, msgStart int, ok bool)

// logSplitter holds the parser and a shared name→color map so a service's
// stdout and stderr splitWriters keep the same color for a given sub-label.
// outerLabel (when non-empty) is prepended to sub-lane labels as
// "<outer>/<sub>" so readers can see which service the inner lane belongs to.
type logSplitter struct {
	parse      prefixParser
	outerLabel string
	mu         sync.Mutex
	colors     map[string]string
}

func newLogSplitter(parse prefixParser, outerLabel string) *logSplitter {
	return &logSplitter{parse: parse, outerLabel: outerLabel, colors: map[string]string{}}
}

// newLogSplitterFromConfig builds a splitter for the given log_split config.
// Returns nil when the config disables splitting. outerLabel is the service
// name to prepend to sub-lane labels; pass "" for ad-hoc commands with no
// surrounding service context.
func newLogSplitterFromConfig(cfg config.LogSplitConfig, outerLabel string) (*logSplitter, error) {
	switch cfg.Mode {
	case "":
		return nil, nil
	case "compose":
		return newLogSplitter(parseComposePrefix, outerLabel), nil
	case "regex":
		re, err := regexp.Compile(cfg.Regex)
		if err != nil {
			return nil, fmt.Errorf("log_split: invalid regex: %w", err)
		}
		nameIdx := re.SubexpIndex("name")
		msgIdx := re.SubexpIndex("msg")
		if nameIdx < 0 || msgIdx < 0 {
			return nil, fmt.Errorf("log_split: regex must contain named captures `name` and `msg`")
		}
		parse := func(line []byte) (string, int, bool) {
			m := re.FindSubmatchIndex(line)
			if m == nil {
				return "", 0, false
			}
			nameStart, nameEnd := m[2*nameIdx], m[2*nameIdx+1]
			if nameStart < 0 {
				return "", 0, false
			}
			msgStart := m[2*msgIdx]
			if msgStart < 0 {
				msgStart = m[1] // end of overall match
			}
			return string(line[nameStart:nameEnd]), msgStart, true
		}
		return newLogSplitter(parse, outerLabel), nil
	default:
		return nil, fmt.Errorf("log_split: unknown mode %q", cfg.Mode)
	}
}

func (s *logSplitter) colorFor(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.colors[name]; ok {
		return c
	}
	c := nextColor()
	s.colors[name] = c
	return c
}

// splitWriter sits in front of a service's stdout or stderr, parses each line
// for a sub-label, and emits matching lines under per-sub-label prefixWriters
// sharing colors via splitter. Non-matching lines go through fallback.
type splitWriter struct {
	mu       sync.Mutex
	buf      []byte
	fallback io.Writer
	out      io.Writer
	splitter *logSplitter
	subs     map[string]*prefixWriter
}

func newSplitWriter(fallback io.Writer, out io.Writer, splitter *logSplitter) *splitWriter {
	return &splitWriter{
		fallback: fallback,
		out:      out,
		splitter: splitter,
		subs:     map[string]*prefixWriter{},
	}
}

func (w *splitWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := w.buf[:idx]
		w.buf = w.buf[idx+1:]
		w.writeLine(line)
	}
	return len(p), nil
}

func (w *splitWriter) writeLine(line []byte) {
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	name, msgStart, ok := w.splitter.parse(line)
	if !ok {
		out := append(make([]byte, 0, len(line)+1), line...)
		out = append(out, '\n')
		_, _ = w.fallback.Write(out)
		return
	}
	pw, present := w.subs[name]
	if !present {
		label := name
		if w.splitter.outerLabel != "" {
			label = w.splitter.outerLabel + "/" + name
		}
		pw = newPrefixWriter(label, w.splitter.colorFor(name), w.out)
		w.subs[name] = pw
	}
	msg := line[msgStart:]
	out := append(make([]byte, 0, len(msg)+1), msg...)
	out = append(out, '\n')
	_, _ = pw.Write(out)
}

// parseComposePrefix returns the container name and the message start index
// for a line in docker-compose's combined-stream format. Returns ok=false
// when the line doesn't match — callers should fall through to the outer
// prefix in that case.
//
// Handles both the plain (`--ansi=never`) and colored forms:
//
//	api-1   | hello
//	\x1b[36mapi-1   \x1b[0m | hello        (name+padding colored, pipe plain)
//	\x1b[36mapi-1   |\x1b[0m hello         (name+padding+pipe colored)
//
// The message portion is returned verbatim — any embedded color codes in the
// message are preserved.
func parseComposePrefix(line []byte) (name string, msgStart int, ok bool) {
	pipeIdx := bytes.IndexByte(line, '|')
	if pipeIdx <= 0 {
		return "", 0, false
	}
	// Strip ANSI sequences from the prefix before matching the name pattern.
	stripped := ansiSeqRe.ReplaceAll(line[:pipeIdx], nil)
	m := composeNameRe.FindSubmatch(stripped)
	if m == nil {
		return "", 0, false
	}
	// Skip one optional space after the pipe so messages don't start with a
	// leading space (compose emits `<name>  | <msg>`, i.e. pipe-space-msg).
	// ANSI reset codes that immediately follow the pipe are left in place so
	// the rendered output still resets formatting between prefix and message.
	msgStart = pipeIdx + 1
	if msgStart < len(line) && line[msgStart] == ' ' {
		msgStart++
	}
	return string(m[1]), msgStart, true
}

func (w *splitWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.writeLine(w.buf)
		w.buf = nil
	}
	for _, pw := range w.subs {
		pw.Flush()
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

func runSingleMode(cmd *cobra.Command, args []string, controlPort int, groupFlag, tlsCert, tlsKey string, logSplit config.LogSplitConfig) error {
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
	// Resolve TLS paths against the caller's cwd — the daemon, which actually
	// loads the cert, may be running from a different directory.
	if tlsCert != "" {
		abs, err := filepath.Abs(tlsCert)
		if err != nil {
			return fmt.Errorf("resolve --tls-cert: %w", err)
		}
		tlsCert = abs
	}
	if tlsKey != "" {
		abs, err := filepath.Abs(tlsKey)
		if err != nil {
			return fmt.Errorf("resolve --tls-key: %w", err)
		}
		tlsKey = abs
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
		return runProxied(args, envVar, assignedPort, controlURL, serverName, clientID, logSplit)
	} else {
		proxyURL, proxyRunning := detectProxy(proxyPort)
		if !proxyRunning {
			slog.Info("no proxy detected, starting in solo mode", "proxy-port", proxyPort)
			return runSolo(args, logSplit)
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
		splitter, err := newLogSplitterFromConfig(logSplit, "")
		if err != nil {
			return err
		}
		if splitter != nil {
			opts.Stdout = newSplitWriter(os.Stdout, os.Stdout, splitter)
			opts.Stderr = newSplitWriter(os.Stderr, os.Stderr, splitter)
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

func runProxied(args []string, envVar string, port int, controlURL string, serverName string, clientID string, logSplit config.LogSplitConfig) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	splitter, err := newLogSplitterFromConfig(logSplit, "")
	if err != nil {
		return err
	}
	var stdoutSplit, stderrSplit *splitWriter
	if splitter != nil {
		stdoutSplit = newSplitWriter(os.Stdout, os.Stdout, splitter)
		stderrSplit = newSplitWriter(os.Stderr, os.Stderr, splitter)
		cmd.Stdout = stdoutSplit
		cmd.Stderr = stderrSplit
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%d", envVar, port), "MDP=1")
	flushSplits := func() {
		if stdoutSplit != nil {
			stdoutSplit.Flush()
		}
		if stderrSplit != nil {
			stderrSplit.Flush()
		}
	}
	defer flushSplits()

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
		if err != nil {
			hbCancel()
			disconnectFromOrchestrator(controlURL, clientID)
			if ee, ok := err.(*exec.ExitError); ok {
				flushSplits()
				os.Exit(ee.ExitCode())
			}
			return err
		}
		// Clean exit. The command may have detached (e.g. `docker compose
		// up -d`) — keep the registration alive while the port still
		// answers, and only disconnect once it stops or the user
		// interrupts.
		if err := holdDetached(sigCh, gone, controlURL, clientID, port); err != nil {
			return err
		}
		hbCancel()
	}
	return nil
}

// holdDetached keeps the client session alive after a clean command exit
// as long as the service port keeps responding. Returns when a signal
// arrives, the orchestrator goes away, or the port stops answering.
func holdDetached(sigCh <-chan os.Signal, gone <-chan struct{}, controlURL, clientID string, port int) error {
	// A detached command (e.g. `docker compose up -d`) may exit before the
	// backgrounded process has finished binding its port, so give the port
	// a short grace window to come up before concluding the command just
	// crashed.
	const bindGrace = 5 * time.Second
	deadline := time.Now().Add(bindGrace)
	for !registry.TCPCheck(port) {
		if time.Now().After(deadline) {
			disconnectFromOrchestrator(controlURL, clientID)
			return nil
		}
		select {
		case <-sigCh:
			disconnectFromOrchestrator(controlURL, clientID)
			return nil
		case <-gone:
			disconnectFromOrchestrator(controlURL, clientID)
			return nil
		case <-time.After(200 * time.Millisecond):
		}
	}
	slog.Info("command exited cleanly; port still reachable — keeping session alive", "port", port)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	failures := 0
	const threshold = 3
	for {
		select {
		case <-sigCh:
			disconnectFromOrchestrator(controlURL, clientID)
			return nil
		case <-gone:
			slog.Warn("orchestrator is shutting down")
			disconnectFromOrchestrator(controlURL, clientID)
			return nil
		case <-ticker.C:
			if registry.TCPCheck(port) {
				failures = 0
				continue
			}
			failures++
			if failures >= threshold {
				slog.Info("service port no longer reachable; disconnecting", "port", port, "failures", failures)
				disconnectFromOrchestrator(controlURL, clientID)
				return nil
			}
		}
	}
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

func runSolo(args []string, logSplit config.LogSplitConfig) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	splitter, err := newLogSplitterFromConfig(logSplit, "")
	if err != nil {
		return err
	}
	var stdoutSplit, stderrSplit *splitWriter
	if splitter != nil {
		stdoutSplit = newSplitWriter(os.Stdout, os.Stdout, splitter)
		stderrSplit = newSplitWriter(os.Stderr, os.Stderr, splitter)
		cmd.Stdout = stdoutSplit
		cmd.Stderr = stderrSplit
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	flushSplits := func() {
		if stdoutSplit != nil {
			stdoutSplit.Flush()
		}
		if stderrSplit != nil {
			stderrSplit.Flush()
		}
	}
	defer flushSplits()

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
				flushSplits()
				os.Exit(ee.ExitCode())
			}
			return err
		}
	}
	return nil
}
