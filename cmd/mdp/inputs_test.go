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
		{"pick @{current} by number", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, groups, "3\n", "@{current}", ""},
		{"typed @{current}", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, groups, "@{current}\n", "@{current}", ""},
		{"empty uses @{current} default", config.InputSpec{Name: "b", Choices: "groups", Default: "@{current}", HasDefault: true}, groups, "\n", "@{current}", ""},
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
			got, err := promptInput(tt.spec, "feature-x", tt.groups, r, &out)
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

// The pick-list ends with an "@{current}" entry annotated with the caller's
// actual group, so the fallback-to-own-group sentinel is discoverable.
func TestPromptInputPickListShowsCurrent(t *testing.T) {
	spec := config.InputSpec{Name: "b", Choices: "groups", Default: "@{current}", HasDefault: true}
	r := bufio.NewReader(strings.NewReader("\n"))
	var out bytes.Buffer
	got, err := promptInput(spec, "feature-x", []string{"main", "derek/foo"}, r, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "@{current}" {
		t.Fatalf("got %q, want @{current}", got)
	}
	if !strings.Contains(out.String(), "3) @{current} — this checkout's default group (feature-x) (default)") {
		t.Fatalf("pick-list missing @{current} entry, got:\n%s", out.String())
	}
}

func TestResolveInputsDefaults(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{
		{Name: "a", Default: "x", HasDefault: true},
		{Name: "b", Default: "y", HasDefault: true},
	}}
	vals, err := resolveInputs(cfg, false, "feature-x", nil, strings.NewReader(""), io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["a"] != "x" || vals["b"] != "y" {
		t.Fatalf("got %v", vals)
	}
}

func TestResolveInputsMissingDefault(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{{Name: "a"}}}
	_, err := resolveInputs(cfg, false, "feature-x", nil, strings.NewReader(""), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "has no default") {
		t.Fatalf("want missing-default error, got %v", err)
	}
}

