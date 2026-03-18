package process

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

type RunOpts struct {
	ProxyURL     string
	ServerName   string
	AssignedPort int
	Scheme       string // "http" or "https" — auto-detected or from --tls-cert
	TLSCertPath  string // forwarded to proxy for dynamic TLS upgrade
	TLSKeyPath   string // forwarded to proxy for dynamic TLS upgrade
	ProxyTimeout time.Duration
}

type Manager struct{}

func New() *Manager { return &Manager{} }

func (m *Manager) Run(ctx context.Context, args []string, opts RunOpts) (int, error) {
	if len(args) == 0 {
		return -1, fmt.Errorf("no command specified")
	}
	if opts.ProxyTimeout == 0 {
		opts.ProxyTimeout = 2 * time.Second
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", opts.AssignedPort))
	SetProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("start %q: %w", args[0], err)
	}
	pid := cmd.Process.Pid
	slog.Info("started process", "pid", pid, "port", opts.AssignedPort, "name", opts.ServerName)

	if opts.ProxyURL != "" && opts.ServerName != "" {
		if err := registerWithProxy(opts.ProxyURL, opts, pid, opts.ProxyTimeout); err != nil {
			slog.Warn("failed to register with proxy", "err", err)
		} else {
			slog.Info("registered with proxy", "name", opts.ServerName, "port", opts.AssignedPort)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var exitErr error
	select {
	case sig := <-sigCh:
		slog.Info("received signal, stopping child", "signal", sig)
		deregisterFromProxy(opts.ProxyURL, opts.ServerName, opts.ProxyTimeout)
		KillProcessGroup(pid, 5*time.Second)
		exitErr = <-done
	case exitErr = <-done:
		slog.Info("child exited", "pid", pid)
		deregisterFromProxy(opts.ProxyURL, opts.ServerName, opts.ProxyTimeout)
	case <-ctx.Done():
		deregisterFromProxy(opts.ProxyURL, opts.ServerName, opts.ProxyTimeout)
		KillProcessGroup(pid, 5*time.Second)
		exitErr = <-done
	}

	if exitErr == nil {
		return 0, nil
	}
	if ee, ok := exitErr.(*exec.ExitError); ok {
		return ee.ExitCode(), nil
	}
	return -1, exitErr
}

func registerWithProxy(proxyURL string, opts RunOpts, pid int, timeout time.Duration) error {
	payload := map[string]any{"name": opts.ServerName, "port": opts.AssignedPort, "pid": pid}
	if opts.Scheme != "" {
		payload["scheme"] = opts.Scheme
	}
	if opts.TLSCertPath != "" {
		payload["tlsCertPath"] = opts.TLSCertPath
	}
	if opts.TLSKeyPath != "" {
		payload["tlsKeyPath"] = opts.TLSKeyPath
	}
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, proxyURL+"/__mdp/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("register returned %d", resp.StatusCode)
	}
	return nil
}

func deregisterFromProxy(proxyURL, name string, timeout time.Duration) {
	if proxyURL == "" || name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, proxyURL+"/__mdp/register/"+urlEncodeServerName(name), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Debug("deregister failed", "err", err)
		return
	}
	resp.Body.Close()
	slog.Info("deregistered from proxy", "name", name)
}

func urlEncodeServerName(name string) string {
	var b bytes.Buffer
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '/' || c == '%' || c == ' ' || c == '+' {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}
