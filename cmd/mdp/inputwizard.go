package main

import (
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/derekgould/multi-dev-proxy/internal/config"
)

// inputStep is one input actually prompted by the wizard — resolveInputs has
// already applied the skip-to-default / degrade-to-free-text logic before
// building this, so every step here is shown to the user. choices is nil for
// a free-text step, or the pick-list (active groups + trailing @{current}
// sentinel) for a `choices: groups` step with at least one active group.
type inputStep struct {
	spec    config.InputSpec
	choices []string
}

// inputWizardModel drives a single Bubble Tea program through every step in
// sequence: one textinput per step, pre-filled with the default, with an
// optional pick-list rendered below it. Up/Down browses the pick-list and
// overwrites the textinput with the highlighted choice; typing edits the
// textinput directly. Enter submits whatever the textinput currently holds.
type inputWizardModel struct {
	steps  []inputStep
	values map[string]string

	step   int
	input  textinput.Model
	cursor int // index into steps[step].choices; -1 when nothing is highlighted
	errMsg string

	cancelled    bool
	currentGroup string
}

func newInputWizardModel(steps []inputStep, currentGroup string) inputWizardModel {
	m := inputWizardModel{
		steps:        steps,
		values:       make(map[string]string, len(steps)),
		currentGroup: currentGroup,
		input:        textinput.New(),
	}
	// Focused immediately (not just in Init) so the model works standalone in
	// tests that drive Update() directly without a running tea.Program.
	m.input.Focus()
	return m.enterStep(0)
}

// enterStep resets the textinput/cursor for step i. The default is shown as
// a placeholder rather than pre-filled text — typing immediately replaces it
// instead of appending to it — and still submits on Enter with an empty box,
// matching the old "press enter to accept the default" behavior. The
// pick-list still highlights the default's entry, if it has one.
func (m inputWizardModel) enterStep(i int) inputWizardModel {
	m.step = i
	m.errMsg = ""
	spec := m.steps[i].spec

	m.input.SetValue("")
	m.input.Placeholder = ""
	if spec.HasDefault {
		m.input.Placeholder = spec.Default
	}

	m.cursor = -1
	for idx, c := range m.steps[i].choices {
		if c == spec.Default {
			m.cursor = idx
			break
		}
	}
	return m
}

func (m inputWizardModel) Init() tea.Cmd {
	return m.input.Focus()
}

func (m inputWizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	choices := m.steps[m.step].choices
	switch keyMsg.String() {
	case "ctrl+c", "esc":
		m.cancelled = true
		return m, tea.Quit
	case "up":
		if len(choices) > 0 {
			m.cursor--
			if m.cursor < 0 {
				m.cursor = len(choices) - 1
			}
			m.input.SetValue(choices[m.cursor])
			m.input.CursorEnd()
		}
		return m, nil
	case "down":
		if len(choices) > 0 {
			m.cursor = (m.cursor + 1) % len(choices)
			m.input.SetValue(choices[m.cursor])
			m.input.CursorEnd()
		}
		return m, nil
	case "enter":
		return m.submit()
	}

	m.cursor = -1 // manual edit no longer matches any pick-list entry
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// submit validates and records the current step's answer — same rules as the
// old promptInput: empty falls back to the default (or re-prompts with no
// default), and a value containing ${...} is rejected so inputs stay plain
// literals. Advances to the next step, or quits once the last one is answered.
func (m inputWizardModel) submit() (tea.Model, tea.Cmd) {
	spec := m.steps[m.step].spec
	value := strings.TrimSpace(m.input.Value())
	if value == "" {
		if !spec.HasDefault {
			m.errMsg = "a value is required"
			return m, nil
		}
		value = spec.Default
	}
	if strings.Contains(value, "${") {
		m.errMsg = fmt.Sprintf("input %q: value must be a plain literal (it cannot contain ${...} references)", spec.Name)
		return m, nil
	}

	m.values[spec.Name] = value
	if m.step+1 >= len(m.steps) {
		return m, tea.Quit
	}
	return m.enterStep(m.step + 1), nil
}

func (m inputWizardModel) View() tea.View {
	spec := m.steps[m.step].spec
	label := spec.Prompt
	if label == "" {
		label = spec.Name
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n", label, m.input.View())
	for i, c := range m.steps[m.step].choices {
		cursor := "  "
		if i == m.cursor {
			cursor = "▸ "
		}
		marker := ""
		if c == spec.Default {
			marker = " (default)"
		}
		// Annotated with the workspace's group; a service-level `group:`
		// override resolves @{current} to its own group instead (effectiveGroup).
		if c == currentGroupSentinel {
			marker = fmt.Sprintf(" — this checkout's default group (%s)%s", m.currentGroup, marker)
		}
		fmt.Fprintf(&b, "%s%s%s\n", cursor, c, marker)
	}
	if m.errMsg != "" {
		fmt.Fprintf(&b, "  %s\n", m.errMsg)
	}
	fmt.Fprintf(&b, "(%d/%d) enter to confirm", m.step+1, len(m.steps))
	if len(m.steps[m.step].choices) > 0 {
		b.WriteString(", ↑/↓ to browse")
	}
	b.WriteString(", esc to cancel\n")
	return tea.NewView(b.String())
}

// runInputWizard prompts for every step in order in a single Bubble Tea
// program and returns the collected {name -> value} map. It requires stdin
// to be a real terminal — the caller must check that first.
func runInputWizard(steps []inputStep, currentGroup string) (map[string]string, error) {
	p := tea.NewProgram(newInputWizardModel(steps, currentGroup), tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("run input prompt: %w", err)
	}
	fm := final.(inputWizardModel)
	if fm.cancelled {
		return nil, fmt.Errorf("input %q cancelled", fm.steps[fm.step].spec.Name)
	}
	return fm.values, nil
}
