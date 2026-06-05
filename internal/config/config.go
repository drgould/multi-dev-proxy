package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/derekgould/multi-dev-proxy/internal/envexpand"
	"gopkg.in/yaml.v3"
)

// Config is the top-level mdp.yaml configuration.
type Config struct {
	Services  map[string]ServiceConfig `yaml:"services"`
	PortRange string                   `yaml:"port_range"`
	// StablePorts toggles per-branch stable port reuse. Nil (absent) means on;
	// set `stable_ports: false` to disable. See StablePortsEnabled.
	StablePorts *bool        `yaml:"stable_ports"`
	Global      GlobalConfig `yaml:"global"`
	// Inputs declares values prompted for by `mdp run -i` and referenced
	// elsewhere as ${inputs.<name>}. Declaration order is preserved so prompts
	// appear in a stable order.
	Inputs Inputs `yaml:"inputs"`
	// Links maps a peer repo name to the group its services run in, mirroring
	// the repeatable `--link repo=group` CLI flag. A value may reference an
	// input (e.g. ${inputs.api_branch}). CLI `--link` overrides config links
	// per repo.
	Links map[string]string `yaml:"links"`
}

// StablePortsEnabled reports whether per-branch stable port reuse is active.
// It defaults to on; set `stable_ports: false` in mdp.yaml to disable.
func (c *Config) StablePortsEnabled() bool {
	return c.StablePorts == nil || *c.StablePorts
}

// GlobalConfig holds project-wide settings that aren't tied to a single service.
type GlobalConfig struct {
	// EnvFile, if non-empty, is a path where an aggregate .env file is written
	// at startup. Values are resolved from Env (below).
	EnvFile string `yaml:"env_file"`
	// Env is an explicit map of env vars to write to EnvFile. See EnvValue.
	Env map[string]EnvValue `yaml:"env"`
}

// EnvValue is one entry in either a global or per-service `env:` map.
//
// In YAML it accepts two shapes:
//   - a scalar string — placed in Value and treated as a literal (with ${...}
//     interpolation applied at resolve time)
//   - a mapping with `ref:` and optional `default:` keys — placed in Ref
//     (a bare reference like "svc.port" or "@repo.svc.env.VAR") and Default
//     (used as a fallback when ref cannot be resolved)
type EnvValue struct {
	Value   string  // set when the YAML entry is a scalar string
	Ref     string  // set when the YAML entry is a mapping with `ref:`
	Default *string // optional fallback used when Ref cannot be resolved (nil = absent)
}

// UnmarshalYAML accepts either a scalar string or a mapping with `ref:` and
// optional `default:` keys. Any other shape is a parse error so typos surface
// early.
func (g *EnvValue) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		g.Value = node.Value
		return nil
	case yaml.MappingNode:
		var sawRef bool
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]
			switch keyNode.Value {
			case "ref":
				var refStr string
				if err := valNode.Decode(&refStr); err != nil {
					return fmt.Errorf("line %d: `ref:` value must be a string: %w", valNode.Line, err)
				}
				if refStr == "" {
					return fmt.Errorf("line %d: `ref:` value must not be empty", valNode.Line)
				}
				g.Ref = refStr
				sawRef = true
			case "default":
				var defStr string
				if err := valNode.Decode(&defStr); err != nil {
					return fmt.Errorf("line %d: `default:` value must be a string: %w", valNode.Line, err)
				}
				g.Default = &defStr
			default:
				return fmt.Errorf("line %d: unknown key %q in env entry (only `ref` and `default` are supported)", keyNode.Line, keyNode.Value)
			}
		}
		if !sawRef {
			return fmt.Errorf("line %d: env mapping must include `ref:`", node.Line)
		}
		return nil
	default:
		return fmt.Errorf("line %d: env entry must be a string or mapping with `ref:`", node.Line)
	}
}

// HasDefault reports whether a fallback was explicitly set in YAML.
func (g EnvValue) HasDefault() bool { return g.Default != nil }

// DefaultValue returns the fallback string, or "" if no default was set.
// Use HasDefault to distinguish the absent case.
func (g EnvValue) DefaultValue() string {
	if g.Default == nil {
		return ""
	}
	return *g.Default
}

