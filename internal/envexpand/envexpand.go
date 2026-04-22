// Package envexpand resolves ${service.port} and ${service.env.VAR} references
// in mdp.yaml env values.
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

// EnvMap holds each service's resolved env vars (name → value).
// Used to resolve ${service.env.VAR} references.
type EnvMap map[string]map[string]string

// refPattern matches ${svc.key} (port lookup) or ${svc.env.VAR} (env var
// lookup). Group 2 is "env." when present; group 3 is the final segment.
var refPattern = regexp.MustCompile(`\$\{([A-Za-z0-9_-]+)\.(env\.)?([A-Za-z0-9_]+)\}`)

// bareRefPattern is refPattern without the ${} wrapping, anchored, for
// validating the bare ref form accepted by LookupRef.
var bareRefPattern = regexp.MustCompile(`^([A-Za-z0-9_-]+)\.(env\.)?([A-Za-z0-9_]+)$`)

// Expand replaces ${service.key} port references in value using pm. It does
// NOT resolve ${service.env.VAR} — any such reference is an error. Use
// ExpandAll when env-var references are allowed.
func Expand(value string, pm PortMap) (string, error) {
	return ExpandAll(value, pm, nil)
}

// ExpandAll replaces every ${svc.key} or ${svc.env.VAR} reference in value.
// - ${svc.key} resolves via pm (port lookup). Key "port" falls back to "PORT".
// - ${svc.env.VAR} resolves via em. A nil em disables env-var lookups and any
//   such reference is treated as unresolved.
// Returns an error if any reference cannot be resolved.
func ExpandAll(value string, pm PortMap, em EnvMap) (string, error) {
	var firstErr error
	out := refPattern.ReplaceAllStringFunc(value, func(match string) string {
		m := refPattern.FindStringSubmatch(match)
		svc, isEnv, key := m[1], m[2] != "", m[3]
		if isEnv {
			if em == nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("unresolved reference %s: env-var references are not allowed here", match)
				}
				return match
			}
			vars, ok := em[svc]
			if !ok {
				if firstErr == nil {
					firstErr = fmt.Errorf("unresolved reference %s: no such service", match)
				}
				return match
			}
			val, ok := vars[key]
			if !ok {
				if firstErr == nil {
					firstErr = fmt.Errorf("unresolved reference %s: service has no env var %q", match, key)
				}
				return match
			}
			return val
		}
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

// LookupRef resolves a single bare reference string (no ${} wrapping) of the
// form "svc.env.VAR" or "svc.key". Used to implement the `ref:` config form.
func LookupRef(ref string, pm PortMap, em EnvMap) (string, error) {
	if !bareRefPattern.MatchString(ref) {
		return "", fmt.Errorf("invalid ref %q: must be svc.key or svc.env.VAR", ref)
	}
	// Wrap and reuse the same resolver — guarantees identical error messages.
	return ExpandAll("${"+ref+"}", pm, em)
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
