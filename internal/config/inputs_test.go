package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadInputsAndLinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
inputs:
  api_branch:
    prompt: "Which branch?"
    default: main
    choices: groups
  region:
    default: us
links:
  api: ${inputs.api_branch}
  auth: stable
services:
  web:
    command: npm run dev
    proxy: 3000
    env:
      TARGET: ${inputs.api_branch}
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Inputs) != 2 {
		t.Fatalf("expected 2 inputs, got %d", len(cfg.Inputs))
	}
	// Declaration order is preserved.
	if cfg.Inputs[0].Name != "api_branch" || cfg.Inputs[1].Name != "region" {
		t.Fatalf("input order not preserved: %+v", cfg.Inputs)
	}
	got := cfg.Inputs[0]
	if got.Prompt != "Which branch?" || got.Default != "main" || got.Choices != "groups" {
		t.Fatalf("api_branch spec wrong: %+v", got)
	}
	if cfg.Links["api"] != "${inputs.api_branch}" || cfg.Links["auth"] != "stable" {
		t.Fatalf("links wrong: %+v", cfg.Links)
	}
}

func TestLoadInputDefaultPresence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
inputs:
  region:
    default: ""
  other:
    prompt: pick
  bare:
    default:
services:
  web: {command: x, proxy: 3000}
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// region: an explicit empty default is present and distinct from absent.
	if cfg.Inputs[0].Name != "region" || !cfg.Inputs[0].HasDefault || cfg.Inputs[0].Default != "" {
		t.Fatalf("region spec wrong: %+v", cfg.Inputs[0])
	}
	// other: no `default:` key => HasDefault false.
	if cfg.Inputs[1].Name != "other" || cfg.Inputs[1].HasDefault {
		t.Fatalf("other spec wrong: %+v", cfg.Inputs[1])
	}
	// bare: `default:` with no value (null) is treated as "no default".
	if cfg.Inputs[2].Name != "bare" || cfg.Inputs[2].HasDefault {
		t.Fatalf("bare spec wrong: %+v", cfg.Inputs[2])
	}
}

// Load defers resolving dir/env_file when they carry ${inputs.X}, and
// FinalizePaths resolves them once substitution has happened.
func TestLoadDefersInputPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
inputs:
  branch:
    default: main
services:
  web:
    command: run
    dir: ./services/${inputs.branch}
    env_file: .env
    proxy: 3000
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Deferred: still raw (not joined to the config dir) after Load.
	if got := cfg.Services["web"].Dir; got != "./services/${inputs.branch}" {
		t.Fatalf("dir resolved too early: %q", got)
	}
	if got := cfg.Services["web"].EnvFile; got != ".env" {
		t.Fatalf("env_file resolved too early: %q", got)
	}

	// After substitution + FinalizePaths an absolute dir is honored as-is and a
	// relative env_file resolves against it.
	svc := cfg.Services["web"]
	svc.Dir = "/abs/work/feature"
	cfg.Services["web"] = svc
	cfg.FinalizePaths(dir)
	if got := cfg.Services["web"].Dir; got != "/abs/work/feature" {
		t.Fatalf("absolute dir = %q", got)
	}
	if got := cfg.Services["web"].EnvFile; got != filepath.Join("/abs/work/feature", ".env") {
		t.Fatalf("env_file = %q", got)
	}
}

func TestLoadInputsValidation(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			"unknown choices",
			`
inputs:
  x:
    choices: branches
services:
  web: {command: x, proxy: 3000}
`,
			`unknown choices "branches"`,
		},
		{
			"undeclared input ref in env",
			`
services:
  web:
    command: x
    proxy: 3000
    env:
      A: ${inputs.nope}
`,
			"undeclared input ${inputs.nope}",
		},
		{
			"undeclared input ref in link",
			`
links:
  api: ${inputs.nope}
services:
  web: {command: x, proxy: 3000}
`,
			"undeclared input ${inputs.nope}",
		},
		{
			"reserved service name",
			`
services:
  inputs: {command: x, proxy: 3000}
`,
			`service name "inputs" is reserved`,
		},
		{
			"invalid input name",
			`
inputs:
  api-branch:
    default: main
services:
  web: {command: x, proxy: 3000}
`,
			`input name "api-branch" is invalid`,
		},
		{
			"default is not a plain literal",
			`
inputs:
  a:
    default: main
  b:
    default: "${inputs.a}"
services:
  web: {command: x, proxy: 3000}
`,
			`input "b": default must be a plain literal`,
		},
		{
			"undeclared input ref in command",
			`
services:
  web:
    command: "run --branch ${inputs.nope}"
    proxy: 3000
`,
			"undeclared input ${inputs.nope}",
		},
		{
			"malformed input ref (bad name)",
			`
inputs:
  api_branch:
    default: main
services:
  web:
    command: x
    proxy: 3000
    env:
      A: ${inputs.api-branch}
`,
			"malformed input reference",
		},
		{
			"nested ref in input fallback",
			`
services:
  web:
    command: x
    proxy: 3000
    env:
      A: "${inputs.host:-${api.port}}"
`,
			"malformed input reference",
		},
		{
			"input ref in unsupported field (group)",
			`
inputs:
  branch:
    default: main
services:
  web:
    command: x
    proxy: 3000
    group: ${inputs.branch}
`,
			"group does not support ${inputs.X}",
		},
		{
			"unknown spec key",
			`
inputs:
  x:
    bogus: 1
services:
  web: {command: x, proxy: 3000}
`,
			`unknown key "bogus"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "mdp.yaml")
			os.WriteFile(path, []byte(tc.yaml), 0644)
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
