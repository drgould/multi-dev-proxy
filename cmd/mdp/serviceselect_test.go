package main

import (
	"context"
	"sort"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/derekgould/multi-dev-proxy/internal/config"
)

// serviceSelectTestConfig mirrors the fixture in TestResolveServiceSelection
// (run_test.go) so closure results here stay consistent with what
// resolveServiceSelection will compute once the picker's picks are handed to
// it.
func serviceSelectTestConfig() *config.Config {
	def := "9"
	return &config.Config{
		Services: map[string]config.ServiceConfig{
			"db":       {Command: "db"},
			"cache":    {Command: "cache"},
			"api":      {Command: "api", DependsOn: []string{"db", "cache"}},
			"worker":   {Command: "worker", DependsOn: []string{"db"}},
			"frontend": {Command: "fe", DependsOn: []string{"api"}},
			"solo":     {Command: "solo"},
			"metrics":  {Command: "m"},
			"reporter": {Command: "r", Env: map[string]config.EnvValue{
				"METRICS_URL": {Value: "http://localhost:${metrics.PORT}"},
			}},
			// Defaulted ref → tolerates absence, must not pull in.
			"opt": {Command: "o", Env: map[string]config.EnvValue{
				"X": {Ref: "absent.PORT", Default: &def},
			}},
			// Cross-repo ref → resolved by orchestrator, must not pull in.
			"xrepo": {Command: "x", Env: map[string]config.EnvValue{
				"P": {Value: "${@backend.api.PORT}"},
			}},
		},
	}
}

func setEqual(t *testing.T, got map[string]bool, want ...string) {
	t.Helper()
	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		wantSet[w] = true
	}
	if len(got) != len(wantSet) {
		t.Errorf("got %v, want %v", got, wantSet)
		return
	}
	for k := range wantSet {
		if !got[k] {
			t.Errorf("got %v, want %v", got, wantSet)
			return
		}
	}
}

func TestDependencyClosure(t *testing.T) {
	cfg := serviceSelectTestConfig()

	t.Run("no deps", func(t *testing.T) {
		got := dependencyClosure(cfg, map[string]bool{"solo": true})
		setEqual(t, got, "solo")
	})

	t.Run("transitive depends_on", func(t *testing.T) {
		got := dependencyClosure(cfg, map[string]bool{"frontend": true})
		setEqual(t, got, "frontend", "api", "db", "cache")
	})

	t.Run("diamond deps not double-counted", func(t *testing.T) {
		got := dependencyClosure(cfg, map[string]bool{"api": true, "worker": true})
		setEqual(t, got, "api", "worker", "db", "cache")
	})

	t.Run("env-ref dependency pulled in", func(t *testing.T) {
		got := dependencyClosure(cfg, map[string]bool{"reporter": true})
		setEqual(t, got, "reporter", "metrics")
	})

	t.Run("defaulted and cross-repo refs excluded", func(t *testing.T) {
		got := dependencyClosure(cfg, map[string]bool{"opt": true, "xrepo": true})
		setEqual(t, got, "opt", "xrepo")
	})

	t.Run("empty explicit set", func(t *testing.T) {
		got := dependencyClosure(cfg, map[string]bool{})
		if len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})
}

func TestDirectDeps(t *testing.T) {
	cfg := serviceSelectTestConfig()

	if got := directDeps(cfg, "frontend"); !equalStrings(got, []string{"api"}) {
		t.Errorf("frontend: got %v, want [api]", got)
	}
	if got := directDeps(cfg, "api"); !equalStrings(got, []string{"cache", "db"}) {
		t.Errorf("api: got %v, want [cache db]", got)
	}
	if got := directDeps(cfg, "solo"); len(got) != 0 {
		t.Errorf("solo: got %v, want none", got)
	}
	if got := directDeps(cfg, "reporter"); !equalStrings(got, []string{"metrics"}) {
		t.Errorf("reporter: got %v, want [metrics]", got)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}

func TestServiceSelectModelScrolling(t *testing.T) {
	names := []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10"}
	cfg := &config.Config{Services: map[string]config.ServiceConfig{}}
	for _, n := range names {
		cfg.Services[n] = config.ServiceConfig{Command: n}
	}

	m := newServiceSelectModel(cfg, names, map[string]bool{})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 6}) // windowHeight = 6-3 = 3
	m = next.(serviceSelectModel)
	if win := m.windowHeight(); win != 3 {
		t.Fatalf("windowHeight: got %d, want 3", win)
	}

	// Move the cursor past the initial window; scroll must follow so the
	// cursor stays visible.
	for i := 0; i < 5; i++ {
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = next.(serviceSelectModel)
	}
	if m.cursor != 5 {
		t.Fatalf("cursor: got %d, want 5", m.cursor)
	}
	if m.cursor < m.scroll || m.cursor >= m.scroll+m.windowHeight() {
		t.Fatalf("cursor %d not within visible window [%d, %d)", m.cursor, m.scroll, m.scroll+m.windowHeight())
	}

	// The rendered list must never exceed the window height.
	renderedRows := 0
	for i := m.scroll; i < m.scroll+m.windowHeight() && i < len(m.names); i++ {
		renderedRows++
	}
	if renderedRows > m.windowHeight() {
		t.Fatalf("rendered %d rows, window height is %d", renderedRows, m.windowHeight())
	}

	// Scrolling back up to the top must un-scroll.
	for i := 0; i < 9; i++ {
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
		m = next.(serviceSelectModel)
	}
	if m.cursor != 0 || m.scroll != 0 {
		t.Fatalf("got cursor=%d scroll=%d, want both 0", m.cursor, m.scroll)
	}
}

