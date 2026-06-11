// Package health builds liveness probe closures for service entries.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/derekgould/multi-dev-proxy/internal/config"
)

const (
	tcpTimeout     = 2 * time.Second
	httpTimeout    = 3 * time.Second
	commandTimeout = 5 * time.Second
)

// Build returns a liveness probe for the given health-check config. When hc is
// nil, the probe defaults to a TCP dial of defaultPort on localhost. workDir
// is used as the working directory for command- and docker-style probes, so
// users can rely on the service's configured dir (e.g. to place a docker
// compose command in the right project).
func Build(hc *config.HealthCheck, defaultPort int, workDir string) func() bool {
	if hc == nil {
		return func() bool { return tcpProbe(defaultPort) }
	}
	switch {
	case hc.TCP > 0:
		port := hc.TCP
		return func() bool { return tcpProbe(port) }
	case hc.HTTP != "":
		url := hc.HTTP
		return func() bool { return httpProbe(url) }
	case hc.Command != "":
		cmd := hc.Command
		return func() bool { return commandProbe(cmd, workDir) }
	case len(hc.DockerServices) > 0:
		services := hc.DockerServices
		return func() bool { return dockerServicesProbe(workDir, services) }
	case hc.Docker:
		return func() bool { return dockerProbe(workDir) }
	}
	// Validated away at load time; fall back to the default TCP probe.
	return func() bool { return tcpProbe(defaultPort) }
}

func tcpProbe(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), tcpTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func httpProbe(url string) bool {
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

func commandProbe(cmdLine, workDir string) bool {
	parts, err := splitArgs(cmdLine)
	if err != nil || len(parts) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, parts[0], parts[1:]...)
	c.Dir = workDir
	return c.Run() == nil
}

func dockerProbe(workDir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "docker", "compose", "ps", "-q")
	c.Dir = workDir
	out, err := c.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// composeContainer is the subset of `docker compose ps --format json` output
// the docker-services probe needs.
type composeContainer struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

// dockerServicesProbe is healthy when every named compose service has at least
// one container running, and each of its containers reports healthy if it
// defines a HEALTHCHECK. Requires Docker Compose v2 (`--format json`).
//
// Deliberately not `ps -a`: that would include exited one-off containers from
// `docker compose run` under the same service name, failing the probe even
// when the real stack is healthy. A named service with no running container
// simply has no row, which reads as not-ready.
func dockerServicesProbe(workDir string, services []string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	args := append([]string{"compose", "ps", "--format", "json"}, services...)
	c := exec.CommandContext(ctx, "docker", args...)
	c.Dir = workDir
	out, err := c.Output()
	if err != nil {
		return false
	}
	rows, err := parseComposePS(out)
	if err != nil {
		return false
	}
	return composeServicesReady(rows, services)
}

// parseComposePS handles both `docker compose ps --format json` output shapes:
// a JSON array (compose < v2.21) and NDJSON, one object per line (>= v2.21).
func parseComposePS(out []byte) ([]composeContainer, error) {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var rows []composeContainer
		if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
			return nil, err
		}
		return rows, nil
	}
	var rows []composeContainer
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row composeContainer
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// composeServicesReady reports whether every requested service has at least
// one container and all of its containers (replicas) are running and, when a
// HEALTHCHECK is defined, healthy.
func composeServicesReady(rows []composeContainer, services []string) bool {
	for _, svc := range services {
		found := false
		for _, row := range rows {
			if row.Service != svc {
				continue
			}
			found = true
			if row.State != "running" {
				return false
			}
			if row.Health != "" && row.Health != "healthy" {
				return false
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// splitArgs tokenizes a command line honoring single and double quotes. Kept
// local to avoid an import cycle with internal/orchestrator, where the same
// logic lives as SplitHookArgs.
func splitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	var inSingle, inDouble, hasTok bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(c)
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			} else {
				cur.WriteByte(c)
			}
		case c == '\'':
			inSingle = true
			hasTok = true
		case c == '"':
			inDouble = true
			hasTok = true
		case c == ' ' || c == '\t':
			if hasTok {
				args = append(args, cur.String())
				cur.Reset()
				hasTok = false
			}
		default:
			cur.WriteByte(c)
			hasTok = true
		}
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("unterminated quote in %q", s)
	}
	if hasTok {
		args = append(args, cur.String())
	}
	return args, nil
}