// InputSpec is one declared input under the top-level `inputs:` mapping. Its
// resolved value (prompted via `mdp run -i`, or the Default otherwise) is
// referenced elsewhere as ${inputs.<Name>}.
type InputSpec struct {
	Name       string // the mapping key
	Prompt     string // question shown when prompting; defaults to Name
	Default    string // fallback when not prompting or the answer is empty
	HasDefault bool   // whether `default:` was present (distinguishes `default: ""` from absent)
	Choices    string // optional; "groups" => live orchestrator group pick-list
}

// Inputs is an ordered list of declared inputs. YAML mappings lose key order,
// so UnmarshalYAML walks the mapping node directly to preserve the declaration
// order used when prompting.
type Inputs []InputSpec

// UnmarshalYAML decodes the `inputs:` mapping, preserving declaration order and
// rejecting unknown spec keys so typos surface at load.
func (in *Inputs) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: inputs must be a mapping of name -> spec", node.Line)
	}
	specs := make(Inputs, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]
		if keyNode.Value == "" {
			return fmt.Errorf("line %d: input name must not be empty", keyNode.Line)
		}
		if valNode.Kind != yaml.MappingNode {
			return fmt.Errorf("line %d: input %q must be a mapping with prompt/default/choices keys", valNode.Line, keyNode.Value)
		}
		spec := InputSpec{Name: keyNode.Value}
		for j := 0; j+1 < len(valNode.Content); j += 2 {
			k := valNode.Content[j]
			v := valNode.Content[j+1]
			// A bare `default:` (null node) means "no default"; only an explicit
			// scalar (including `default: ""`) sets one. This keeps null distinct
			// from the empty string so the non-interactive "no default" error
			// still fires for `default:` written with no value.
			if k.Value == "default" && v.Tag == "!!null" {
				continue
			}
			var dst *string
			switch k.Value {
			case "prompt":
				dst = &spec.Prompt
			case "default":
				dst = &spec.Default
				spec.HasDefault = true
			case "choices":
				dst = &spec.Choices
			default:
				return fmt.Errorf("line %d: unknown key %q in input %q (only `prompt`, `default`, and `choices` are supported)", k.Line, k.Value, keyNode.Value)
			}
			if err := v.Decode(dst); err != nil {
				return fmt.Errorf("line %d: input %q %s: %w", v.Line, keyNode.Value, k.Value, err)
			}
		}
		specs = append(specs, spec)
	}
	*in = specs
	return nil
}

// ServiceConfig defines a single service in the config file.
type ServiceConfig struct {
	Command  string              `yaml:"command"`
	Setup    []string            `yaml:"setup"`    // commands run sequentially before Command
	Shutdown []string            `yaml:"shutdown"` // commands run sequentially after Command exits
	Dir      string              `yaml:"dir"`
	Proxy    int                 `yaml:"proxy"`
	Port     int                 `yaml:"port"`
	Group    string              `yaml:"group"`
	Scheme   string              `yaml:"scheme"`   // "http" or "https"; defaults to "http"
	TLSCert  string              `yaml:"tls_cert"` // path to TLS certificate file
	TLSKey   string              `yaml:"tls_key"`  // path to TLS key file
	EnvFile  string              `yaml:"env_file"` // optional path for exported .env file
	Env      map[string]EnvValue `yaml:"env"`
	Ports    []PortMapping       `yaml:"ports"`

	// LogSplit, if set, enables per-sub-service log demultiplexing on the
	// service's stdout/stderr. See LogSplitConfig for the accepted shapes.
	LogSplit LogSplitConfig `yaml:"log_split"`

	// DependsOn names other services that must be ready before this service
	// starts. Names must match keys in the top-level services map.
	DependsOn []string `yaml:"depends_on"`

	// HealthCheck customizes the liveness probe used to decide whether the
	// service is still up after its command has exited. Nil falls back to
	// a TCP probe on the service's registered port.
	HealthCheck *HealthCheck `yaml:"health_check"`
}

// HealthCheck customizes the liveness probe used to decide whether a service
// is still up. Exactly one of TCP, HTTP, Command, or Docker must be set.
type HealthCheck struct {
	TCP     int    `yaml:"tcp"`     // TCP-dial localhost on this port
	HTTP    string `yaml:"http"`    // HTTP GET on this absolute URL; 2xx/3xx = healthy
	Command string `yaml:"command"` // shell tokens (same rules as service.command); exit 0 = healthy
	Docker  bool   `yaml:"-"`       // set via the scalar shorthand `health_check: docker`
}

