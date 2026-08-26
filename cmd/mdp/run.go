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
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/depwait"
	"github.com/derekgould/multi-dev-proxy/internal/detect"
	"github.com/derekgould/multi-dev-proxy/internal/envexpand"
	"github.com/derekgould/multi-dev-proxy/internal/envexport"
	"github.com/derekgould/multi-dev-proxy/internal/health"
	"github.com/derekgould/multi-dev-proxy/internal/hookpty"
	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
	"github.com/derekgould/multi-dev-proxy/internal/ports"
	"github.com/derekgould/multi-dev-proxy/internal/portstore"
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
	buildProbe   func(*config.HealthCheck, int, string) func() bool
	hookMgr      *hookpty.Manager // nil: hooks run on plain pipes (CI, Windows, MDP_NO_HOOK_PTY)
	stdout       io.Writer        // service/log output destination; gated while a hook has focus
	stderr       io.Writer
}

func defaultBatchRuntime() batchRuntime {
	return batchRuntime{
		readyTimeout: 60 * time.Second,
		readyPoll:    200 * time.Millisecond,
		tcpCheck:     registry.TCPCheck,
		buildProbe:   health.Build,
		stdout:       os.Stdout,
		stderr:       os.Stderr,
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
	runCmd.Flags().Bool("no-stable-ports", false, "Disable stable per-branch port reuse (allocate fresh ports each run)")
	runCmd.Flags().String("env", "PORT", "Environment variable name for the assigned port")
	runCmd.Flags().Int("control-port", 13100, "Orchestrator control port")
	runCmd.Flags().String("log-split", "", `Demultiplex combined-stream logs. Values: "compose" (docker-compose format) or "regex:<pattern>" with named captures 'name' and 'msg'.`)
	runCmd.Flags().StringArray("link", nil, "Override the lookup group for cross-repo @<repo>.* env refs: repo=group (repeatable, last-wins per repo). Used when a peer service runs in a different group than the caller (e.g. backend on main, frontend on a feature branch). repo=@{current} resets that repo to the caller's own group.")
	runCmd.Flags().BoolP("interactive", "i", false, "Prompt for the inputs declared in mdp.yaml (see the `inputs:` section); without it, inputs use their defaults.")
	runCmd.Flags().StringSlice("service", nil, "Only start the listed services from mdp.yaml (repeatable or comma-separated). Transitive depends_on are auto-included. Falls back to env MDP_SERVICES. Default: start all.")
	runCmd.Flags().Bool("select-services", false, "Show an interactive checkbox picker for which mdp.yaml services to start (depends_on auto-included). Cannot combine with --service/MDP_SERVICES.")
	runCmd.Flags().BoolP("detach", "d", false, "Run mdp.yaml batch services in the background and return the terminal. Stop with `mdp run --stop`.")
	runCmd.Flags().Bool("stop", false, "Stop the detached batch run for the current repo/group.")
	runCmd.Flags().Bool("restart", false, "Automatically restart a service after its process exits (crash or clean exit). In batch mode this applies to every service in addition to any per-service `restart: true` in mdp.yaml. For a single-command run that detaches (e.g. `docker compose up -d`), this skips the normal detached-session hold and relaunches the command immediately on its clean exit.")
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

// mergeLinks combines config `links:` with CLI --link overrides. CLI wins per
// repo. Returns the CLI map unchanged when there are no config links. Empty
// groups are not filtered here — checkLinkGroups rejects them after the merge,
// so a CLI override can still rescue a config link that resolved empty.
func mergeLinks(configLinks, cliLinks map[string]string) map[string]string {
	if len(configLinks) == 0 {
		return cliLinks
	}
	if len(cliLinks) == 0 {
		return configLinks
	}
	merged := make(map[string]string, len(configLinks)+len(cliLinks))
	for repo, group := range configLinks {
		merged[repo] = group
	}
	for repo, group := range cliLinks {
		merged[repo] = group
	}
	return merged
}

// pickPort returns a port for key. When a previously-remembered port for key is
// still free (per isFree) and not already taken this run (exclude), it is
// reused; otherwise a fresh port is allocated via finder. The chosen port is
// recorded in picked so callers can persist it. Passing a nil remembered/picked
// map disables reuse/recording (used when stable ports are off).
func pickPort(
	finder func(ports.PortRange, []int) (int, error),
	isFree func(int) bool,
	remembered, picked map[string]int,
	key string,
	r ports.PortRange,
	exclude []int,
) (int, error) {
	if p, ok := remembered[key]; ok && p >= r.Start && p <= r.End && isFree(p) && !slices.Contains(exclude, p) {
		if picked != nil {
			picked[key] = p
		}
		return p, nil
	}
	p, err := finder(r, exclude)
	if err != nil {
		return 0, err
	}
	if picked != nil {
		picked[key] = p
	}
	return p, nil
}

// referencedServices returns the local sibling services that svc needs present
// to start cleanly: its explicit depends_on, plus any local service whose port
// or env var it interpolates from its own env without a default fallback.
// Cross-repo (@repo.) refs and defaulted refs are excluded — those tolerate the
// target's absence (the orchestrator resolver / the default covers them).
func referencedServices(svc config.ServiceConfig) []string {
	refs := append([]string(nil), svc.DependsOn...)
	for _, entry := range svc.Env {
		if entry.Ref != "" {
			if entry.HasDefault() || envexpand.IsCrossRepoBareRef(entry.Ref) {
				continue
			}
			// Local bare ref: svc.key or svc.env.VAR — name is up to first dot.
			name := entry.Ref
			if i := strings.IndexByte(name, '.'); i > 0 {
				name = name[:i]
			}
			refs = append(refs, name)
			continue
		}
		refs = append(refs, envexpand.LocalServiceRefs(entry.Value)...)
	}
	return refs
}

// resolveServiceSelection returns the set of service names to start, or nil to
// mean "all services". Empty selection (after trimming) returns nil. Unknown
// names return an error listing the valid names. The returned set is extended
// with the transitive closure of each selected service's dependencies — both
// depends_on and the local services it interpolates from env (see
// referencedServices) — so port allocation and env expansion have every
// service they reference, not just the ones declared in depends_on.
func resolveServiceSelection(cfg *config.Config, selection []string) (map[string]bool, error) {
	cleaned := make([]string, 0, len(selection))
	for _, s := range selection {
		s = strings.TrimSpace(s)
		if s != "" {
			cleaned = append(cleaned, s)
		}
	}
	if len(cleaned) == 0 {
		return nil, nil
	}

	var unknown []string
	for _, name := range cleaned {
		if _, ok := cfg.Services[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		valid := make([]string, 0, len(cfg.Services))
		for name := range cfg.Services {
			valid = append(valid, name)
		}
		sort.Strings(valid)
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown service(s): %s — valid: %s", strings.Join(unknown, ", "), strings.Join(valid, ", "))
	}

	selected := make(map[string]bool, len(cleaned))
	for _, name := range cleaned {
		selected[name] = true
	}

	var pulled []string
	queue := append([]string(nil), cleaned...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		for _, ref := range referencedServices(cfg.Services[name]) {
			if selected[ref] {
				continue
			}
			if _, ok := cfg.Services[ref]; !ok {
				// depends_on is validated in config.Load; an env ref to a
				// missing service would fail expansion at full startup too.
				// Surface it here with the referencing service for context.
				return nil, fmt.Errorf("service %q references unknown service %q", name, ref)
			}
			selected[ref] = true
			pulled = append(pulled, ref)
			queue = append(queue, ref)
		}
	}

	if len(pulled) > 0 {
		sort.Strings(pulled)
		slog.Info("auto-included dependencies", "services", pulled)
	}

	return selected, nil
}

func runRun(cmd *cobra.Command, args []string) error {
	controlPort, _ := cmd.Flags().GetInt("control-port")
	groupFlag, _ := cmd.Flags().GetString("group")

	if stop, _ := cmd.Flags().GetBool("stop"); stop {
		return runBatchStop(controlPort, groupFlag)
	}

	linkValues, _ := cmd.Flags().GetStringArray("link")
	linkMap, err := parseLinks(linkValues)
	if err != nil {
		return err
	}
	if len(linkMap) > 0 {
		pairs := make([]string, 0, len(linkMap))
		for repo, group := range linkMap {
			pairs = append(pairs, repo+"="+group)
		}
		sort.Strings(pairs)
		slog.Info("link flags parsed", "links", pairs)
	}

	interactive, _ := cmd.Flags().GetBool("interactive")
	selectServices, _ := cmd.Flags().GetBool("select-services")
	if len(args) == 0 {
		selection, _ := cmd.Flags().GetStringSlice("service")
		// An explicit but empty flag (e.g. `--service ""`) must not shadow
		// MDP_SERVICES — treat an all-blank selection as unset.
		hasService := false
		for _, s := range selection {
			if strings.TrimSpace(s) != "" {
				hasService = true
				break
			}
		}
		if !hasService {
			if env := os.Getenv("MDP_SERVICES"); env != "" {
				selection = strings.Split(env, ",")
				hasService = true
			}
		}
		if selectServices && hasService {
			return fmt.Errorf("cannot combine --select-services with --service/MDP_SERVICES — use one")
		}
		return runBatchMode(cmd, controlPort, groupFlag, linkMap, interactive, selection, selectServices)
	}
	if interactive {
		return fmt.Errorf("-i/--interactive applies only to mdp.yaml batch mode, not `mdp run -- <command>`")
	}
	if detach, _ := cmd.Flags().GetBool("detach"); detach {
		return fmt.Errorf("-d/--detach applies only to mdp.yaml batch mode, not `mdp run -- <command>`")
	}
	if selectServices {
		return fmt.Errorf("--select-services applies only to mdp.yaml batch mode, not `mdp run -- <command>`")
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

func runBatchMode(cmd *cobra.Command, controlPort int, groupFlag string, linkMap map[string]string, interactive bool, serviceSelection []string, selectServices bool) error {
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

	detach, _ := cmd.Flags().GetBool("detach")
	detachedChild := os.Getenv("_MDP_RUN_DETACHED") != ""
	restartFlag, _ := cmd.Flags().GetBool("restart")

	// Resolve declared inputs (prompting when -i, else defaults), then
	// substitute ${inputs.X} refs throughout the config so the env/link
	// pipeline below never sees an input reference. The detached child can't
	// prompt (no TTY), so it reads the values the detaching parent already
	// resolved and passed via _MDP_RUN_INPUTS.
	var inputs map[string]string
	if detachedChild {
		inputs, err = decodeDetachedInputs()
		// Strip the internal handoff vars from our environment so they don't
		// leak into every service/hook subprocess (which inherit os.Environ()):
		// _MDP_RUN_INPUTS may carry secrets, and a service that itself invokes
		// `mdp run` must not be misdetected as a detached child.
		os.Unsetenv("_MDP_RUN_INPUTS")
		os.Unsetenv("_MDP_RUN_DETACHED")
	} else {
		// Both ends matter: the wizard reads keys from stdin but renders to
		// stderr (see runInputWizard), so a redirected stderr would make the
		// prompt invisible even with a real stdin TTY.
		isTTY := func() bool {
			term := hookpty.RealTerm{}
			return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
		}
		inputs, err = resolveInputs(cfg, interactive, group, func(repo string) []string { return fetchActiveGroups(client, controlURL, repo) }, isTTY)
	}
	if err != nil {
		return err
	}
	if err := applyInputs(cfg, inputs); err != nil {
		return err
	}
	// Resolve dir/env_file paths now that input placeholders are gone — Load
	// deferred any that contained ${inputs.X} so absolute/~ input values
	// resolve correctly.
	cfg.FinalizePaths(filepath.Dir(configPath))

	// --select-services shows a checkbox picker over cfg.Services and replaces
	// serviceSelection with the user's picks; the detached child never prompts
	// (no TTY) — it receives the already-resolved --service list forwarded by
	// spawnDetachedRun below instead.
	if selectServices && !detachedChild {
		picked, err := selectServicesTUI(cfg, serviceSelection)
		if err != nil {
			return err
		}
		serviceSelection = picked
	}

	// Resolve the service selection after inputs are substituted, so its
	// env-ref dependency analysis sees final values — a ${inputs.X} reference
	// must not look like a dependency on a service named "inputs".
	selected, err := resolveServiceSelection(cfg, serviceSelection)
	if err != nil {
		return err
	}

	// Merge config `links:` (now input-substituted) under the CLI --link
	// overrides (CLI wins per repo), then reject empties — checked after the
	// merge so a --link override can rescue a config link that resolved empty.
	linkMap = mergeLinks(cfg.Links, linkMap)
	if err := checkLinkGroups(linkMap); err != nil {
		return err
	}
	if len(linkMap) > 0 {
		pairs := make([]string, 0, len(linkMap))
		for repo, group := range linkMap {
			pairs = append(pairs, repo+"="+group)
		}
		sort.Strings(pairs)
		slog.Info("cross-repo links resolved", "links", pairs)
	}

	portRange, err := ports.ParseRange(cfg.PortRange)
	if err != nil {
		return fmt.Errorf("invalid port range in config: %w", err)
	}

	repo := detect.DetectRepo(filepath.Dir(configPath))

	// Inputs are resolved and the config is finalized; everything below spawns
	// and supervises service processes, so re-exec ourselves as a detached
	// background supervisor and hand the resolved inputs to the child.
	if detach && !detachedChild {
		// Readiness is observed via the proxy registry, where batch services
		// register as "<group>/<name>" — and only when they declare a proxy
		// mapping (see launchBatchService). Services without one never appear
		// there, so don't count them.
		var expected []string
		for name, svc := range cfg.Services {
			if selected != nil && !selected[name] {
				continue
			}
			svcGroup := svc.Group
			if svcGroup == "" {
				svcGroup = group
			}
			proxied := svc.Proxy > 0
			for _, pm := range svc.Ports {
				if pm.Proxy > 0 {
					proxied = true
				}
			}
			if proxied {
				expected = append(expected, svcGroup+"/"+name)
			}
		}
		// The picker already ran above (in this parent); forward its resolved
		// picks to the child via the real --service flag so spawnDetachedRun's
		// arg-rebuild carries them along naturally — the child never re-prompts
		// (select-services is dropped from that rebuild below). Set("", ...)
		// only registers the flag as user-set (Visit below only forwards flags
		// in the FlagSet's internal "actual" set); the real value goes through
		// SliceValue.Replace so a service name containing a literal comma
		// isn't corrupted by Set's own CSV round-trip.
		if selectServices {
			sv, ok := cmd.Flags().Lookup("service").Value.(pflag.SliceValue)
			if !ok {
				return fmt.Errorf("--service flag is not a slice value")
			}
			if err := cmd.Flags().Set("service", ""); err != nil {
				return fmt.Errorf("forward resolved service selection to detached run: %w", err)
			}
			if err := sv.Replace(serviceSelection); err != nil {
				return fmt.Errorf("forward resolved service selection to detached run: %w", err)
			}
		}
		return spawnDetachedRun(cmd, repo, group, inputs, expected)
	}

	// Stable ports: reuse this branch's previously-assigned ports (when still
	// free) so certs/trust keyed to a port survive restarts. Best-effort —
	// remembered and picked stay nil when disabled, which makes pickPort fall
	// straight through to fresh allocation.
	noStable, _ := cmd.Flags().GetBool("no-stable-ports")
	stable := cfg.StablePortsEnabled() && !noStable
	var remembered, picked map[string]int
	if stable {
		remembered = portstore.Load(repo, group)
		picked = map[string]int{}
	}

	bt := &batchTracker{}

	var allocations []batchAlloc
	portMap := envexpand.PortMap{}
	var assignedPorts []int
	for name, svc := range cfg.Services {
		if selected != nil && !selected[name] {
			continue
		}
		if svc.Port > 0 {
			assignedPorts = append(assignedPorts, svc.Port)
		}
	}

	for name, svc := range cfg.Services {
		if selected != nil && !selected[name] {
			continue
		}
		if svc.Command == "" && svc.Port == 0 {
			continue
		}
		svcGroup := svc.Group
		if svcGroup == "" {
			svcGroup = group
		}
		if restartFlag {
			svc.Restart = true
		}

		if len(svc.Ports) > 0 {
			envProtocols := svc.EnvProtocols()
			portAssignments := make(map[string]int)
			for envName, value := range svc.Env {
				if value.Ref == "" && value.Value == "auto" {
					finder := ports.FindFreePort
					isFree := ports.IsPortFree
					if envProtocols[envName] == "udp" {
						finder = ports.FindFreeUDPPort
						isFree = ports.IsUDPPortFree
					}
					port, err := pickPort(finder, isFree, remembered, picked, name+"."+envName, portRange, assignedPorts)
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
			assignedPort, err = pickPort(ports.FindFreePort, ports.IsPortFree, remembered, picked, name, portRange, assignedPorts)
			if err != nil {
				return fmt.Errorf("find free port for %q: %w", name, err)
			}
			assignedPorts = append(assignedPorts, assignedPort)
		}
		portMap[name] = map[string]int{"port": assignedPort, "PORT": assignedPort}
		allocations = append(allocations, batchAlloc{name: name, svc: svc, svcGroup: svcGroup, assignedPort: assignedPort})
	}

	if stable {
		// Save merges picked into the on-disk file under a lock, so other
		// services' entries (and concurrent runs sharing this repo/group file)
		// survive.
		if err := portstore.Save(repo, group, picked); err != nil {
			slog.Warn("failed to persist stable ports", "err", err)
		}
	}

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

	var skipped map[string]bool
	if selected != nil {
		skipped = make(map[string]bool, len(cfg.Services))
		for name := range cfg.Services {
			if !selected[name] {
				skipped[name] = true
			}
		}
	}

	if err := exportBatchEnvFiles(cfg, allocations, portMap, allocResolvers, globalResolver, skipped); err != nil {
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
	// Interactive hook forwarding: setup/shutdown hooks run on a PTY so a
	// hook stuck on a prompt is detected and takes focus of this terminal.
	// nil when stdin/stdout is not a TTY or MDP_NO_HOOK_PTY is set.
	// Closed after bt.wg drains so shutdown hooks stay attachable.
	// All service/log output goes through gates the manager holds while a
	// hook has focus: buffered in memory, flushed when focus releases. With
	// no manager the gates are never held — pure pass-through.
	stdoutGate := hookpty.NewGate(os.Stdout)
	stderrGate := hookpty.NewGate(os.Stderr)
	rt.hookMgr = hookpty.NewManager(os.Stdin, os.Stdout, os.Stderr, hookpty.RealTerm{}, stdoutGate, stderrGate)
	if rt.hookMgr != nil {
		defer rt.hookMgr.Close()
		rt.stdout = stdoutGate
		rt.stderr = stderrGate
		// slog's default handler writes through the log package — route it
		// through the stderr gate so status lines don't interleave with an
		// interactive hook session.
		log.SetOutput(stderrGate)
		defer log.SetOutput(os.Stderr)
	}
	hasSupervisedProcess := false
	for i := range allocations {
		bt.wg.Add(1)
		if allocations[i].svc.Command != "" {
			hasSupervisedProcess = true
		}
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

	// allDone fires once every launched goroutine has returned for good
	// (crashed, exited cleanly, or failed to start — but never a
	// peer-triggered restart, which loops inside superviseProcess without
	// returning). Commandless external services return as soon as they
	// register, so bt.wg only reaches zero once every *supervised* process
	// has also stopped; gating on hasSupervisedProcess additionally skips an
	// all-external batch, where "done" would otherwise fire almost
	// immediately even though every service is still up and healthy.
	allDone := make(chan struct{})
	if hasSupervisedProcess {
		go func() { bt.wg.Wait(); close(allDone) }()
	}

	var runErr error
	select {
	case <-sigCh:
	case <-gone:
		slog.Warn("orchestrator is shutting down")
	case <-allDone:
		for _, st := range states {
			if st.Err != nil {
				runErr = fmt.Errorf("all services in group %q stopped; at least one exited with an error", group)
				break
			}
		}
		if runErr != nil {
			slog.Warn("all services stopped; at least one exited with an error", "group", group)
		} else {
			slog.Info("all services stopped cleanly; shutting down", "group", group)
		}
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
	if detachedChild {
		os.Remove(runPIDFilePath(repo, group))
	}
	return runErr
}

// decodeDetachedInputs reads the inputs the detaching parent resolved (and
// serialized into _MDP_RUN_INPUTS) so the detached child reuses them instead of
// prompting on a terminal it doesn't have.
func decodeDetachedInputs() (map[string]string, error) {
	inputs := map[string]string{}
	raw := os.Getenv("_MDP_RUN_INPUTS")
	if raw == "" || raw == "null" {
		return inputs, nil
	}
	if err := json.Unmarshal([]byte(raw), &inputs); err != nil {
		return nil, fmt.Errorf("decode detached inputs: %w", err)
	}
	return inputs, nil
}

// spawnDetachedRun re-execs `mdp run` as a background supervisor: the same batch
// loop, but detached from the terminal with output redirected to a per-run log
// file. Resolved inputs are handed to the child via env so it never prompts.
// Modeled on startDaemon (the orchestrator's own re-exec).
func spawnDetachedRun(cmd *cobra.Command, repo, group string, inputs map[string]string, expected []string) error {
	if err := os.MkdirAll(stateDir(), 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	// Refuse to start a second supervisor for the same repo/group — it would
	// overwrite the PID file and orphan the first run beyond reach of --stop.
	if b, err := os.ReadFile(runPIDFilePath(repo, group)); err == nil {
		if oldPID, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil && process.IsProcessAlive(oldPID) {
			return fmt.Errorf("a detached run is already active for %s/%s (PID %d) — stop it first with `mdp run --stop`", repo, group, oldPID)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine working directory: %w", err)
	}

	logPath := runLogFilePath(repo, group)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		logFile.Close()
		return fmt.Errorf("find executable: %w", err)
	}

	inputsJSON, err := json.Marshal(inputs)
	if err != nil {
		logFile.Close()
		return fmt.Errorf("encode inputs: %w", err)
	}

	// Re-issue `mdp run --detach` preserving the flags the user set. Drop the
	// flags that don't apply to the supervisor: --detach is re-added explicitly,
	// --stop would short-circuit, and --interactive can't prompt (inputs are
	// already resolved). Use the --name=value form so bool/slice flags re-parse.
	args := []string{"run", "--detach"}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		switch f.Name {
		case "detach", "stop", "interactive", "select-services":
			return
		}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			for _, v := range sv.GetSlice() {
				args = append(args, "--"+f.Name+"="+v)
			}
			return
		}
		args = append(args, "--"+f.Name+"="+f.Value.String())
	})

	child := exec.Command(exe, args...)
	child.Dir = cwd // child re-runs config.Find(cwd); cwd must be preserved
	child.Env = append(os.Environ(), "_MDP_RUN_DETACHED=1", "_MDP_RUN_INPUTS="+string(inputsJSON))
	child.Stdout = logFile
	child.Stderr = logFile
	child.Stdin = nil
	child.SysProcAttr = detachProcAttr()

	if err := child.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("start detached run: %w", err)
	}
	logFile.Close()

	pid := child.Process.Pid
	if err := os.WriteFile(runPIDFilePath(repo, group), []byte(strconv.Itoa(pid)), 0644); err != nil {
		slog.Warn("failed to write run PID file", "err", err)
	}

	controlPort, _ := cmd.Flags().GetInt("control-port")
	controlURL := fmt.Sprintf("http://127.0.0.1:%d", controlPort)
	started := waitForDetachedServices(controlURL, expected, 15*time.Second)

	fmt.Printf("mdp run started in background (PID %d, group %s)\n", pid, group)
	switch {
	case len(expected) == 0:
		fmt.Printf("  logs: %s\n", logPath)
	case len(started) < len(expected):
		fmt.Printf("  %d/%d services up so far — follow logs: %s\n", len(started), len(expected), logPath)
	default:
		fmt.Printf("  %d services up — logs: %s\n", len(started), logPath)
	}
	fmt.Println("  stop with `mdp run --stop`")
	return nil
}

// waitForDetachedServices polls the proxy registry until every expected server
// (named "<group>/<service>") is registered, or the timeout elapses, returning
// the names that came up. Batch services register through /__mdp/register and
// surface under /__mdp/proxies — not /__mdp/services, which only the
// orchestrator's own runner populates.
func waitForDetachedServices(controlURL string, expected []string, timeout time.Duration) []string {
	if len(expected) == 0 {
		return nil
	}
	want := make(map[string]bool, len(expected))
	for _, n := range expected {
		want[n] = true
	}
	seen := map[string]bool{}
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && len(seen) < len(want) {
		resp, err := client.Get(controlURL + "/__mdp/proxies")
		if err == nil {
			var proxies []struct {
				Servers []struct {
					Name string `json:"name"`
				} `json:"servers"`
			}
			json.NewDecoder(resp.Body).Decode(&proxies)
			resp.Body.Close()
			for _, p := range proxies {
				for _, s := range p.Servers {
					if want[s.Name] {
						seen[s.Name] = true
					}
				}
			}
		}
		if len(seen) < len(want) {
			time.Sleep(300 * time.Millisecond)
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// runBatchStop stops the detached batch run for the current repo/group by
// signaling the supervisor PID recorded by spawnDetachedRun (see
// signalDetachedRun for the per-platform mechanism).
func runBatchStop(controlPort int, groupFlag string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine working directory: %w", err)
	}
	configPath := config.Find(cwd)
	if configPath == "" {
		return fmt.Errorf("no mdp.yaml found — run `mdp run --stop` from a repo with an mdp.yaml")
	}
	dir := filepath.Dir(configPath)
	group := groupFlag
	if group == "" {
		group = orchestrator.DetectGroup(dir)
	}
	repo := detect.DetectRepo(dir)

	pidPath := runPIDFilePath(repo, group)
	b, err := os.ReadFile(pidPath)
	if err != nil {
		return fmt.Errorf("no detached run for %s/%s", repo, group)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		os.Remove(pidPath)
		return fmt.Errorf("invalid run PID file %s", pidPath)
	}
	if !process.IsProcessAlive(pid) {
		os.Remove(pidPath)
		fmt.Printf("no detached run for %s/%s (cleaned up stale PID file)\n", repo, group)
		return nil
	}

	if err := signalDetachedRun(pid); err != nil {
		return fmt.Errorf("stop process %d: %w", pid, err)
	}

	// Wait for the supervisor to drain its services and exit. Keep the PID file
	// if it outlives the wait (slow shutdown hooks) — the supervisor removes the
	// file itself on clean exit, and a later --stop can still find it meanwhile.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && process.IsProcessAlive(pid) {
		time.Sleep(200 * time.Millisecond)
	}
	if process.IsProcessAlive(pid) {
		fmt.Printf("sent stop to detached run %s/%s (PID %d); still shutting down — it will exit and clean up once services drain\n", repo, group, pid)
		return nil
	}
	os.Remove(pidPath)
	fmt.Printf("stopped detached run for %s/%s (PID %d)\n", repo, group, pid)
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
				registrations = append(registrations, regEntry{
					serverName: fmt.Sprintf("%s/%s", a.svcGroup, a.name),
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

	color := nextColor()
	pw := newPrefixWriter(a.name, color, rt.stdout)
	pwErr := newPrefixWriter(a.name, color, rt.stderr)

	// postStartHooks runs the service's post_start hooks. Best-effort: failures
	// only warn. waitTCP re-polls TCP readiness first (restart path). When
	// health_check names docker compose services, hooks additionally wait for
	// that gate before running.
	postStartHooks := func(env []string, waitTCP bool) {
		if len(a.svc.PostStart.Commands) == 0 {
			return
		}
		if waitTCP && len(probePorts) > 0 {
			if err := depwait.TCPReady(ctx, probePorts, rt.readyTimeout, rt.readyPoll, rt.tcpCheck); err != nil {
				slog.Warn("post_start skipped; service not ready", "name", a.name, "err", err)
				return
			}
		}
		if hc := a.svc.HealthCheck; hc != nil && len(hc.DockerServices) > 0 {
			probe := rt.buildProbe(hc, 0, a.svc.Dir)
			deadline := time.Now().Add(rt.readyTimeout)
			for !probe() {
				if time.Now().After(deadline) {
					slog.Warn("post_start skipped; docker health gate not ready", "name", a.name, "timeout", rt.readyTimeout)
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(rt.readyPoll):
				}
			}
		}
		for i, raw := range a.svc.PostStart.Commands {
			slog.Info("service hook", "name", a.name, "phase", "post_start", "step", i+1, "cmd", raw)
			if err := runServiceHook(ctx, rt.hookMgr, raw, a.name, "post_start", env, a.svc.Dir, pw, pwErr, postStartWaitDelay); err != nil {
				slog.Warn("post_start hook failed", "name", a.name, "step", i+1, "cmd", raw, "err", err)
			}
		}
	}

	// runPostStart runs the hooks on a goroutine so signalReady timing is
	// never affected and the supervise loop is never blocked.
	var postStartWG sync.WaitGroup
	var postStartMu sync.Mutex // serializes initial run vs restart re-runs
	runPostStart := func(env []string, waitTCP bool) {
		if len(a.svc.PostStart.Commands) == 0 {
			return
		}
		postStartWG.Add(1)
		go func() {
			defer postStartWG.Done()
			postStartMu.Lock()
			defer postStartMu.Unlock()
			postStartHooks(env, waitTCP)
		}()
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
				return
			}
		}
		signalReady()
		// Nothing else to supervise on this path, so the hooks run inline.
		postStartHooks(a.env, false)
		pw.Flush()
		pwErr.Flush()
		return
	}

	env := a.env

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
		stdoutW = newSplitWriter(pw, rt.stdout, splitter)
		stderrW = newSplitWriter(pwErr, rt.stderr, splitter)
	}

	// Run setup before registering so routing never points at a service
	// whose setup is still running (or has failed).
	for i, raw := range a.svc.Setup {
		slog.Info("service hook", "name", a.name, "phase", "setup", "step", i+1, "cmd", raw)
		if err := runServiceHook(ctx, rt.hookMgr, raw, a.name, "setup", env, a.svc.Dir, pw, pwErr, 0); err != nil {
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

	// Start waiting on the process now (Wait may only be called once), so a
	// crash during the readiness poll below is noticed immediately instead
	// of only after the poll's full timeout — otherwise a service whose
	// process dies right after starting wouldn't reach superviseProcess (and
	// so wouldn't get crash-restarted) for up to rt.readyTimeout.
	cmdExit := waitCmd(cmd)

	// Poll TCP readiness so dependents only unblock once this service is
	// actually accepting connections. Bail out early if the process exits
	// first — no point polling a dead process's port for the full timeout.
	if len(probePorts) > 0 {
		readyCtx, readyCancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-cmdExit.done:
				readyCancel()
			case <-readyCtx.Done():
			}
		}()
		if err := depwait.TCPReady(readyCtx, probePorts, rt.readyTimeout, rt.readyPoll, rt.tcpCheck); err != nil {
			slog.Error("service not ready", "name", a.name, "err", err)
			state.Err = err
			// Fall through to wait for the cmd — leave it running so logs
			// still stream and shutdown can clean it up normally.
		}
		readyCancel()
	}

	// Signal dependents now; the rest of this goroutine just drains the cmd
	// and (if this service has cross-repo peers) restarts it on peer change.
	signalReady()
	if state.Err == nil {
		runPostStart(env, false)
	}

	if err := superviseProcess(ctx, cmd, cmdExit, bt, client, controlURL, a, registered, registerAll, runPostStart, portMap, resolver, linkMap, pw, pwErr); err != nil {
		state.Err = err
	}

	// In-flight post_start hooks are ctx-bound, so after shutdown this wait is
	// short; it keeps hook output ahead of shutdown hooks and the final flush.
	postStartWG.Wait()

	for i, raw := range a.svc.Shutdown {
		slog.Info("service hook", "name", a.name, "phase", "shutdown", "step", i+1, "cmd", raw)
		hCtx, hCancel := context.WithTimeout(context.Background(), shutdownHookTimeout)
		if err := runServiceHook(hCtx, rt.hookMgr, raw, a.name, "shutdown", a.env, a.svc.Dir, pw, pwErr, 0); err != nil {
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

// postStartWaitDelay bounds how long a post_start hook's Run may linger in
// the pipe drain after the hook process exits or ctx is cancelled. It is not
// a limit on the hook's own runtime.
const postStartWaitDelay = 10 * time.Second

// runServiceHook executes one setup/shutdown/post_start hook command. When
// mgr is non-nil the hook runs on a PTY (stderr merges into stdout — PTYs
// have one stream) so a hook stuck on a prompt is detected and can be
// attached to; otherwise output pipes to the prefixed writers with stdin
// disconnected. waitDelay (when non-zero) bounds the pipe-fallback Run's
// post-exit pipe drain and post-cancel wait — not the hook's own runtime.
func runServiceHook(ctx context.Context, mgr *hookpty.Manager, raw, name, phase string, env []string, dir string, pw, pwErr io.Writer, waitDelay time.Duration) error {
	parts, err := orchestrator.SplitHookArgs(raw)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return nil
	}
	newCmd := func(extraEnv ...string) *exec.Cmd {
		h := exec.CommandContext(ctx, parts[0], parts[1:]...)
		h.Env = append(append(os.Environ(), extraEnv...), env...)
		if dir != "" {
			h.Dir = dir
		}
		return h
	}
	if mgr != nil {
		// On a PTY, TTY-detecting tools turn interactive — disable pagers by
		// default so e.g. `git log` doesn't hang in `less` unattended. The
		// service's own env (appended after) still overrides. The PTY path
		// needs no waitDelay: it has no pipes to drain, and ctx cancellation
		// already escalates SIGINT→SIGKILL inside hookpty.
		ran, err := mgr.RunHook(ctx, newCmd("PAGER=cat", "GIT_PAGER=cat"), name+" "+phase, pw)
		if ran {
			return err
		}
		slog.Debug("hook PTY unavailable; falling back to pipes", "name", name, "err", err)
	}
	h := newCmd()
	h.WaitDelay = waitDelay
	h.Stdout = pw
	h.Stderr = pwErr
	return h.Run()
}

// peerWatchInterval is how often supervisor goroutines poll the orchestrator
// for cross-repo peer state changes. Package-level so tests can shorten it.
var peerWatchInterval = 2 * time.Second

// crashRestartDelay is the pause before respawning a service whose process
// exited (crash or clean exit) with restart enabled. Package-level so tests
// can shorten it.
var crashRestartDelay = 1 * time.Second

// cmdWaiter broadcasts a cmd.Wait() result to multiple observers (exec.Cmd's
// Wait may only be called once). done closes once err is set.
type cmdWaiter struct {
	done chan struct{}
	err  error
}

func waitCmd(cmd *exec.Cmd) *cmdWaiter {
	w := &cmdWaiter{done: make(chan struct{})}
	go func() {
		w.err = cmd.Wait()
		close(w.done)
	}()
	return w
}

// superviseProcess waits for cmd to exit, restarting it whenever a watched
// cross-repo peer's port or env value changes, or (when a.svc.Restart is
// true) whenever the process itself exits. Returns when the cmd exits
// without a restart, or when ctx is cancelled.
//
// initialExit, when non-nil, is a cmdWaiter already watching cmd (started by
// the caller so a crash during the caller's own pre-supervision readiness
// poll is observed promptly) — its result is reused for the first loop
// iteration instead of calling cmd.Wait() a second time. Every later
// iteration (relaunch) waits on its own fresh cmd as usual.
func superviseProcess(
	ctx context.Context,
	cmd *exec.Cmd,
	initialExit *cmdWaiter,
	bt *batchTracker,
	client *http.Client,
	controlURL string,
	a *batchAlloc,
	registered []string,
	registerAll func() ([]string, error),
	runPostStart func(env []string, waitTCP bool),
	portMap envexpand.PortMap,
	resolver envexpand.Resolver,
	linkMap map[string]string,
	pw, pwErr *prefixWriter,
) error {
	peerRefs := extractPeerRefs(a.svc)
	// Seed peerRefs with the values we resolved at startup so the watcher
	// only fires on a *change*, not on first sight.
	if len(peerRefs) > 0 {
		_, peerRefs = refreshPeerRefs(client, controlURL, a.name, a.svcGroup, linkMap, peerRefs)
	}

	for {
		watchCtx, watchCancel := context.WithCancel(ctx)
		peerCh := make(chan []peerRef, 1)
		if len(peerRefs) > 0 {
			go watchPeerRefs(watchCtx, client, controlURL, a.name, a.svcGroup, linkMap, peerRefs, peerWatchInterval, peerCh)
		}

		cmdExit := make(chan error, 1)
		if initialExit != nil {
			w := initialExit
			initialExit = nil
			go func() { <-w.done; cmdExit <- w.err }()
		} else {
			go func(c *exec.Cmd) { cmdExit <- c.Wait() }(cmd)
		}

		var restart bool
		var exitErr error
		select {
		case <-ctx.Done():
			watchCancel()
			cmd.Process.Signal(syscall.SIGTERM)
			<-cmdExit
		case waitErr := <-cmdExit:
			watchCancel()
			if waitErr != nil {
				slog.Error("service process exited", "name", a.name, "command", a.svc.Command, "err", waitErr)
				exitErr = waitErr
			}
			if a.svc.Restart {
				slog.Info("service exited; restarting", "name", a.name)
				select {
				case <-ctx.Done():
					return nil // shutting down — don't relaunch
				case <-time.After(crashRestartDelay):
				}
				restart = true
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
			return exitErr
		}

		// Flush any partial line still buffered from the exited process —
		// otherwise its trailing fragment glues onto the relaunched
		// process's first output.
		pw.Flush()
		pwErr.Flush()

		newEnv, err := buildBatchEnv(*a, portMap, resolver)
		if err != nil {
			slog.Error("rebuild env failed; not restarting", "name", a.name, "err", err)
			return nil
		}
		a.env = newEnv
		if a.svc.EnvFile != "" {
			if err := envexport.WritePerService(a.svc.EnvFile, newEnv); err != nil {
				slog.Warn("rewrite env file failed", "name", a.name, "err", err)
			}
		}
		if _, err := registerAll(); err != nil {
			slog.Error("re-register failed; not restarting", "name", a.name, "err", err)
			return err
		}
		// The exited cmd stays in bt's tracked list otherwise — with restart
		// enabled that list would grow without bound across a crash loop.
		bt.remove(cmd)
		newCmd, err := startBatchCommand(bt, a.svc.Command, a.svc.Dir, newEnv, pw, pwErr)
		if err != nil {
			slog.Error("restart failed", "name", a.name, "err", err)
			for _, sn := range registered {
				deregisterFromOrchestrator(controlURL, sn)
			}
			return err
		}
		for _, sn := range registered {
			updatePIDWithOrchestrator(controlURL, sn, newCmd.Process.Pid)
		}
		if a.svc.PostStart.OnRestart {
			// Readiness re-polling happens inside the hook goroutine, so the
			// supervise loop is never blocked.
			runPostStart(newEnv, true)
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
//
// skipped, if non-nil, is the set of services excluded by --service selection.
// Entries in cfg.Global.Env that reference a skipped service (and have no
// default to fall back on) are omitted from the global env file with a warning,
// matching the graceful-degradation behavior used for unresolved cross-repo
// peers.
func exportBatchEnvFiles(cfg *config.Config, allocations []batchAlloc, portMap envexpand.PortMap, allocResolvers []envexpand.Resolver, globalResolver envexpand.Resolver, skipped map[string]bool) error {
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
		globalEnv := filterGlobalEnvForSkipped(cfg.Global.Env, skipped)
		if err := envexport.WriteGlobalWith(cfg.Global.EnvFile, globalEnv, portMap, envMap, globalResolver); err != nil {
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

// filterGlobalEnvForSkipped returns a copy of globalEnv with entries removed
// whose Ref or Value references a local service in the skipped set without a
// default fallback. Entries with defaults are kept (the resolver will use the
// default). Returns globalEnv unchanged when skipped is empty.
func filterGlobalEnvForSkipped(globalEnv map[string]config.EnvValue, skipped map[string]bool) map[string]config.EnvValue {
	if len(skipped) == 0 || len(globalEnv) == 0 {
		return globalEnv
	}
	out := make(map[string]config.EnvValue, len(globalEnv))
	for k, entry := range globalEnv {
		if entry.Ref != "" {
			if entry.HasDefault() || envexpand.IsCrossRepoBareRef(entry.Ref) {
				out[k] = entry
				continue
			}
			// Local bare ref: svc.key or svc.env.VAR. Service name is everything
			// before the first dot.
			svc := entry.Ref
			if i := strings.IndexByte(svc, '.'); i > 0 {
				svc = svc[:i]
			}
			if skipped[svc] {
				slog.Warn("omitting global env entry that references unselected service", "key", k, "ref", entry.Ref, "service", svc)
				continue
			}
			out[k] = entry
			continue
		}
		// Value form may embed ${svc.key} refs without defaults. Omit the entry
		// if any such ref points at a skipped service.
		if svc := firstSkippedRef(entry.Value, skipped); svc != "" {
			slog.Warn("omitting global env entry that references unselected service", "key", k, "value", entry.Value, "service", svc)
			continue
		}
		out[k] = entry
	}
	return out
}

// firstSkippedRef returns the first local service referenced by value (without
// a default) that is in the skipped set, or "" if none. Cross-repo and
// defaulted refs are excluded by envexpand.LocalServiceRefs.
func firstSkippedRef(value string, skipped map[string]bool) string {
	for _, svc := range envexpand.LocalServiceRefs(value) {
		if skipped[svc] {
			return svc
		}
	}
	return ""
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

// remove drops an already-exited cmd from the tracked list, e.g. right
// before a restart replaces it — otherwise a crash-restart loop grows this
// list without bound.
func (bt *batchTracker) remove(cmd *exec.Cmd) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	for i, c := range bt.cmds {
		if c == cmd {
			bt.cmds = append(bt.cmds[:i], bt.cmds[i+1:]...)
			return
		}
	}
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
	restartFlag, _ := cmd.Flags().GetBool("restart")

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
	repo := repoOverride
	if repo == "" {
		repo = detect.DetectRepo(cwd)
	}
	serverName := nameOverride
	if serverName == "" {
		serverName = detect.ServerName(repo, detect.DetectBranch(cwd))
	}

	group := groupFlag
	if group == "" {
		group = detect.DetectBranch(cwd)
	}

	// Stable ports: reuse this branch's previously-assigned port when still free
	// (see runBatchMode). On by default; --no-stable-ports opts out.
	noStable, _ := cmd.Flags().GetBool("no-stable-ports")
	stable := !noStable
	var remembered, picked map[string]int
	if stable {
		remembered = portstore.Load(repo, group)
		picked = map[string]int{}
	}
	assignedPort, err := pickPort(ports.FindFreePort, ports.IsPortFree, remembered, picked, serverName, portRange, nil)
	if err != nil {
		return fmt.Errorf("find free port: %w", err)
	}
	if stable {
		// Save merges under a lock, so batch-mode services sharing this
		// repo/group file aren't wiped by an ad-hoc single-command run.
		if err := portstore.Save(repo, group, picked); err != nil {
			slog.Warn("failed to persist stable ports", "err", err)
		}
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
		return runProxied(args, envVar, assignedPort, controlURL, serverName, clientID, logSplit, restartFlag)
	} else {
		proxyURL, proxyRunning := detectProxy(proxyPort)
		if !proxyRunning {
			slog.Info("no proxy detected, starting in solo mode", "proxy-port", proxyPort)
			return runSolo(args, logSplit, restartFlag)
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
			Restart:      restartFlag,
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

func runProxied(args []string, envVar string, port int, controlURL string, serverName string, clientID string, logSplit config.LogSplitConfig, restart bool) error {
	splitter, err := newLogSplitterFromConfig(logSplit, "")
	if err != nil {
		return err
	}

	hbCtx, hbCancel := context.WithCancel(context.Background())
	defer hbCancel()
	startHeartbeat(hbCtx, controlURL, clientID)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	gone := watchShutdown(controlURL)

	// waitBeforeRestart pauses crashRestartDelay before a relaunch, but
	// bails out (disconnecting) if a shutdown signal arrives first. Only
	// called once the crashed/exited process has already been reaped.
	waitBeforeRestart := func() (stop bool) {
		select {
		case <-sigCh:
			hbCancel()
			disconnectFromOrchestrator(controlURL, clientID)
			return true
		case <-gone:
			hbCancel()
			disconnectFromOrchestrator(controlURL, clientID)
			return true
		case <-time.After(crashRestartDelay):
			return false
		}
	}

	for {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdin = os.Stdin
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

		if err := cmd.Start(); err != nil {
			flushSplits()
			// On a restart iteration the orchestrator already has this
			// client registered from the previous run — disconnect it so a
			// failed relaunch doesn't leave a stale registration behind.
			disconnectFromOrchestrator(controlURL, clientID)
			return fmt.Errorf("start %q: %w", args[0], err)
		}
		updatePIDWithOrchestrator(controlURL, serverName, cmd.Process.Pid)

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

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
			flushSplits()
			return nil
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
			flushSplits()
			return nil
		case err := <-done:
			flushSplits()
			if restart {
				// Auto-restart is incompatible with holding a session open
				// for a detached command — relaunch instead, on crash or
				// clean exit alike.
				slog.Info("service exited; restarting", "name", serverName)
				if waitBeforeRestart() {
					return nil
				}
				continue
			}
			if err != nil {
				hbCancel()
				disconnectFromOrchestrator(controlURL, clientID)
				if ee, ok := err.(*exec.ExitError); ok {
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
			return nil
		}
	}
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

func runSolo(args []string, logSplit config.LogSplitConfig, restart bool) error {
	splitter, err := newLogSplitterFromConfig(logSplit, "")
	if err != nil {
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	for {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdin = os.Stdin
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

		if err := cmd.Start(); err != nil {
			flushSplits()
			return fmt.Errorf("start %q: %w", args[0], err)
		}

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
			flushSplits()
			return nil
		case err := <-done:
			flushSplits()
			if restart {
				// Relaunch regardless of exit code — restart is "crash or
				// clean exit alike" (err is deliberately not inspected here).
				slog.Info("command exited; restarting")
				select {
				case <-sigCh:
					return nil
				case <-time.After(crashRestartDelay):
				}
				continue
			}
			if err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					os.Exit(ee.ExitCode())
				}
				return err
			}
			return nil
		}
	}
}
