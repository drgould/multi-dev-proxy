package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/depwait"
	"github.com/derekgould/multi-dev-proxy/internal/detect"
	"github.com/derekgould/multi-dev-proxy/internal/envexpand"
	"github.com/derekgould/multi-dev-proxy/internal/ports"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

// readyTimeout and readyPoll are package-level vars (not consts) so tests can
// shorten them.
var (
	readyTimeout = 60 * time.Second
	readyPoll    = 200 * time.Millisecond
)

// tcpCheck is overridable in tests.
var tcpCheck = registry.TCPCheck

type serviceAlloc struct {
	name            string
	svc             config.ServiceConfig
	assignedPort    int
	portAssignments map[string]int // multi-port only
}

// StartConfigServices starts all services from the config under the given group name.
func (o *Orchestrator) StartConfigServices(ctx context.Context, group string) error {
	if o.cfg == nil || len(o.cfg.Services) == 0 {
		return nil
	}
	portRange, err := ports.ParseRange(o.cfg.PortRange)
	if err != nil {
		return fmt.Errorf("invalid port_range: %w", err)
	}

	var allocations []serviceAlloc
	portMap := envexpand.PortMap{}
	var assignedPorts []int
	for _, svc := range o.cfg.Services {
		if svc.Port > 0 {
			assignedPorts = append(assignedPorts, svc.Port)
		}
	}

	for name, svc := range o.cfg.Services {
		if svc.Command == "" && svc.Port == 0 {
			continue
		}
		if len(svc.Ports) > 0 {
			portAssignments := make(map[string]int)
			for envName, value := range svc.Env {
				if value == "auto" {
					port, err := ports.FindFreePort(portRange, assignedPorts)
					if err != nil {
						return fmt.Errorf("find free port for %s.%s: %w", name, envName, err)
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
			allocations = append(allocations, serviceAlloc{name, svc, 0, portAssignments})
			continue
		}
		assignedPort := svc.Port
		if assignedPort == 0 {
			var err error
			assignedPort, err = ports.FindFreePort(portRange, assignedPorts)
			if err != nil {
				return fmt.Errorf("find free port for %s: %w", name, err)
			}
			assignedPorts = append(assignedPorts, assignedPort)
		}
		portMap[name] = map[string]int{"port": assignedPort, "PORT": assignedPort}
		allocations = append(allocations, serviceAlloc{name, svc, assignedPort, nil})
	}

	names := make([]string, 0, len(allocations))
	for _, a := range allocations {
		names = append(names, a.name)
	}
	states := depwait.NewStates(names)

	var wg sync.WaitGroup
	for _, a := range allocations {
		wg.Add(1)
		go func(a serviceAlloc, state *depwait.State) {
			defer wg.Done()
			defer close(state.Done)

			serverName := fmt.Sprintf("%s/%s", group, a.name)
			tracked := len(a.svc.DependsOn) > 0 && a.svc.Command != ""

			if tracked {
				o.SetService(serverName, &ManagedService{
					Name:   serverName,
					Config: a.svc,
					Group:  group,
					Port:   a.assignedPort,
					Status: "waiting",
				})
			}

			if err := depwait.Wait(ctx, states, a.svc.DependsOn, readyTimeout); err != nil {
				slog.Error("service aborted waiting on deps", "name", a.name, "err", err)
				state.Err = err
				if tracked {
					o.UpdateServiceStatus(serverName, "failed")
				}
				return
			}

			var err error
			if len(a.svc.Ports) > 0 {
				err = o.startMultiPortService(ctx, a.name, a.svc, group, a.portAssignments, portMap)
			} else {
				err = o.startSingleService(ctx, a.name, a.svc, group, a.assignedPort, portMap)
			}
			if err != nil {
				slog.Error("failed to start service", "name", a.name, "err", err)
				state.Err = err
				if tracked {
					o.UpdateServiceStatus(serverName, "failed")
				}
				return
			}

			probePorts := probePortsFor(a)
			if len(probePorts) == 0 {
				if a.svc.Command != "" {
					o.UpdateServiceStatus(serverName, "running")
				}
				return
			}
			if err := o.waitForReady(ctx, serverName, probePorts, readyTimeout); err != nil {
				slog.Error("service not ready", "name", a.name, "err", err)
				state.Err = err
				o.UpdateServiceStatus(serverName, "failed")
				return
			}
			o.UpdateServiceStatus(serverName, "running")
		}(a, states[a.name])
	}
	wg.Wait()
	return nil
}

// probePortsFor returns the TCP ports that should be polled to decide whether
// a service has become ready. External services (no command) are probed too
// so dependents only unblock once the externally-managed service is actually
// reachable.
func probePortsFor(a serviceAlloc) []int {
	if len(a.portAssignments) > 0 {
		result := make([]int, 0, len(a.portAssignments))
		for _, p := range a.portAssignments {
			result = append(result, p)
		}
		return result
	}
	if a.assignedPort > 0 {
		return []int{a.assignedPort}
	}
	return nil
}

// waitForReady polls TCP reachability on the given ports until all respond or
// the timeout elapses. Returns early with an error if the service's status
// becomes terminal (the process exited) before any port responds.
func (o *Orchestrator) waitForReady(ctx context.Context, serverName string, probePorts []int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(readyPoll)
	defer ticker.Stop()
	for {
		if status, ok := o.ServiceStatus(serverName); ok && (status == "stopped" || status == "failed") {
			return fmt.Errorf("service exited before becoming ready (status=%s)", status)
		}
		allReady := true
		for _, p := range probePorts {
			if !tcpCheck(p) {
				allReady = false
				break
			}
		}
		if allReady {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not ready on ports %v after %s", probePorts, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (o *Orchestrator) startSingleService(ctx context.Context, name string, svc config.ServiceConfig, group string, assignedPort int, portMap envexpand.PortMap) error {
	serverName := fmt.Sprintf("%s/%s", group, name)

	if svc.Command != "" {
		env, err := buildEnv(svc.Env, map[string]int{"PORT": assignedPort}, portMap)
		if err != nil {
			return fmt.Errorf("build env for %s: %w", name, err)
		}
		if err := o.launchProcess(ctx, name, svc, serverName, group, assignedPort, env); err != nil {
			return err
		}
	}

	if svc.Proxy > 0 {
		scheme := svc.Scheme
		if scheme == "" {
			scheme = "http"
		}
		entry := &registry.ServerEntry{
			Name:        serverName,
			Repo:        name,
			Group:       group,
			Port:        assignedPort,
			Scheme:      scheme,
			TLSCertPath: svc.TLSCert,
			TLSKeyPath:  svc.TLSKey,
		}
		if err := o.Register(svc.Proxy, entry); err != nil {
			return fmt.Errorf("register %s: %w", serverName, err)
		}
		if svc.TLSCert != "" {
			if err := o.AddCert(svc.TLSCert, svc.TLSKey); err != nil {
				slog.Warn("failed to load service TLS cert", "name", serverName, "err", err)
			}
		}
	}

	return nil
}

func (o *Orchestrator) startMultiPortService(ctx context.Context, name string, svc config.ServiceConfig, group string, portAssignments map[string]int, portMap envexpand.PortMap) error {
	if svc.Command != "" {
		env, err := buildEnv(svc.Env, portAssignments, portMap)
		if err != nil {
			return fmt.Errorf("build env for %s: %w", name, err)
		}
		if err := o.launchProcess(ctx, name, svc, fmt.Sprintf("%s/%s", group, name), group, 0, env); err != nil {
			return err
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
		entry := &registry.ServerEntry{
			Name:  serverName,
			Repo:  name,
			Group: group,
			Port:  port,
		}
		if err := o.Register(pm.Proxy, entry); err != nil {
			slog.Error("register multi-port service", "name", serverName, "err", err)
		}
	}

	return nil
}

func (o *Orchestrator) launchProcess(ctx context.Context, name string, svc config.ServiceConfig, serverName, group string, port int, env []string) error {
	parts := strings.Fields(svc.Command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command for service %s", name)
	}

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Env = append(os.Environ(), env...)
	if svc.Dir != "" {
		cmd.Dir = svc.Dir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}

	ms := &ManagedService{
		Name:   serverName,
		Config: svc,
		Group:  group,
		PID:    cmd.Process.Pid,
		Port:   port,
		Status: "starting",
	}
	o.SetService(serverName, ms)

	go func() {
		err := cmd.Wait()
		status := "stopped"
		if err != nil {
			status = "failed"
			slog.Error("service exited", "name", serverName, "err", err)
		} else {
			slog.Info("service exited", "name", serverName)
		}
		o.UpdateServiceStatus(serverName, status)
	}()

	slog.Info("service started", "name", serverName, "pid", cmd.Process.Pid, "port", port)
	return nil
}

func buildEnv(configEnv map[string]string, portAssignments map[string]int, portMap envexpand.PortMap) ([]string, error) {
	var env []string
	for k, v := range configEnv {
		if v == "auto" {
			if port, ok := portAssignments[k]; ok {
				env = append(env, k+"="+strconv.Itoa(port))
			}
			continue
		}
		expanded, err := envexpand.Expand(v, portMap)
		if err != nil {
			return nil, fmt.Errorf("env %q: %w", k, err)
		}
		env = append(env, k+"="+expanded)
	}
	for k, v := range portAssignments {
		if _, exists := configEnv[k]; !exists {
			env = append(env, k+"="+strconv.Itoa(v))
		}
	}
	return env, nil
}

// DetectGroup returns the current git branch or fallback "default".
func DetectGroup(dir string) string {
	branch := detect.DetectBranch(dir)
	if branch == "" || branch == "unknown" {
		return "default"
	}
	return branch
}
