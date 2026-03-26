package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the top-level mdp.yaml configuration.
type Config struct {
	Services  map[string]ServiceConfig `yaml:"services"`
	PortRange string                   `yaml:"port_range"`
}

// ServiceConfig defines a single service in the config file.
type ServiceConfig struct {
	Command string            `yaml:"command"`
	Dir     string            `yaml:"dir"`
	Proxy   int               `yaml:"proxy"`
	Port    int               `yaml:"port"`
	Group   string            `yaml:"group"`
	Env     map[string]string `yaml:"env"`
	Ports   []PortMapping     `yaml:"ports"`
}

// PortMapping maps an auto-assigned port env var to a proxy and service name.
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
		if svc.Dir != "" && !filepath.IsAbs(svc.Dir) {
			svc.Dir = filepath.Join(dir, svc.Dir)
		}
		cfg.Services[name] = svc
	}
	return &cfg, nil
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
