// Package envexpand resolves ${service.port}, ${service.env.VAR}, and
// ${@repo.service.key} references in mdp.yaml env values, with optional
// POSIX-style ${ref:-default} fallback syntax.
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

// Resolver resolves a cross-repo @-reference (e.g. ${@backend.api.port}).
// Implementations typically query the orchestrator for the named peer's port
// or env var. Returns (value, true) on success; (zero, false) if the peer is
// not available — the caller then uses the inline :-default if present, or
// records an unresolved-reference error.
type Resolver func(repo, svc string, isEnv bool, key string) (string, bool)

// refPattern matches the ${...} reference forms accepted in env values:
//
//	${svc.key}, ${svc.env.VAR}, ${@repo.svc.key}, ${@repo.svc.env.VAR}
//
// each optionally followed by :-default fallback text (anything up to '}').
//
//	group 2: repo name (without leading '@'), or empty for local refs
//	group 3: service name
//	group 4: literal "env." marker (empty for port refs)
//	group 5: key (port name or env-var name)
//	group 6: raw ":-default" (empty when no fallback)
//	group 7: default text only
var refPattern = regexp.MustCompile(`\$\{(@([A-Za-z0-9_-]+)\.)?([A-Za-z0-9_-]+)\.(env\.)?([A-Za-z0-9_]+)(:-([^}]*))?\}`)

// bareRefPattern is refPattern without the ${} wrapping or :-default support,
// anchored, for validating the bare ref form accepted by LookupRef.
var bareRefPattern = regexp.MustCompile(`^(@([A-Za-z0-9_-]+)\.)?([A-Za-z0-9_-]+)\.(env\.)?([A-Za-z0-9_]+)$`)

// Expand replaces ${service.key} port references in value using pm. Env-var
// references and cross-repo @-references are not permitted; either is treated
// as unresolved unless an inline :-default is supplied.
func Expand(value string, pm PortMap) (string, error) {
	return expandInternal(value, pm, nil, nil)
}

// ExpandAll replaces every ${svc.key} or ${svc.env.VAR} reference in value.
// - ${svc.key} resolves via pm (port lookup). Key "port" falls back to "PORT".
// - ${svc.env.VAR} resolves via em. A nil em rejects env-var references.
// Cross-repo @-references are not resolved here; use ExpandWith for those.
// Inline :-default fallbacks substitute when the underlying reference is
// unresolved.
func ExpandAll(value string, pm PortMap, em EnvMap) (string, error) {
	return expandInternal(value, pm, em, nil)
}

// ExpandWith is the most permissive form: it accepts all reference shapes,
// including cross-repo @-references that the supplied resolver looks up.
// A nil resolver makes @-references behave the same as in ExpandAll (rejected
// unless an inline :-default is present).
func ExpandWith(value string, pm PortMap, em EnvMap, resolver Resolver) (string, error) {
	return expandInternal(value, pm, em, resolver)
}

// LookupRef resolves a single bare reference string (no ${} wrapping) of the
// form "svc.key" or "svc.env.VAR". Used to implement the `ref:` config form.
func LookupRef(ref string, pm PortMap, em EnvMap) (string, error) {
	return LookupRefWith(ref, "", false, pm, em, nil)
}

// LookupRefWith resolves a bare reference, optionally substituting fallback
// when the reference cannot be resolved. resolver handles "@repo.svc.key"
// refs; pass nil to reject them.
func LookupRefWith(ref, fallback string, hasFallback bool, pm PortMap, em EnvMap, resolver Resolver) (string, error) {
	if !bareRefPattern.MatchString(ref) {
		return "", fmt.Errorf("invalid ref %q: must be svc.key, svc.env.VAR, or @repo.svc[.env].key", ref)
	}
	wrapped := "${" + ref
	if hasFallback {
		wrapped += ":-" + fallback
	}
	wrapped += "}"
	return expandInternal(wrapped, pm, em, resolver)
}

func expandInternal(value string, pm PortMap, em EnvMap, resolver Resolver) (string, error) {
	var firstErr error
	out := refPattern.ReplaceAllStringFunc(value, func(match string) string {
		m := refPattern.FindStringSubmatch(match)
		repo, svc, isEnv, key := m[2], m[3], m[4] != "", m[5]
		hasDefault := m[6] != ""
		defaultVal := m[7]

		fallback := func(reason string) string {
			if hasDefault {
				return defaultVal
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("unresolved reference %s: %s", match, reason)
			}
			return match
		}

		if repo != "" {
			if resolver == nil {
				return fallback(fmt.Sprintf("cross-repo (@%s.) references are not allowed here", repo))
			}
			val, ok := resolver(repo, svc, isEnv, key)
			if !ok {
				return fallback("peer not found")
			}
			return val
		}

		if isEnv {
			if em == nil {
				return fallback("env-var references are not allowed here")
			}
			vars, ok := em[svc]
			if !ok {
				return fallback("no such service")
			}
			val, ok := vars[key]
			if !ok {
				return fallback(fmt.Sprintf("service has no env var %q", key))
			}
			return val
		}

		port, ok := lookup(pm, svc, key)
		if !ok {
			return fallback("no such service or port")
		}
		return strconv.Itoa(port)
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// HasCrossRepoRef reports whether value contains any ${@repo.svc...} reference.
// Useful for callers that want to short-circuit the orchestrator query when a
// value has only local references.
func HasCrossRepoRef(value string) bool {
	matches := refPattern.FindAllStringSubmatch(value, -1)
	for _, m := range matches {
		if m[2] != "" {
			return true
		}
	}
	return false
}

// IsCrossRepoBareRef reports whether ref (the bare `ref:` form) targets a
// cross-repo peer (i.e. starts with @<repo>.).
func IsCrossRepoBareRef(ref string) bool {
	m := bareRefPattern.FindStringSubmatch(ref)
	return m != nil && m[2] != ""
}

// CrossRepoRef is a parsed @<repo>.<svc>[.env].<key> reference.
type CrossRepoRef struct {
	Repo  string
	Svc   string
	IsEnv bool
	Key   string
}

// ScanCrossRepoRefs returns every ${@repo.svc.key} reference embedded in
// value, in source order. Returns nil if none are present.
func ScanCrossRepoRefs(value string) []CrossRepoRef {
	var out []CrossRepoRef
	for _, m := range refPattern.FindAllStringSubmatch(value, -1) {
		if m[2] == "" {
			continue
		}
		out = append(out, CrossRepoRef{Repo: m[2], Svc: m[3], IsEnv: m[4] != "", Key: m[5]})
	}
	return out
}

// ParseCrossRepoBareRef parses a bare ref of the form @repo.svc[.env].key.
// Returns (ref, true) if it is a cross-repo bare ref; (zero, false) otherwise.
func ParseCrossRepoBareRef(ref string) (CrossRepoRef, bool) {
	m := bareRefPattern.FindStringSubmatch(ref)
	if m == nil || m[2] == "" {
		return CrossRepoRef{}, false
	}
	return CrossRepoRef{Repo: m[2], Svc: m[3], IsEnv: m[4] != "", Key: m[5]}, true
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