// UnmarshalYAML accepts either a scalar shorthand (currently only "docker") or
// a mapping with tcp/http/command fields.
func (h *HealthCheck) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Value != "docker" {
			return fmt.Errorf("line %d: unknown health_check shorthand %q (only \"docker\" is supported)", node.Line, node.Value)
		}
		h.Docker = true
		return nil
	case yaml.MappingNode:
		type raw HealthCheck
		return node.Decode((*raw)(h))
	default:
		return fmt.Errorf("line %d: health_check must be a string or mapping", node.Line)
	}
}

// Validate checks that exactly one variant is set.
func (h *HealthCheck) Validate() error {
	set := 0
	if h.TCP > 0 {
		set++
	}
	if h.HTTP != "" {
		set++
	}
	if h.Command != "" {
		set++
	}
	if h.Docker {
		set++
	}
	if set == 0 {
		return fmt.Errorf("health_check: must set one of tcp, http, command, or the \"docker\" shorthand")
	}
	if set > 1 {
		return fmt.Errorf("health_check: only one of tcp, http, command, or docker may be set")
	}
	return nil
}

// PortMapping declares one auto-assigned port for a multi-port service.
// Proxy is optional: omit it for non-HTTP ports (databases, caches, etc.) that
// need a free port allocated for ${svc.env} interpolation but should not be
// registered with an HTTP reverse-proxy listener.
//
// All proxy-bearing ports of a service register under the parent service's
// key (`<group>/<service-key>`); individual ports are addressed by env-var
// key via `@<repo>.<svc>.env.<KEY>` for cross-repo refs.
//
// Name is no longer supported; it's kept on the struct purely to detect
// stale configs at Load time and emit a migration error. Remove on the
// next major bump.
type PortMapping struct {
	Env      string `yaml:"env"`
	Proxy    int    `yaml:"proxy"`
	Protocol string `yaml:"protocol"` // "tcp" (default) or "udp"
	Name     string `yaml:"name"`     // removed; Load errors when set
}

// EnvProtocols returns a {env var → normalized protocol} map for every entry
// in Ports. Empty protocols are normalized to "tcp". Allocators use this to
// decide whether a port should be verified via TCP or UDP probes.
func (s ServiceConfig) EnvProtocols() map[string]string {
	if len(s.Ports) == 0 {
		return nil
	}
	m := make(map[string]string, len(s.Ports))
	for _, pm := range s.Ports {
		proto := strings.ToLower(pm.Protocol)
		if proto == "" {
			proto = "tcp"
		}
		m[pm.Env] = proto
	}
	return m
}

