// Package envexpand resolves ${service.port} references in mdp.yaml env values.
package envexpand

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// PortMap holds each service's named port assignments.
// Outer key is service name (as written in mdp.yaml).
// Inner key is the port name: "port"/"PORT" for single-port services, or the
// user-defined env key (from an "auto" entry) for multi-port services.
type PortMap map[string]map[string]int

var refPattern = regexp.MustCompile(`\$\{([A-Za-z0-9_-]+)\.([A-Za-z0-9_]+)\}`)

// Expand replaces every ${service.key} reference in value using pm.
// If key is "port" and the service has no entry named "port", "PORT" is tried.
// Returns an error if any reference cannot be resolved.
func Expand(value string, pm PortMap) (string, error) {
	var firstErr error
	out := refPattern.ReplaceAllStringFunc(value, func(match string) string {
		m := refPattern.FindStringSubmatch(match)
		svc, key := m[1], m[2]
		port, ok := lookup(pm, svc, key)
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("unresolved reference %s: no such service or port", match)
			}
			return match
		}
		return strconv.Itoa(port)
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

func lookup(pm PortMap, svc, key string) (int, bool) {
	ports, ok := pm[svc]
	if !ok {
		return 0, false
	}
	if port, ok := ports[key]; ok {
		return port, true
	}
	if strings.EqualFold(key, "port") {
		if port, ok := ports["PORT"]; ok {
			return port, true
		}
		if port, ok := ports["port"]; ok {
			return port, true
		}
	}
	return 0, false
}
