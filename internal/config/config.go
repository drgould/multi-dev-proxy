package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level mdp.yaml configuration.
type Config struct {
	Services  map[string]ServiceConfig `yaml:"services"`
	PortRange string                   `yaml:"port_range"`
	Global    GlobalConfig             `yaml:"global"`
}

// GlobalConfig holds project-wide settings that aren't tied to a single service.
type GlobalConfig struct {
	// EnvFile, if non-empty, is a path where an aggregate .env file is written
	// at startup. Values are resolved from Env (below).
	EnvFile string `yaml:"env_file"`
	// Env is an explicit map of env vars to write to EnvFile. Values may be
	// scalar strings (supporting ${svc.key} and ${svc.env.VAR} interpolation)
	// or mappings with a single `ref:` key that pass through another service's
	// env var or port without string-wrapping it.
	Env map[string]GlobalEnvValue `yaml:"env"`
}

// GlobalEnvValue is either a literal value (possibly with ${...} refs) or a
// pass-through reference to another service's env var or port.
type GlobalEnvValue struct {
	Value string // set when the YAML entry is a scalar string
	Ref   string // set when the YAML entry is a mapping with `ref:`
}

// UnmarshalYAML accepts either a scalar string or a mapping with a single
// `ref:` key. Any other shape is a parse error so typos surface early.
func (g *GlobalEnvValue) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		g.Value = node.Value
		return nil
	case yaml.MappingNode:
		if len(node.Content) != 2 {
			return fmt.Errorf("line %d: global env mapping must have exactly one key (`ref`)", node.Line)
		}
		key := node.Content[0].Value
		if key != "ref" {
			return fmt.Errorf("line %d: unknown key %q in global env entry (only `ref` is supported)", node.Line, key)
		}
		val := node.Content[1]
		var refStr string
		if err := val.Decode(&refStr); err != nil {
			return fmt.Errorf("line %d: `ref:` value must be a string: %w", val.Line, err)
		}
		if refStr == "" {
			return fmt.Errorf("line %d: `ref:` value must not be empty", val.Line)
		}
		g.Ref = refStr
		return nil
	default:
		return fmt.Errorf("line %d: global env entry must be a string or mapping with `ref:`", node.Line)
	}
}

// ServiceConfig defines a single service in the config file.
type ServiceConfig struct {
	Command  string            `yaml:"command"`
	Setup    []string          `yaml:"setup"`    // commands run sequentially before Command
	Shutdown []string          `yaml:"shutdown"` // commands run sequentially after Command exits
	Dir      string            `yaml:"dir"`
	Proxy    int               `yaml:"proxy"`
	Port     int               `yaml:"port"`
	Group    string            `yaml:"group"`
	Scheme   string            `yaml:"scheme"`   // "http" or "https"; defaults to "http"
	TLSCert  string            `yaml:"tls_cert"` // path to TLS certificate file
	TLSKey   string            `yaml:"tls_key"`  // path to TLS key file
	EnvFile  string            `yaml:"env_file"` // optional path for exported .env file
	Env      map[string]string `yaml:"env"`
	Ports    []PortMapping     `yaml:"ports"`

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

// PortMapping maps an auto-assigned port env var to a proxy and service name.
// Proxy is optional: omit it for non-HTTP ports (databases, caches, etc.) that
// need a free port allocated for ${svc.env} interpolation but should not be
// registered with an HTTP reverse-proxy listener.
type PortMapping struct {
	Env   string `yaml:"env"`
	Proxy int    `yaml:"proxy"`
	Name  string `yaml:"name"`
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
		svc.Dir = resolvePath(svc.Dir, dir)
		svc.TLSCert = resolvePath(svc.TLSCert, dir)
		svc.TLSKey = resolvePath(svc.TLSKey, dir)
		// Per-service env_file is resolved against the service's (already
		// absolute) dir; fall back to the config dir when dir is empty.
		envFileBase := svc.Dir
		if envFileBase == "" {
			envFileBase = dir
		}
		svc.EnvFile = resolvePath(svc.EnvFile, envFileBase)
		// Infer scheme from cert presence.
		if svc.Scheme == "" && svc.TLSCert != "" {
			svc.Scheme = "https"
		}
		if svc.HealthCheck != nil {
			if err := svc.HealthCheck.Validate(); err != nil {
				return nil, fmt.Errorf("service %q: %w", name, err)
			}
		}
		cfg.Services[name] = svc
	}
	cfg.Global.EnvFile = resolvePath(cfg.Global.EnvFile, dir)
	if err := validateDependencies(cfg.Services); err != nil {
		return nil, err
	}
	return &cfg, nil
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