// Load reads and parses the config file at the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.PortRange == "" {
		cfg.PortRange = "10000-60000"
	}
	dir := filepath.Dir(path)
	for name, svc := range cfg.Services {
		// Paths containing ${inputs.X} are resolved later by FinalizePaths,
		// after inputs are substituted — resolving a placeholder now would join
		// a relative literal to the config dir (or miss ~-expansion) for input
		// values that are themselves absolute or ~-prefixed.
		if !containsInputRef(svc.Dir) {
			svc.Dir = resolvePath(svc.Dir, dir)
		}
		svc.TLSCert = resolvePath(svc.TLSCert, dir)
		svc.TLSKey = resolvePath(svc.TLSKey, dir)
		// Per-service env_file is resolved against the service's (already
		// absolute) dir; fall back to the config dir when dir is empty. Defer
		// when env_file or its base dir still carries an input ref.
		if !containsInputRef(svc.EnvFile) && !containsInputRef(svc.Dir) {
			envFileBase := svc.Dir
			if envFileBase == "" {
				envFileBase = dir
			}
			svc.EnvFile = resolvePath(svc.EnvFile, envFileBase)
		}
		// Infer scheme from cert presence.
		if svc.Scheme == "" && svc.TLSCert != "" {
			svc.Scheme = "https"
		}
		// Normalize + validate port mapping protocols, and reject same-proxy
		// duplicates within a service. Without this check, two ports declaring
		// the same `proxy:` would silently overwrite each other in the registry
		// (entries are keyed by service name, which is shared across a
		// service's ports).
		seenProxy := map[int]string{}
		for i, pm := range svc.Ports {
			if pm.Name != "" {
				return nil, fmt.Errorf("service %q: port %q sets removed `name:` field — drop it; multi-port services now register under the parent service key (e.g. \"<group>/%s\"). See docs/mdp-yaml-reference.md#ports--multi-port-services", name, pm.Env, name)
			}
			proto := strings.ToLower(pm.Protocol)
			switch proto {
			case "", "tcp":
				// ok
			case "udp":
				if pm.Proxy > 0 {
					return nil, fmt.Errorf("service %q: protocol: udp is incompatible with a non-zero proxy port (env %q)", name, pm.Env)
				}
			default:
				return nil, fmt.Errorf("service %q: unknown protocol %q for port mapping %q (expected \"tcp\" or \"udp\")", name, pm.Protocol, pm.Env)
			}
			if pm.Proxy > 0 {
				if prev, ok := seenProxy[pm.Proxy]; ok {
					return nil, fmt.Errorf("service %q: ports %q and %q both declare proxy: %d (one proxy per service-port)", name, prev, pm.Env, pm.Proxy)
				}
				seenProxy[pm.Proxy] = pm.Env
			}
			svc.Ports[i].Protocol = proto
		}
		if svc.HealthCheck != nil {
			if err := svc.HealthCheck.Validate(); err != nil {
				return nil, fmt.Errorf("service %q: %w", name, err)
			}
		}
		if err := svc.LogSplit.Validate(); err != nil {
			return nil, fmt.Errorf("service %q: %w", name, err)
		}
		cfg.Services[name] = svc
	}
	if !containsInputRef(cfg.Global.EnvFile) {
		cfg.Global.EnvFile = resolvePath(cfg.Global.EnvFile, dir)
	}
	if err := validateDependencies(cfg.Services); err != nil {
		return nil, err
	}
	if err := validateInputs(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// containsInputRef reports whether s carries a ${inputs.X} placeholder, whose
// resolution is deferred until inputs are substituted.
func containsInputRef(s string) bool {
	return strings.Contains(s, "${inputs.")
}

// FinalizePaths resolves the service Dir/EnvFile and global EnvFile that Load
// left raw because they contained ${inputs.X}. Call it after inputs are
// substituted (see VisitInputRefFields). resolvePath is idempotent on already
// absolute paths, so paths Load already resolved are unchanged — meaning this
// is a no-op for configs that use no inputs in path fields. configDir is the
// directory containing the mdp.yaml.
func (cfg *Config) FinalizePaths(configDir string) {
	for name, svc := range cfg.Services {
		svc.Dir = resolvePath(svc.Dir, configDir)
		base := svc.Dir
		if base == "" {
			base = configDir
		}
		svc.EnvFile = resolvePath(svc.EnvFile, base)
		cfg.Services[name] = svc
	}
	cfg.Global.EnvFile = resolvePath(cfg.Global.EnvFile, configDir)
}

// inputNamePattern is the accepted syntax for input names. It must match the
// key syntax that envexpand's ${inputs.NAME} reference regex supports, so a
// declared input is always referenceable (and any ${inputs.X} typo is caught
// as an undeclared reference rather than silently surviving substitution).
var inputNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// validateInputs checks the `inputs:` and `links:` sections: input names are
// valid and unique, `choices` is empty or "groups", defaults are plain
// literals, the reserved service name "inputs" is unused, and every
// ${inputs.X} reference names a declared input. The reference scan drives
// VisitInputRefFields, so it covers exactly the fields where substitution
// applies — the two cannot drift apart — and in a deterministic order, so the
// reported error is stable when several refs are undeclared.
func validateInputs(cfg *Config) error {
	declared := make(map[string]bool, len(cfg.Inputs))
	for _, in := range cfg.Inputs {
		if in.Name == "" {
			return fmt.Errorf("input name must not be empty")
		}
		if !inputNamePattern.MatchString(in.Name) {
			return fmt.Errorf("input name %q is invalid: only letters, digits, and underscore are allowed", in.Name)
		}
		if declared[in.Name] {
			return fmt.Errorf("input %q declared more than once", in.Name)
		}
		declared[in.Name] = true
		switch in.Choices {
		case "", "groups":
		default:
			return fmt.Errorf("input %q: unknown choices %q (only \"groups\" is supported)", in.Name, in.Choices)
		}
		if in.HasDefault && strings.Contains(in.Default, "${") {
			return fmt.Errorf("input %q: default must be a plain literal (it cannot contain ${...} references)", in.Name)
		}
	}
	if _, ok := cfg.Services["inputs"]; ok {
		return fmt.Errorf("service name \"inputs\" is reserved (it collides with ${inputs.*} references)")
	}

	var firstErr error
	cfg.VisitInputRefFields(func(where, value string) (string, bool) {
		if firstErr != nil {
			return value, false
		}
		if bad := envexpand.InvalidInputRefs(value); len(bad) > 0 {
			firstErr = fmt.Errorf("%s has malformed input reference %s (input names allow only letters, digits, and underscore; a :-fallback cannot contain a nested ${...})", where, bad[0])
			return value, false
		}
		for _, name := range envexpand.ScanInputRefs(value) {
			if !declared[name] {
				firstErr = fmt.Errorf("%s references undeclared input ${inputs.%s}", where, name)
				break
			}
		}
		return value, false // validation never rewrites
	})
	if firstErr != nil {
		return firstErr
	}

	// Reject ${inputs.X} in fields that don't support substitution, so a natural
	// mistake (e.g. `group: ${inputs.branch}`) fails clearly at load instead of
	// surviving as a literal. These are the plausible branch/group/path-shaped
	// fields outside VisitInputRefFields' supported set.
	svcNames := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		svcNames = append(svcNames, name)
	}
	sort.Strings(svcNames)
	for _, name := range svcNames {
		svc := cfg.Services[name]
		for _, f := range []struct{ label, val string }{
			{"group", svc.Group},
			{"tls_cert", svc.TLSCert},
			{"tls_key", svc.TLSKey},
		} {
			if strings.Contains(f.val, "${inputs.") {
				return fmt.Errorf("service %q %s does not support ${inputs.X} references", name, f.label)
			}
		}
	}
	return nil
}