// An explicit `default: ""` is a valid optional value, distinct from an absent
// default, so the non-interactive path resolves it to "" without erroring.
func TestResolveInputsEmptyDefault(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{{Name: "a", Default: "", HasDefault: true}}}
	vals, err := resolveInputs(cfg, false, "feature-x", nil, strings.NewReader(""), io.Discard)
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
	vals, err := resolveInputs(cfg, false, "feature-x", func(string) []string {
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
	groupsFor := func(repo string) []string {
		fetches++
		if repo != "" {
			t.Fatalf("groupsFor repo = %q, want empty (no repo filter declared)", repo)
		}
		return []string{"main", "derek/foo"}
	}
	// branch: pick #2; region: typed; tier: empty => default.
	vals, err := resolveInputs(cfg, true, "feature-x", groupsFor, strings.NewReader("2\neu\n\n"), io.Discard)
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

// A `choices: groups` input with no active groups and a declared default is
// skipped silently — no prompt output, no stdin consumed — so `mdp run -i`
// only prompts when there is something to select. An input with no default
// still prompts (free text), since a value is required.
func TestResolveInputsSkipsGroupChoiceWithoutGroups(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{
		{Name: "branch", Default: "@{current}", HasDefault: true, Choices: "groups"},
		{Name: "region", Default: "us", HasDefault: true},
	}}
	groupsFor := func(string) []string { return []string{} }
	var out bytes.Buffer
	// "eu" must be read by region, not swallowed by the skipped branch input.
	vals, err := resolveInputs(cfg, true, "feature-x", groupsFor, strings.NewReader("eu\n"), &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["branch"] != "@{current}" || vals["region"] != "eu" {
		t.Fatalf("got %v", vals)
	}
	if strings.Contains(out.String(), "branch") {
		t.Fatalf("skipped input must not prompt, got:\n%s", out.String())
	}

	// No default: still prompts free-text.
	cfg = &config.Config{Inputs: config.Inputs{{Name: "branch", Choices: "groups"}}}
	vals, err = resolveInputs(cfg, true, "feature-x", groupsFor, strings.NewReader("other/branch\n"), io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["branch"] != "other/branch" {
		t.Fatalf("got %v", vals)
	}
}

// A failed groups fetch (nil, e.g. orchestrator unreachable) must not be
// mistaken for "no groups exist": instead of silently taking the default, the
// input degrades to a free-text prompt so -i still lets the user choose.
func TestResolveInputsFetchErrorPromptsFreeText(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{
		{Name: "branch", Default: "main", HasDefault: true, Choices: "groups"},
	}}
	groupsFor := func(string) []string { return nil }
	var out bytes.Buffer
	vals, err := resolveInputs(cfg, true, "feature-x", groupsFor, strings.NewReader("derek/foo\n"), &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["branch"] != "derek/foo" {
		t.Fatalf("got %v, want typed value (not skipped to default)", vals)
	}
	if out.Len() == 0 {
		t.Fatal("expected a prompt to be written")
	}
}

// The groups fetch is cached per repo filter: same repo => one fetch,
// different repos => separate fetches with the declared repo passed through.
func TestResolveInputsGroupsPerRepo(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{
		{Name: "a", Default: "main", HasDefault: true, Choices: "groups", Repo: "api"},
		{Name: "b", Default: "main", HasDefault: true, Choices: "groups", Repo: "api"},
		{Name: "c", Default: "main", HasDefault: true, Choices: "groups", Repo: "auth"},
	}}
	fetches := map[string]int{}
	groupsFor := func(repo string) []string {
		fetches[repo]++
		if repo == "auth" {
			return []string{} // no active auth groups => input c skipped
		}
		return []string{"main", "derek/foo"}
	}
	vals, err := resolveInputs(cfg, true, "feature-x", groupsFor, strings.NewReader("2\n\n"), io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["a"] != "derek/foo" || vals["b"] != "main" || vals["c"] != "main" {
		t.Fatalf("got %v", vals)
	}
	if !reflect.DeepEqual(fetches, map[string]int{"api": 1, "auth": 1}) {
		t.Fatalf("fetches = %v, want one per repo", fetches)
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
				PostStart: config.PostStartConfig{
					Commands: []string{"seed ${inputs.x}"},
				},
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
	if web.PostStart.Commands[0] != "seed main" {
		t.Fatalf("post_start = %v", web.PostStart.Commands)
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
	// "@{current}" is a valid (non-empty) group: it resolves to the caller's own
	// group at lookup time.
	if err := checkLinkGroups(map[string]string{"api": "@{current}"}); err != nil {
		t.Fatalf("@{current} should be accepted, got %v", err)
	}
}

// An input answered with "@{current}" flows through the link pipeline and makes
// the lookup fall back to the caller's own group.
func TestInputCurrentFallsBackToOwnGroup(t *testing.T) {
	cfg := &config.Config{Links: map[string]string{"api": "${inputs.x}"}}
	if err := applyInputs(cfg, map[string]string{"x": "@{current}"}); err != nil {
		t.Fatalf("applyInputs: %v", err)
	}
	merged := mergeLinks(cfg.Links, nil)
	if err := checkLinkGroups(merged); err != nil {
		t.Fatalf("checkLinkGroups: %v", err)
	}
	if g := effectiveGroup("api", "feature-x", merged); g != "feature-x" {
		t.Fatalf("effectiveGroup = %q, want feature-x", g)
	}
}

func TestFetchActiveGroups(t *testing.T) {
	client := &http.Client{}

	var gotRepo string
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRepo = r.URL.Query().Get("repo")
		w.Write([]byte(`{"main":["a"],"derek/foo":["b"]}`))
	}))
	defer ok.Close()
	if got := fetchActiveGroups(client, ok.URL, ""); !reflect.DeepEqual(got, []string{"derek/foo", "main"}) {
		t.Fatalf("success: got %v, want sorted [derek/foo main]", got)
	}
	if gotRepo != "" {
		t.Fatalf("repo param = %q, want unset", gotRepo)
	}

	// A repo filter is forwarded as the ?repo= query param.
	fetchActiveGroups(client, ok.URL, "my repo")
	if gotRepo != "my repo" {
		t.Fatalf("repo param = %q, want %q", gotRepo, "my repo")
	}

	// A successful response with zero groups is a non-nil empty slice — distinct
	// from the nil error paths below, so resolveInputs can skip vs prompt.
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer empty.Close()
	if got := fetchActiveGroups(client, empty.URL, ""); got == nil || len(got) != 0 {
		t.Fatalf("empty: got %#v, want non-nil empty slice", got)
	}

	// Each degradation path returns nil so prompting falls back to free text.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	if got := fetchActiveGroups(client, bad.URL, ""); got != nil {
		t.Fatalf("non-200: got %v, want nil", got)
	}

	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer junk.Close()
	if got := fetchActiveGroups(client, junk.URL, ""); got != nil {
		t.Fatalf("bad json: got %v, want nil", got)
	}

	if got := fetchActiveGroups(client, "http://127.0.0.1:1", ""); got != nil {
		t.Fatalf("connection error: got %v, want nil", got)
	}
}

// runBatchMode substitutes inputs before computing the --service selection, so
// a ${inputs.X} env ref must not be mistaken for a dependency on a service
// literally named "inputs".
func TestResolveServiceSelectionAfterInputs(t *testing.T) {
	cfg := &config.Config{
		Inputs: config.Inputs{{Name: "branch", Default: "main", HasDefault: true}},
		Services: map[string]config.ServiceConfig{
			"web": {Command: "run", Env: map[string]config.EnvValue{"X": {Value: "${inputs.branch}"}}},
		},
	}
	if err := applyInputs(cfg, map[string]string{"branch": "main"}); err != nil {
		t.Fatalf("applyInputs: %v", err)
	}
	sel, err := resolveServiceSelection(cfg, []string{"web"})
	if err != nil {
		t.Fatalf("resolveServiceSelection: %v", err)
	}
	if len(sel) != 1 || !sel["web"] {
		t.Fatalf("expected only web selected, got %v", sel)
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
