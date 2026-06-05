package main

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/config"
)

func TestPromptInput(t *testing.T) {
	groups := []string{"main", "derek/foo"}
	tests := []struct {
		name    string
		spec    config.InputSpec
		groups  []string
		input   string
		want    string
		wantErr string
	}{
		{"pick by number", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, groups, "2\n", "derek/foo", ""},
		{"empty uses default", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, groups, "\n", "main", ""},
		{"custom typed branch", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, groups, "other/branch\n", "other/branch", ""},
		{"out-of-range number is literal", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, groups, "9\n", "9", ""},
		{"numeric-named group selectable", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, []string{"main", "456"}, "456\n", "456", ""},
		{"picked group with ${} rejected", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, []string{"main", "dev${x.port}"}, "2\n", "", "plain literal"},
		{"free text", config.InputSpec{Name: "r", Default: "us", HasDefault: true}, nil, "eu\n", "eu", ""},
		{"free text empty uses default", config.InputSpec{Name: "r", Default: "us", HasDefault: true}, nil, "\n", "us", ""},
		{"no active groups falls to free text", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, nil, "1\n", "1", ""},
		{"EOF aborts", config.InputSpec{Name: "b", Default: "main", HasDefault: true}, nil, "", "", "cancelled"},
		{"re-prompts then accepts value when no default", config.InputSpec{Name: "b"}, nil, "\nfeature\n", "feature", ""},
		{"value with ${} rejected", config.InputSpec{Name: "b", Default: "main", HasDefault: true}, nil, "${web.port}\n", "", "plain literal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			var out bytes.Buffer
			got, err := promptInput(tt.spec, tt.groups, r, &out)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveInputsDefaults(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{
		{Name: "a", Default: "x", HasDefault: true},
		{Name: "b", Default: "y", HasDefault: true},
	}}
	vals, err := resolveInputs(cfg, false, nil, strings.NewReader(""), io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["a"] != "x" || vals["b"] != "y" {
		t.Fatalf("got %v", vals)
	}
}

func TestResolveInputsMissingDefault(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{{Name: "a"}}}
	_, err := resolveInputs(cfg, false, nil, strings.NewReader(""), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "has no default") {
		t.Fatalf("want missing-default error, got %v", err)
	}
}

// An explicit `default: ""` is a valid optional value, distinct from an absent
// default, so the non-interactive path resolves it to "" without erroring.
func TestResolveInputsEmptyDefault(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{{Name: "a", Default: "", HasDefault: true}}}
	vals, err := resolveInputs(cfg, false, nil, strings.NewReader(""), io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := vals["a"]; !ok || v != "" {
		t.Fatalf("got %q (ok=%v), want empty string", v, ok)
	}
}

// Without -i (interactive=false), defaults are used and the groups fetcher is
// never invoked.
func TestResolveInputsPromptDisabled(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{{Name: "a", Default: "x", HasDefault: true, Choices: "groups"}}}
	vals, err := resolveInputs(cfg, false, func() []string {
		t.Fatal("groups fetcher must not be called when prompting is disabled")
		return nil
	}, strings.NewReader("ignored\n"), io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["a"] != "x" {
		t.Fatalf("got %v", vals)
	}
}

// When prompting, a `choices: groups` input fetches the active groups once and
// accepts a numbered selection; a plain input reads free text; and an empty
// answer falls back to the declared default.
func TestResolveInputsPrompting(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{
		{Name: "branch", Default: "main", HasDefault: true, Choices: "groups"},
		{Name: "region", Default: "us", HasDefault: true},
		{Name: "tier", Default: "free", HasDefault: true},
	}}
	fetches := 0
	groupsFor := func() []string {
		fetches++
		return []string{"main", "derek/foo"}
	}
	// branch: pick #2; region: typed; tier: empty => default.
	vals, err := resolveInputs(cfg, true, groupsFor, strings.NewReader("2\neu\n\n"), io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["branch"] != "derek/foo" || vals["region"] != "eu" || vals["tier"] != "free" {
		t.Fatalf("got %v", vals)
	}
	if fetches != 1 {
		t.Fatalf("groups fetched %d times, want 1", fetches)
	}
}

func TestApplyInputs(t *testing.T) {
	def := "${inputs.x}"
	cfg := &config.Config{
		Services: map[string]config.ServiceConfig{
			"web": {
				Command:  "run --branch ${inputs.x}",
				Dir:      "/repo/worktrees/${inputs.x}",
				EnvFile:  "/repo/worktrees/${inputs.x}/.env",
				Setup:    []string{"setup ${inputs.x}"},
				Shutdown: []string{"teardown ${inputs.x}"},
				Env: map[string]config.EnvValue{
					"A": {Value: "v-${inputs.x}"},
					"B": {Ref: "api.port", Default: &def},
					"C": {Ref: "@${inputs.x}.server.port"},
				},
			},
		},
		Global: config.GlobalConfig{Env: map[string]config.EnvValue{
			"G": {Value: "${inputs.x}"},
		}},
		Links: map[string]string{"api": "${inputs.x}", "auth": "stable"},
	}
	if err := applyInputs(cfg, map[string]string{"x": "main"}); err != nil {
		t.Fatalf("applyInputs: %v", err)
	}
	web := cfg.Services["web"]
	if web.Command != "run --branch main" {
		t.Fatalf("command = %q", web.Command)
	}
	if web.Dir != "/repo/worktrees/main" {
		t.Fatalf("dir = %q", web.Dir)
	}
	if web.EnvFile != "/repo/worktrees/main/.env" {
		t.Fatalf("env_file = %q", web.EnvFile)
	}
	if web.Setup[0] != "setup main" || web.Shutdown[0] != "teardown main" {
		t.Fatalf("setup/shutdown = %v / %v", web.Setup, web.Shutdown)
	}
	if got := web.Env["A"].Value; got != "v-main" {
		t.Fatalf("env A = %q", got)
	}
	if got := web.Env["B"].DefaultValue(); got != "main" {
		t.Fatalf("env B default = %q", got)
	}
	if got := web.Env["C"].Ref; got != "@main.server.port" {
		t.Fatalf("env C ref = %q", got)
	}
	if got := cfg.Global.Env["G"].Value; got != "main" {
		t.Fatalf("global G = %q", got)
	}
	if cfg.Links["api"] != "main" || cfg.Links["auth"] != "stable" {
		t.Fatalf("links = %v", cfg.Links)
	}
}

// A relative env_file resolved against a dir containing ${inputs...} must be
// fixed up alongside dir, not left pointing at the literal placeholder path.
func TestApplyInputsResolvesRelativeEnvFile(t *testing.T) {
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

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := applyInputs(cfg, map[string]string{"branch": "feature"}); err != nil {
		t.Fatalf("applyInputs: %v", err)
	}
	cfg.FinalizePaths(dir) // mirrors runBatchMode: resolve paths after substitution
	web := cfg.Services["web"]
	wantDir := filepath.Join(dir, "services", "feature")
	if web.Dir != wantDir {
		t.Fatalf("dir = %q, want %q", web.Dir, wantDir)
	}
	if want := filepath.Join(wantDir, ".env"); web.EnvFile != want {
		t.Fatalf("env_file = %q, want %q", web.EnvFile, want)
	}
}

// An input resolving to an absolute path must be honored as-is, not joined to
// the config dir — the case that breaks if resolution runs before substitution.
func TestApplyInputsAbsoluteDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mdp.yaml")
	os.WriteFile(path, []byte(`
inputs:
  wt:
    default: /tmp/placeholder
services:
  web:
    command: run
    dir: ${inputs.wt}
    proxy: 3000
`), 0644)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := applyInputs(cfg, map[string]string{"wt": "/home/u/work"}); err != nil {
		t.Fatalf("applyInputs: %v", err)
	}
	cfg.FinalizePaths(dir)
	if got := cfg.Services["web"].Dir; got != "/home/u/work" {
		t.Fatalf("dir = %q, want /home/u/work (must not be joined to the config dir)", got)
	}
}

// An env ref: that resolves to empty must be rejected, not silently downgraded
// to a scalar KEY= value.
func TestApplyInputsEmptyRef(t *testing.T) {
	cfg := &config.Config{Services: map[string]config.ServiceConfig{
		"web": {Env: map[string]config.EnvValue{"K": {Ref: "${inputs.target}"}}},
	}}
	err := applyInputs(cfg, map[string]string{"target": ""})
	if err == nil || !strings.Contains(err.Error(), "empty ref") {
		t.Fatalf("want empty-ref error, got %v", err)
	}
}

// A link that resolves to an empty group is an error (not a silent fall-through
// to the caller's own group), but only after CLI --link overrides are merged in
// — an override can rescue a config link that resolved empty.
func TestCheckLinkGroups(t *testing.T) {
	if err := checkLinkGroups(map[string]string{"api": "main"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := checkLinkGroups(map[string]string{"api": ""}); err == nil || !strings.Contains(err.Error(), "empty group") {
		t.Fatalf("want empty-group error, got %v", err)
	}
	// An input that resolves to empty leaves an empty config link; a --link
	// override for that repo rescues it, so the merged result is valid.
	cfg := &config.Config{Links: map[string]string{"api": "${inputs.x}"}}
	if err := applyInputs(cfg, map[string]string{"x": ""}); err != nil {
		t.Fatalf("applyInputs: %v", err)
	}
	merged := mergeLinks(cfg.Links, map[string]string{"api": "main"})
	if err := checkLinkGroups(merged); err != nil {
		t.Fatalf("override should rescue empty link, got %v", err)
	}
	// Without the override the empty link is rejected.
	if err := checkLinkGroups(mergeLinks(cfg.Links, nil)); err == nil {
		t.Fatalf("want empty-group error for un-overridden empty link")
	}
}

func TestFetchActiveGroups(t *testing.T) {
	client := &http.Client{}

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"main":["a"],"derek/foo":["b"]}`))
	}))
	defer ok.Close()
	if got := fetchActiveGroups(client, ok.URL); !reflect.DeepEqual(got, []string{"derek/foo", "main"}) {
		t.Fatalf("success: got %v, want sorted [derek/foo main]", got)
	}

	// Each degradation path returns nil so prompting falls back to free text.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	if got := fetchActiveGroups(client, bad.URL); got != nil {
		t.Fatalf("non-200: got %v, want nil", got)
	}

	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer junk.Close()
	if got := fetchActiveGroups(client, junk.URL); got != nil {
		t.Fatalf("bad json: got %v, want nil", got)
	}

	if got := fetchActiveGroups(client, "http://127.0.0.1:1"); got != nil {
		t.Fatalf("connection error: got %v, want nil", got)
	}
}

func TestMergeLinks(t *testing.T) {
	// CLI wins per repo; config-only repos are retained.
	got := mergeLinks(
		map[string]string{"api": "main", "auth": "stable"},
		map[string]string{"api": "cli-override"},
	)
	want := map[string]string{"api": "cli-override", "auth": "stable"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// No config links: returns the CLI map unchanged.
	cli := map[string]string{"x": "y"}
	if got := mergeLinks(nil, cli); !reflect.DeepEqual(got, cli) {
		t.Fatalf("got %v, want %v", got, cli)
	}
	// No CLI links: returns the config map unchanged (no allocation).
	conf := map[string]string{"api": "main"}
	if got := mergeLinks(conf, nil); !reflect.DeepEqual(got, conf) {
		t.Fatalf("got %v, want %v", got, conf)
	}
}