// VisitInputRefFields walks, in deterministic order, every config field where
// ${inputs.X} references are supported — a service's command, dir, setup,
// shutdown, env (value/ref/default), and env_file; the global env block and
// env_file; and links values — calling visit(where, value). When visit returns
// (newValue, true), the field is rewritten in place. Load-time validation and
// run-time substitution both drive this single walk, so the supported-field set
// cannot drift between them.
func (cfg *Config) VisitInputRefFields(visit func(where, value string) (string, bool)) {
	apply := func(where, value string, set func(string)) {
		if nv, changed := visit(where, value); changed {
			set(nv)
		}
	}
	visitEnv := func(where string, env map[string]EnvValue) {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			e := env[k]
			apply(fmt.Sprintf("%s env %q", where, k), e.Value, func(v string) { e.Value = v })
			apply(fmt.Sprintf("%s env %q ref", where, k), e.Ref, func(v string) { e.Ref = v })
			if e.Default != nil {
				apply(fmt.Sprintf("%s env %q default", where, k), *e.Default, func(v string) { e.Default = &v })
			}
			env[k] = e
		}
	}

	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		svc := cfg.Services[name]
		where := fmt.Sprintf("service %q", name)
		apply(where+" command", svc.Command, func(v string) { svc.Command = v })
		apply(where+" dir", svc.Dir, func(v string) { svc.Dir = v })
		for i := range svc.Setup {
			apply(fmt.Sprintf("%s setup[%d]", where, i), svc.Setup[i], func(v string) { svc.Setup[i] = v })
		}
		for i := range svc.Shutdown {
			apply(fmt.Sprintf("%s shutdown[%d]", where, i), svc.Shutdown[i], func(v string) { svc.Shutdown[i] = v })
		}
		visitEnv(where, svc.Env)
		apply(where+" env_file", svc.EnvFile, func(v string) { svc.EnvFile = v })
		cfg.Services[name] = svc
	}
	visitEnv("global", cfg.Global.Env)
	apply("global env_file", cfg.Global.EnvFile, func(v string) { cfg.Global.EnvFile = v })

	repos := make([]string, 0, len(cfg.Links))
	for repo := range cfg.Links {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	for _, repo := range repos {
		apply(fmt.Sprintf("link %q", repo), cfg.Links[repo], func(v string) { cfg.Links[repo] = v })
	}
}

// resolvePath expands a leading "~" and joins relative paths against base.
// Returns an empty string unchanged.
func resolvePath(p, base string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				p = home
			} else if strings.HasPrefix(p, "~/") {
				p = filepath.Join(home, p[2:])
			}
		}
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

