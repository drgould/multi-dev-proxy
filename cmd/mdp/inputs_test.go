package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/derekgould/multi-dev-proxy/internal/config"
)

// key sends a single named key press ("up", "down", "enter", "esc", or
// "ctrl+c") to the model and returns the updated model.
func key(t *testing.T, m inputWizardModel, name string) inputWizardModel {
	t.Helper()
	var msg tea.KeyPressMsg
	switch name {
	case "up":
		msg = tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		msg = tea.KeyPressMsg{Code: tea.KeyDown}
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+c":
		msg = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	default:
		t.Fatalf("key: unknown key name %q", name)
	}
	next, _ := m.Update(msg)
	return next.(inputWizardModel)
}

// typeText sends one KeyPressMsg per rune, as a real terminal would.
func typeText(t *testing.T, m inputWizardModel, s string) inputWizardModel {
	t.Helper()
	for _, r := range s {
		next, _ := m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
		m = next.(inputWizardModel)
	}
	return m
}

func TestInputWizardFreeText(t *testing.T) {
	groups := []string{"main", "derek/foo"}
	tests := []struct {
		name    string
		spec    config.InputSpec
		choices []string
		do      func(t *testing.T, m inputWizardModel) inputWizardModel
		want    string
		wantErr string
	}{
		{"pick by arrow", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, groups,
			func(t *testing.T, m inputWizardModel) inputWizardModel { return key(t, m, "down") }, "derek/foo", ""},
		{"pick @{current} by arrow", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, append(append([]string(nil), groups...), currentGroupSentinel),
			func(t *testing.T, m inputWizardModel) inputWizardModel { return key(t, key(t, m, "down"), "down") }, "@{current}", ""},
		{"typed @{current}", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, groups,
			func(t *testing.T, m inputWizardModel) inputWizardModel { return typeText(t, m, "@{current}") }, "@{current}", ""},
		{"empty uses @{current} default", config.InputSpec{Name: "b", Choices: "groups", Default: "@{current}", HasDefault: true}, groups,
			func(t *testing.T, m inputWizardModel) inputWizardModel { return m }, "@{current}", ""},
		{"empty uses default", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, groups,
			func(t *testing.T, m inputWizardModel) inputWizardModel { return m }, "main", ""},
		{"custom typed branch", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, groups,
			func(t *testing.T, m inputWizardModel) inputWizardModel { return typeText(t, m, "other/branch") }, "other/branch", ""},
		{"picked group with ${} rejected", config.InputSpec{Name: "b", Choices: "groups", Default: "main", HasDefault: true}, []string{"main", "dev${x.port}"},
			func(t *testing.T, m inputWizardModel) inputWizardModel { return key(t, m, "down") }, "", "plain literal"},
		{"free text", config.InputSpec{Name: "r", Default: "us", HasDefault: true}, nil,
			func(t *testing.T, m inputWizardModel) inputWizardModel { return typeText(t, m, "eu") }, "eu", ""},
		{"free text empty uses default", config.InputSpec{Name: "r", Default: "us", HasDefault: true}, nil,
			func(t *testing.T, m inputWizardModel) inputWizardModel { return m }, "us", ""},
		{"value with ${} rejected", config.InputSpec{Name: "b", Default: "main", HasDefault: true}, nil,
			func(t *testing.T, m inputWizardModel) inputWizardModel { return typeText(t, m, "${web.port}") }, "", "plain literal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newInputWizardModel([]inputStep{{spec: tt.spec, choices: tt.choices}}, "feature-x")
			m = tt.do(t, m)
			next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m = next.(inputWizardModel)
			if tt.wantErr != "" {
				if m.errMsg == "" || !strings.Contains(m.errMsg, tt.wantErr) {
					t.Fatalf("want error containing %q, got %q", tt.wantErr, m.errMsg)
				}
				return
			}
			if cmd == nil {
				t.Fatalf("expected tea.Quit after the only step, got nil cmd")
			}
			if got := m.values[tt.spec.Name]; got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The default is shown as a placeholder, not pre-filled text, so typing a
// custom value replaces it instead of appending to it (e.g. "main" + typing
// "feature/foo" must not produce "mainfeature/foo").
func TestInputWizardTypingReplacesDefault(t *testing.T) {
	spec := config.InputSpec{Name: "b", Default: "main", HasDefault: true}
	m := newInputWizardModel([]inputStep{{spec: spec}}, "feature-x")
	if got := m.input.Value(); got != "" {
		t.Fatalf("initial value = %q, want empty (default shown as placeholder)", got)
	}
	m = typeText(t, m, "feature/foo")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(inputWizardModel)
	if cmd == nil {
		t.Fatalf("expected tea.Quit after the only step")
	}
	if got := m.values["b"]; got != "feature/foo" {
		t.Fatalf("got %q, want feature/foo", got)
	}
}

// An input with no default re-prompts (in place) on an empty answer instead
// of erroring, and accepts a subsequent typed value.
func TestInputWizardNoDefaultReprompts(t *testing.T) {
	spec := config.InputSpec{Name: "b"}
	m := newInputWizardModel([]inputStep{{spec: spec}}, "feature-x")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(inputWizardModel)
	if cmd != nil {
		t.Fatalf("empty answer with no default must not advance/quit")
	}
	if m.errMsg == "" {
		t.Fatalf("want a required-value error")
	}
	m = typeText(t, m, "feature")
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(inputWizardModel)
	if cmd == nil {
		t.Fatalf("expected tea.Quit after the only step")
	}
	if got := m.values["b"]; got != "feature" {
		t.Fatalf("got %q, want feature", got)
	}
}

// Esc/Ctrl-C aborts the whole wizard immediately, matching the old Ctrl-D
// behavior — the caller (runInputWizard) turns this into an error.
func TestInputWizardCancel(t *testing.T) {
	msgs := map[string]tea.KeyPressMsg{
		"esc":    {Code: tea.KeyEscape},
		"ctrl+c": {Code: 'c', Mod: tea.ModCtrl},
	}
	for name, msg := range msgs {
		t.Run(name, func(t *testing.T) {
			spec := config.InputSpec{Name: "b", Default: "main", HasDefault: true}
			m := newInputWizardModel([]inputStep{{spec: spec}}, "feature-x")
			next, cmd := m.Update(msg)
			m = next.(inputWizardModel)
			if cmd == nil {
				t.Fatalf("want tea.Quit on cancel")
			}
			if !m.cancelled {
				t.Fatalf("want cancelled=true")
			}
		})
	}
}

// Submitting one step advances to the next with its own default shown as a
// placeholder and its own pick-list, and both answers land in the final
// values map.
func TestInputWizardMultipleSteps(t *testing.T) {
	steps := []inputStep{
		{spec: config.InputSpec{Name: "branch", Default: "main", HasDefault: true}, choices: []string{"main", "derek/foo", currentGroupSentinel}},
		{spec: config.InputSpec{Name: "region", Default: "us", HasDefault: true}},
	}
	m := newInputWizardModel(steps, "feature-x")
	if got := m.input.Placeholder; got != "main" {
		t.Fatalf("step 0 placeholder = %q, want main", got)
	}
	m = key(t, m, "down") // -> derek/foo
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(inputWizardModel)
	if cmd != nil {
		t.Fatalf("submitting a non-final step must not quit")
	}
	if m.step != 1 {
		t.Fatalf("step = %d, want 1", m.step)
	}
	if got := m.input.Placeholder; got != "us" {
		t.Fatalf("step 1 placeholder = %q, want us", got)
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(inputWizardModel)
	if cmd == nil {
		t.Fatalf("submitting the final step must quit")
	}
	if m.values["branch"] != "derek/foo" || m.values["region"] != "us" {
		t.Fatalf("got %v", m.values)
	}
}

// The pick-list's initial cursor lands on the default's entry, so the
// "@{current}" sentinel is reachable and its marker names the caller's group.
func TestInputWizardViewShowsCurrent(t *testing.T) {
	spec := config.InputSpec{Name: "b", Choices: "groups", Default: "@{current}", HasDefault: true}
	choices := []string{"main", "derek/foo", currentGroupSentinel}
	m := newInputWizardModel([]inputStep{{spec: spec, choices: choices}}, "feature-x")
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (the @{current} entry)", m.cursor)
	}
	view := m.View().Content
	if !strings.Contains(view, "@{current} — this checkout's default group (feature-x) (default)") {
		t.Fatalf("view missing @{current} entry, got:\n%s", view)
	}
}

func TestResolveInputsDefaults(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{
		{Name: "a", Default: "x", HasDefault: true},
		{Name: "b", Default: "y", HasDefault: true},
	}}
	vals, err := resolveInputs(context.Background(), cfg, false, "feature-x", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["a"] != "x" || vals["b"] != "y" {
		t.Fatalf("got %v", vals)
	}
}

func TestResolveInputsMissingDefault(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{{Name: "a"}}}
	_, err := resolveInputs(context.Background(), cfg, false, "feature-x", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "has no default") {
		t.Fatalf("want missing-default error, got %v", err)
	}
}

// An explicit `default: ""` is a valid optional value, distinct from an absent
// default, so the non-interactive path resolves it to "" without erroring.
func TestResolveInputsEmptyDefault(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{{Name: "a", Default: "", HasDefault: true}}}
	vals, err := resolveInputs(context.Background(), cfg, false, "feature-x", nil, nil)
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
	vals, err := resolveInputs(context.Background(), cfg, false, "feature-x", func(string) []string {
		t.Fatal("groups fetcher must not be called when prompting is disabled")
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["a"] != "x" {
		t.Fatalf("got %v", vals)
	}
}

// `mdp run -i` requires an interactive terminal — the wizard can't run over a
// pipe — but only when there's actually something left to prompt for.
func TestResolveInputsRequiresTTY(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{{Name: "a", Default: "main", HasDefault: true}}}
	_, err := resolveInputs(context.Background(), cfg, true, "feature-x", nil, func() bool { return false })
	if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("want TTY-required error, got %v", err)
	}
}

// If every input skips to its default, the wizard never runs, so isTTY must
// not even be called.
func TestResolveInputsSkipAllNoTTYNeeded(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{
		{Name: "branch", Default: "@{current}", HasDefault: true, Choices: "groups"},
	}}
	groupsFor := func(string) []string { return []string{} }
	vals, err := resolveInputs(context.Background(), cfg, true, "feature-x", groupsFor, func() bool {
		t.Fatal("isTTY must not be called when there's nothing to prompt")
		return false
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vals["branch"] != "@{current}" {
		t.Fatalf("got %v", vals)
	}
}

// An already-cancelled ctx (SIGTERM landing during buildInputSteps/
// fetchActiveGroups, both ctx-blind) must stop resolveInputs before it ever
// calls runInputWizard — otherwise tea.Program.Run would put the real
// process stdin into raw mode and block reading from it, hanging this test.
func TestResolveInputsCancelledContext(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{{Name: "a", Default: "x", HasDefault: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := resolveInputs(ctx, cfg, true, "feature-x", nil, func() bool { return true })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// When prompting, a `choices: groups` input becomes a step with a pick-list
// (groups + the "@{current}" sentinel); a plain input always becomes a
// free-text step (it has no skip condition).
func TestBuildInputStepsPrompting(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{
		{Name: "branch", Default: "main", HasDefault: true, Choices: "groups"},
		{Name: "region", Default: "us", HasDefault: true},
	}}
	fetches := 0
	groupsFor := func(repo string) []string {
		fetches++
		if repo != "" {
			t.Fatalf("groupsFor repo = %q, want empty (no repo filter declared)", repo)
		}
		return []string{"main", "derek/foo"}
	}
	values, steps, err := buildInputSteps(cfg, true, groupsFor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("values = %v, want empty (nothing skipped)", values)
	}
	if len(steps) != 2 {
		t.Fatalf("steps = %v, want 2", steps)
	}
	if steps[0].spec.Name != "branch" || !reflect.DeepEqual(steps[0].choices, []string{"main", "derek/foo", currentGroupSentinel}) {
		t.Fatalf("branch step = %+v", steps[0])
	}
	if steps[1].spec.Name != "region" || steps[1].choices != nil {
		t.Fatalf("region step = %+v", steps[1])
	}
	if fetches != 1 {
		t.Fatalf("groups fetched %d times, want 1", fetches)
	}
}

// A `choices: groups` input with no active groups and a declared default is
// skipped silently — resolved straight into values, never becoming a step —
// so `mdp run -i` only prompts when there is something to select. An input
// with no default still becomes a (free-text) step, since a value is required.
func TestBuildInputStepsSkipsGroupChoiceWithoutGroups(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{
		{Name: "branch", Default: "@{current}", HasDefault: true, Choices: "groups"},
		{Name: "region", Default: "us", HasDefault: true},
	}}
	groupsFor := func(string) []string { return []string{} }
	values, steps, err := buildInputSteps(cfg, true, groupsFor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if values["branch"] != "@{current}" {
		t.Fatalf("branch should be skipped straight to its default, got %v", values)
	}
	if len(steps) != 1 || steps[0].spec.Name != "region" {
		t.Fatalf("steps = %+v, want just region", steps)
	}

	// No default: still becomes a free-text step, not skipped.
	cfg = &config.Config{Inputs: config.Inputs{{Name: "branch", Choices: "groups"}}}
	values, steps, err = buildInputSteps(cfg, true, groupsFor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != 0 || len(steps) != 1 || steps[0].choices != nil {
		t.Fatalf("values=%v steps=%+v, want one free-text step", values, steps)
	}
}

// A failed groups fetch (nil, e.g. orchestrator unreachable) must not be
// mistaken for "no groups exist": instead of silently taking the default, the
// input becomes a free-text step (no pick-list) so -i still lets the user
// choose.
func TestBuildInputStepsFetchErrorDegradesToFreeText(t *testing.T) {
	cfg := &config.Config{Inputs: config.Inputs{
		{Name: "branch", Default: "main", HasDefault: true, Choices: "groups"},
	}}
	groupsFor := func(string) []string { return nil }
	values, steps, err := buildInputSteps(cfg, true, groupsFor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("branch must not be skipped to its default on a failed fetch, got %v", values)
	}
	if len(steps) != 1 || steps[0].choices != nil {
		t.Fatalf("steps = %+v, want one free-text step (no pick-list)", steps)
	}
}

// The groups fetch is cached per repo filter: same repo => one fetch,
// different repos => separate fetches with the declared repo passed through.
func TestBuildInputStepsGroupsPerRepo(t *testing.T) {
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
	values, steps, err := buildInputSteps(cfg, true, groupsFor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if values["c"] != "main" {
		t.Fatalf("c should be skipped to its default, got %v", values)
	}
	if len(steps) != 2 || steps[0].spec.Name != "a" || steps[1].spec.Name != "b" {
		t.Fatalf("steps = %+v, want a and b", steps)
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
