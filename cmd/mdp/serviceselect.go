package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"

	"github.com/derekgould/multi-dev-proxy/internal/config"
)

var (
	ssTitleStyle   = lipgloss.NewStyle().Bold(true)
	ssHelpStyle    = lipgloss.NewStyle().Faint(true)
	ssCursorStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	ssCheckedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	ssLockedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	ssMessageStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)

// serviceSelectModel is the full-screen checkbox picker behind
// `mdp run --select-services`. explicit holds the user's direct picks;
// locked (recomputed on every change) holds services pulled in only because
// something explicit needs them, per dependencyClosure. Locked rows render
// checked as a preview, but the picker only ever returns explicit — the
// caller feeds that into resolveServiceSelection for the authoritative
// closure (which also knows about cross-repo refs and unknown-name errors).
type serviceSelectModel struct {
	cfg       *config.Config
	names     []string
	explicit  map[string]bool
	locked    map[string]bool
	cursor    int
	scroll    int // index of the first visible row in names
	width     int
	height    int
	done      bool
	cancelled bool
	message   string
}

// ssChromeLines is the number of fixed lines View renders around the
// scrollable service list: title, help text, and the blank line separating
// them from the list.
const ssChromeLines = 3

// windowHeight returns how many service rows fit on screen. Before the first
// tea.WindowSizeMsg (height == 0 — e.g. in unit tests that never send one)
// it returns len(names) so nothing is clipped.
func (m serviceSelectModel) windowHeight() int {
	if m.height <= 0 {
		return len(m.names)
	}
	h := m.height - ssChromeLines
	if h < 1 {
		h = 1
	}
	return h
}

// ensureCursorVisible scrolls just enough to keep m.cursor inside the
// current window, clamped to the list's bounds.
func (m *serviceSelectModel) ensureCursorVisible() {
	win := m.windowHeight()
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	} else if m.cursor >= m.scroll+win {
		m.scroll = m.cursor - win + 1
	}
	if maxScroll := len(m.names) - win; m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func newServiceSelectModel(cfg *config.Config, names []string, explicit map[string]bool) serviceSelectModel {
	m := serviceSelectModel{cfg: cfg, names: names, explicit: explicit}
	m.locked = m.computeLocked()
	return m
}

func (m serviceSelectModel) computeLocked() map[string]bool {
	closure := dependencyClosure(m.cfg, m.explicit)
	locked := make(map[string]bool, len(closure))
	for name := range closure {
		if !m.explicit[name] {
			locked[name] = true
		}
	}
	return locked
}

func (m serviceSelectModel) Init() tea.Cmd { return nil }

func (m serviceSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = wsMsg.Width
		m.height = wsMsg.Height
		m.ensureCursorVisible()
		return m, nil
	}
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	m.message = ""
	switch keyMsg.String() {
	case "ctrl+c", "q", "esc":
		m.cancelled = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.ensureCursorVisible()
		}
	case "down", "j":
		if m.cursor < len(m.names)-1 {
			m.cursor++
			m.ensureCursorVisible()
		}
	case "space":
		name := m.names[m.cursor]
		if m.explicit[name] {
			delete(m.explicit, name)
		} else {
			m.explicit[name] = true
		}
		m.locked = m.computeLocked()
	case "a":
		if len(m.explicit) == len(m.names) {
			m.explicit = map[string]bool{}
		} else {
			all := make(map[string]bool, len(m.names))
			for _, n := range m.names {
				all[n] = true
			}
			m.explicit = all
		}
		m.locked = m.computeLocked()
	case "enter":
		if len(m.explicit) == 0 {
			m.message = "select at least one service (space to toggle, q to cancel)"
			return m, nil
		}
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m serviceSelectModel) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	if m.done || m.cancelled {
		return v
	}
	var b strings.Builder
	b.WriteString(ssTitleStyle.Render("Select services to run") + "\n")
	b.WriteString(ssHelpStyle.Render("space: toggle  ·  a: all/none  ·  enter: confirm  ·  q: cancel") + "\n\n")
	win := m.windowHeight()
	end := m.scroll + win
	if end > len(m.names) {
		end = len(m.names)
	}
	for i := m.scroll; i < end; i++ {
		name := m.names[i]
		marker, style := "[ ]", lipgloss.NewStyle()
		switch {
		case m.explicit[name]:
			marker, style = "[x]", ssCheckedStyle
		case m.locked[name]:
			marker, style = "[+]", ssLockedStyle
		}
		line := marker + " " + name
		if deps := directDeps(m.cfg, name); len(deps) > 0 {
			line += "  " + ssLockedStyle.Render("(needs: "+strings.Join(deps, ", ")+")")
		}
		prefix := "  "
		if i == m.cursor {
			prefix = ssCursorStyle.Render("> ")
		}
		b.WriteString(prefix + style.Render(line) + "\n")
	}
	if m.message != "" {
		b.WriteString("\n" + ssMessageStyle.Render(m.message) + "\n")
	}
	v.SetContent(b.String())
	return v
}