// validateDependencies checks that every name in each service's depends_on
// refers to a defined service, and that the dependency graph has no cycles.
func validateDependencies(services map[string]ServiceConfig) error {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		for _, dep := range services[name].DependsOn {
			if _, ok := services[dep]; !ok {
				return fmt.Errorf("service %q: unknown dependency %q", name, dep)
			}
		}
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(services))
	var path []string
	var visit func(string) error
	visit = func(n string) error {
		color[n] = gray
		path = append(path, n)
		deps := append([]string(nil), services[n].DependsOn...)
		sort.Strings(deps)
		for _, dep := range deps {
			switch color[dep] {
			case gray:
				cycle := append([]string(nil), path...)
				cycle = append(cycle, dep)
				start := 0
				for i, v := range cycle {
					if v == dep {
						start = i
						break
					}
				}
				return fmt.Errorf("dependency cycle: %s", strings.Join(cycle[start:], " -> "))
			case white:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		path = path[:len(path)-1]
		color[n] = black
		return nil
	}
	for _, name := range names {
		if color[name] == white {
			if err := visit(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// LogSplitConfig describes how a service's combined log stream should be
// demultiplexed into per-sub-service lanes. YAML accepts two shapes:
//
//	log_split: compose                           # built-in shorthand
//	log_split:
//	  regex: '^\[(?P<name>[^\]]+)\]\s*(?P<msg>.*)$'   # user-supplied pattern
//
// The regex form must have named captures `name` and `msg`.
type LogSplitConfig struct {
	Mode  string // "", "compose", or "regex"
	Regex string // pattern when Mode == "regex"
}

// UnmarshalYAML accepts the scalar ("compose") and mapping (`regex:`) forms.
func (l *LogSplitConfig) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Value == "" {
			return nil
		}
		if node.Value != "compose" {
			return fmt.Errorf("line %d: unknown log_split shorthand %q (only \"compose\" is supported as a scalar; use a mapping with `regex:` for custom patterns)", node.Line, node.Value)
		}
		l.Mode = "compose"
		return nil
	case yaml.MappingNode:
		var raw struct {
			Regex string `yaml:"regex"`
		}
		if err := node.Decode(&raw); err != nil {
			return fmt.Errorf("line %d: %w", node.Line, err)
		}
		if raw.Regex == "" {
			return fmt.Errorf("line %d: log_split mapping requires `regex:` key", node.Line)
		}
		l.Mode = "regex"
		l.Regex = raw.Regex
		return nil
	default:
		return fmt.Errorf("line %d: log_split must be a string or mapping", node.Line)
	}
}

// Validate checks that the config is internally consistent. For regex mode
// it compiles the pattern and verifies it contains `name` and `msg` captures.
func (l *LogSplitConfig) Validate() error {
	switch l.Mode {
	case "", "compose":
		return nil
	case "regex":
		re, err := regexp.Compile(l.Regex)
		if err != nil {
			return fmt.Errorf("log_split: invalid regex: %w", err)
		}
		var hasName, hasMsg bool
		for _, n := range re.SubexpNames() {
			switch n {
			case "name":
				hasName = true
			case "msg":
				hasMsg = true
			}
		}
		if !hasName || !hasMsg {
			return fmt.Errorf("log_split: regex must contain named captures `name` and `msg`")
		}
		return nil
	default:
		return fmt.Errorf("log_split: unknown mode %q", l.Mode)
	}
}

// ParseLogSplitFlag converts the `--log-split` CLI flag value into a
// LogSplitConfig. Accepts "", "compose", or "regex:<pattern>".
func ParseLogSplitFlag(v string) (LogSplitConfig, error) {
	switch {
	case v == "":
		return LogSplitConfig{}, nil
	case v == "compose":
		return LogSplitConfig{Mode: "compose"}, nil
	case strings.HasPrefix(v, "regex:"):
		cfg := LogSplitConfig{Mode: "regex", Regex: strings.TrimPrefix(v, "regex:")}
		if err := cfg.Validate(); err != nil {
			return LogSplitConfig{}, err
		}
		return cfg, nil
	default:
		return LogSplitConfig{}, fmt.Errorf("--log-split: unknown value %q (expected \"compose\" or \"regex:<pattern>\")", v)
	}
}

// Find looks for mdp.yaml in the given directory, then walks up to the root.
// Returns the path if found, or empty string.
func Find(startDir string) string {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return ""
	}
	for {
		path := filepath.Join(dir, "mdp.yaml")
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