func TestServiceSelectModelUpdate(t *testing.T) {
	cfg := serviceSelectTestConfig()
	names := []string{"api", "cache", "db", "frontend"} // sorted subset for a small, deterministic list

	newModel := func() serviceSelectModel {
		return newServiceSelectModel(cfg, names, map[string]bool{})
	}

	t.Run("space toggles explicit and locks dependencies", func(t *testing.T) {
		m := newModel()
		m.cursor = 3 // "frontend"
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
		m = next.(serviceSelectModel)
		if !m.explicit["frontend"] {
			t.Fatal("frontend should be explicitly checked")
		}
		if !m.locked["api"] {
			t.Error("api should be locked-in as frontend's dependency")
		}
		if m.locked["frontend"] {
			t.Error("frontend is explicit, not locked")
		}
	})

	t.Run("toggling off removes now-unneeded locks", func(t *testing.T) {
		m := newModel()
		m.cursor = 3 // "frontend"
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
		m = next.(serviceSelectModel)
		if !m.locked["api"] {
			t.Fatal("setup: api should be locked")
		}
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}) // toggle frontend off again
		m = next.(serviceSelectModel)
		if m.explicit["frontend"] {
			t.Error("frontend should be unchecked")
		}
		if m.locked["api"] {
			t.Error("api should no longer be locked once nothing needs it")
		}
	})

	t.Run("a selects all then none", func(t *testing.T) {
		m := newModel()
		next, _ := m.Update(tea.KeyPressMsg{Text: "a", Code: 'a'})
		m = next.(serviceSelectModel)
		if len(m.explicit) != len(names) {
			t.Fatalf("expected all %d selected, got %d", len(names), len(m.explicit))
		}
		next, _ = m.Update(tea.KeyPressMsg{Text: "a", Code: 'a'})
		m = next.(serviceSelectModel)
		if len(m.explicit) != 0 {
			t.Fatalf("expected none selected, got %v", m.explicit)
		}
	})

	t.Run("enter with nothing selected shows a message instead of confirming", func(t *testing.T) {
		m := newModel()
		next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = next.(serviceSelectModel)
		if m.done {
			t.Error("should not confirm with an empty selection")
		}
		if m.message == "" {
			t.Error("expected a validation message")
		}
		if cmd != nil {
			t.Error("expected no command (not quitting)")
		}
	})

	t.Run("enter with a selection confirms and quits", func(t *testing.T) {
		m := newModel()
		m.explicit["db"] = true
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Error("expected a quit command")
		}
	})

	t.Run("q cancels and quits", func(t *testing.T) {
		m := newModel()
		next, cmd := m.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
		m = next.(serviceSelectModel)
		if !m.cancelled {
			t.Error("expected cancelled")
		}
		if cmd == nil {
			t.Error("expected a quit command")
		}
	})

	t.Run("up/down move the cursor within bounds", func(t *testing.T) {
		m := newModel()
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp}) // already at 0, stays
		m = next.(serviceSelectModel)
		if m.cursor != 0 {
			t.Errorf("cursor should clamp at 0, got %d", m.cursor)
		}
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = next.(serviceSelectModel)
		if m.cursor != 1 {
			t.Errorf("cursor should move to 1, got %d", m.cursor)
		}
	})
}

func TestSelectServicesTUINoTTY(t *testing.T) {
	// go test's stdin/stdout are never a TTY, so this exercises the guard
	// that prevents the picker from hanging in CI/non-interactive contexts.
	cfg := serviceSelectTestConfig()
	if _, err := selectServicesTUI(context.Background(), cfg, nil); err == nil {
		t.Fatal("expected an error when stdin/stdout is not a TTY")
	}
}
