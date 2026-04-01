package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/derekgould/multi-dev-proxy/internal/config"
	"github.com/derekgould/multi-dev-proxy/internal/detect"
	"github.com/derekgould/multi-dev-proxy/internal/ports"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

// StartConfigServices starts all services from the config under the given group name.
func (o *Orchestrator) StartConfigServices(ctx context.Context, group string) error {
	if o.cfg == nil || len(o.cfg.Services) == 0 {
		return nil
	}
	portRange, err := ports.ParseRange(o.cfg.PortRange)
	if err != nil {
		return fmt.Errorf("invalid port_range: %w", err)
	}

	for name, svc := range o.cfg.Services {
		if err := o.startConfigService(ctx, name, svc, group, portRange); err != nil {
			slog.Error("failed to start service", "name", name, "err", err)
		}
	}
	return nil
}

func (o *Orchestrator) startConfigService(ctx context.Context, name string, svc config.ServiceConfig, group string, portRange ports.PortRange) error {
	if svc.Command == "" && svc.Port == 0 {
		return nil
	}

	if len(svc.Ports) > 0 {
		return o.startMultiPortService(ctx, name, svc, group, portRange)
	}

	return o.startSingleService(ctx, name, svc, group, portRange)
}

func (o *Orchestrator) startSingleService(ctx context.Context, name string, svc config.ServiceConfig, group string, portRange ports.PortRange) error {
	serverName := fmt.Sprintf("%s/%s", group, name)

	var assignedPort int
	if svc.Port > 0 {
		assignedPort = svc.Port
	} else {
		var err error
		assignedPort, err = ports.FindFreePort(portRange, nil)
		if err != nil {
			return fmt.Errorf("find free port for %s: %w", name, err)
		}
	}

	if svc.Command != "" {
		env := buildEnv(svc.Env, map[string]int{"PORT": assignedPort})
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

func (o *Orchestrator) startMultiPortService(ctx context.Context, name string, svc config.ServiceConfig, group string, portRange ports.PortRange) error {
	portAssignments := make(map[string]int)
	for envName, value := range svc.Env {
		if value == "auto" {
			port, err := ports.FindFreePort(portRange, nil)
			if err != nil {
				return fmt.Errorf("find free port for %s.%s: %w", name, envName, err)
			}
			portAssignments[envName] = port
		}
	}

	if svc.Command != "" {
		env := buildEnv(svc.Env, portAssignments)
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
		Status: "running",
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

func buildEnv(configEnv map[string]string, portAssignments map[string]int) []string {
	var env []string
	for k, v := range configEnv {
		if v == "auto" {
			if port, ok := portAssignments[k]; ok {
				env = append(env, k+"="+strconv.Itoa(port))
			}
		} else {
			env = append(env, k+"="+v)
		}
	}
	for k, v := range portAssignments {
		if _, exists := configEnv[k]; !exists {
			env = append(env, k+"="+strconv.Itoa(v))
		}
	}
	return env
}

// DetectGroup returns the current git branch or fallback "default".
func DetectGroup(dir string) string {
	branch := detect.DetectBranch(dir)
	if branch == "" || branch == "unknown" {
		return "default"
	}
	return branch
}