// dependencyClosure returns explicit plus every service transitively needed
// to start it, expanding the same edges resolveServiceSelection does
// (depends_on and un-defaulted local env refs, via referencedServices) but
// without its error handling — this is a cosmetic preview inside the picker,
// not the authoritative expansion.
func dependencyClosure(cfg *config.Config, explicit map[string]bool) map[string]bool {
	closure := make(map[string]bool, len(explicit))
	queue := make([]string, 0, len(explicit))
	for name := range explicit {
		closure[name] = true
		queue = append(queue, name)
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		svc, ok := cfg.Services[name]
		if !ok {
			continue
		}
		for _, ref := range referencedServices(svc) {
			if closure[ref] {
				continue
			}
			if _, ok := cfg.Services[ref]; !ok {
				continue
			}
			closure[ref] = true
			queue = append(queue, ref)
		}
	}
	return closure
}

// directDeps returns the sorted, deduplicated local services name directly
// references (depends_on plus un-defaulted local env refs), for the inline
// "(needs: ...)" row annotation.
func directDeps(cfg *config.Config, name string) []string {
	svc, ok := cfg.Services[name]
	if !ok {
		return nil
	}
	seen := make(map[string]bool)
	var deps []string
	for _, ref := range referencedServices(svc) {
		if _, ok := cfg.Services[ref]; !ok || seen[ref] {
			continue
		}
		seen[ref] = true
		deps = append(deps, ref)
	}
	sort.Strings(deps)
	return deps
}

// selectServicesTUI shows a full-screen checkbox picker over every service in
// cfg and returns the user's explicit picks (sorted) — not the dependency
// closure; the caller feeds the result through resolveServiceSelection for
// the authoritative expansion. preselected seeds the initial checked set.
// Returns an error if stdin/stdout isn't a TTY or the user cancels. ctx is
// wired into the program the same way as the input wizard (see
// runInputWizard) so a SIGTERM from the caller's signal.NotifyContext kills
// the picker instead of leaving it blocked on stdin.
func selectServicesTUI(ctx context.Context, cfg *config.Config, preselected []string) ([]string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return nil, fmt.Errorf("--select-services requires an interactive terminal (no TTY detected)")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("mdp.yaml declares no services to select from")
	}
	sort.Strings(names)

	explicit := make(map[string]bool, len(preselected))
	for _, n := range preselected {
		n = strings.TrimSpace(n)
		if _, ok := cfg.Services[n]; ok {
			explicit[n] = true
		}
	}

	p := tea.NewProgram(newServiceSelectModel(cfg, names, explicit), tea.WithContext(ctx), tea.WithoutSignalHandler())
	final, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("service selector: %w", err)
	}
	fm := final.(serviceSelectModel)
	if fm.cancelled || !fm.done {
		return nil, fmt.Errorf("service selection cancelled")
	}

	selected := make([]string, 0, len(fm.explicit))
	for name := range fm.explicit {
		selected = append(selected, name)
	}
	sort.Strings(selected)
	return selected, nil
}
