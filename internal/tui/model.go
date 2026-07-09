package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
)

// Model is the bubbletea model for the TUI.
type Model struct {
	backend     Backend
	snap        orchestrator.Snapshot // cached; the only copy the render path reads
	fetching    bool                  // at most one snapshot fetch in flight
	version     string
	controlPort int

	items  []Item
	rows   []row
	cursor int
	scroll [tabCount]int
	tab    int

	filter      [tabCount]string
	filterInput textinput.Model
	logs        logsState

	hoverRow    int // item index under mouse, -1 = none
	hoverTabIdx int // tab index under mouse, -1 = none
	hoverGroup  string
	tabLabels   []string
	tabRanges   []tabRange

	width  int
	height int
	isDark bool
	th     theme
	st     styles
	keys   keyMap
	help   help.Model

	pending *pendingAction
	confirm *Item // non-nil while the stop confirmation modal is open
	status  status
	gen     int
	spin    spinner.Model

	quitting   bool
	Detached   bool
	DaemonLost bool
}

// New creates a new TUI model backed by the given Backend.
func New(backend Backend, controlPort int, version string) Model {
	m := Model{
		backend:     backend,
		controlPort: controlPort,
		version:     version,
		hoverRow:    -1,
		hoverTabIdx: -1,
		snap:        backend.Snapshot(),
		keys:        newKeyMap(),
		help:        help.New(),
		filterInput: textinput.New(),
		logs:        newLogsState(),
	}
	m.filterInput.Prompt = "/ "
	m.applyTheme(true) // assume dark until the terminal reports its background
	if len(m.snap.Groups) > 0 && len(m.snap.Proxies) > 1 {
		m.tab = tabGroups
	} else {
		m.tab = tabProxies
	}
	m.refreshRows()
	return m
}

func (m *Model) applyTheme(isDark bool) {
	m.isDark = isDark
	m.th = newTheme(isDark)
	m.st = newStyles(m.th)
	m.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(m.st.chromeDim))
	hs := help.DefaultStyles(isDark)
	hs.ShortKey = m.st.chrome.Foreground(m.th.fg)
	hs.ShortDesc = m.st.chromeDim
	hs.ShortSeparator = m.st.chrome.Foreground(m.th.faint)
	m.help.Styles = hs
}

// Init starts listening for backend events and asks the terminal for its
// background color to resolve the light/dark theme.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		waitForEvent(m.backend.Events()),
		func() tea.Msg { return tea.RequestBackgroundColor() },
	)
}

// Update handles input and events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampScroll()
		m.ensureCursorVisible()
		if m.tab == tabLogs {
			m.refreshLogView()
		}
		return m, nil
	case tea.BackgroundColorMsg:
		m.applyTheme(msg.IsDark())
		return m, nil
	case tea.KeyPressMsg:
		// ctrl+c always quits, even with the confirm modal or filter input open.
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
		if m.confirm != nil {
			switch msg.String() {
			case "enter":
				item := *m.confirm
				m.confirm = nil
				return m, m.startStop(item)
			case "esc", "q":
				m.confirm = nil
			}
			return m, nil
		}
		if m.filterInput.Focused() {
			switch msg.String() {
			case "esc":
				m.filterInput.Blur()
				m.filterInput.SetValue("")
				m.filter[m.tab] = ""
				m.refreshRows()
			case "enter":
				m.filterInput.Blur()
			default:
				var cmd tea.Cmd
				m.filterInput, cmd = m.filterInput.Update(msg)
				m.filter[m.tab] = m.filterInput.Value()
				m.refreshRows()
				return m, cmd
			}
			return m, nil
		}
		switch {
		case key.Matches(msg, m.keys.Filter):
			m.filterInput.SetValue(m.filter[m.tab])
			return m, m.filterInput.Focus()
		case key.Matches(msg, m.keys.ClearFilter):
			if m.filter[m.tab] != "" {
				m.filter[m.tab] = ""
				m.filterInput.SetValue("")
				m.refreshRows()
			}
		case key.Matches(msg, m.keys.Detach):
			m.quitting = true
			m.Detached = true
			return m, tea.Quit
		case key.Matches(msg, m.keys.Quit):
			m.quitting = true
			return m, tea.Quit
		case key.Matches(msg, m.keys.NextTab):
			return m, m.setTab((m.tab + 1) % tabCount)
		case key.Matches(msg, m.keys.PrevTab):
			return m, m.setTab((m.tab - 1 + tabCount) % tabCount)
		case key.Matches(msg, m.keys.Tab1):
			return m, m.setTab(tabGroups)
		case key.Matches(msg, m.keys.Tab2):
			return m, m.setTab(tabProxies)
		case key.Matches(msg, m.keys.Tab3):
			return m, m.setTab(tabServices)
		case key.Matches(msg, m.keys.Tab4):
			return m, m.setTab(tabLogs)
		case key.Matches(msg, m.keys.Stop):
			return m, m.requestStop()
		case m.tab == tabLogs && key.Matches(msg, m.keys.LogFollow):
			m.logs.follow = !m.logs.follow
			if m.logs.follow {
				m.logs.vp.GotoBottom()
			}
		case m.tab == tabLogs && key.Matches(msg, m.keys.LogPrev):
			if n := len(m.logs.sources); n > 0 {
				return m, m.selectLogSource((m.logs.source - 1 + n) % n)
			}
		case m.tab == tabLogs && key.Matches(msg, m.keys.LogNext):
			if n := len(m.logs.sources); n > 0 {
				return m, m.selectLogSource((m.logs.source + 1) % n)
			}
		case m.tab == tabLogs && key.Matches(msg, m.keys.Up):
			m.logs.vp.ScrollUp(1)
			m.logs.follow = m.logs.vp.AtBottom()
		case m.tab == tabLogs && key.Matches(msg, m.keys.Down):
			m.logs.vp.ScrollDown(1)
			m.logs.follow = m.logs.vp.AtBottom()
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
				m.ensureCursorVisible()
			}
		case key.Matches(msg, m.keys.Down):
			if m.cursor < len(m.items)-1 {
				m.cursor++
				m.ensureCursorVisible()
			}
		case key.Matches(msg, m.keys.EnterSwitch):
			return m, m.activateItem()
		}
	case tea.MouseMotionMsg:
		if m.confirm != nil {
			return m, nil
		}
		m.updateHover(msg.X, msg.Y)
	case tea.MouseClickMsg:
		if m.confirm != nil {
			return m, nil
		}
		m.updateHover(msg.X, msg.Y)
		if msg.Button == tea.MouseLeft {
			return m, m.handleClick(msg.X, msg.Y)
		}
	case tea.MouseWheelMsg:
		if m.confirm != nil {
			return m, nil
		}
		m.updateHover(msg.X, msg.Y)
		delta := 0
		switch msg.Button {
		case tea.MouseWheelUp:
			delta = -1
		case tea.MouseWheelDown:
			delta = 1
		}
		if delta != 0 {
			switch {
			case m.tab == tabLogs:
				if delta < 0 {
					m.logs.vp.ScrollUp(3)
				} else {
					m.logs.vp.ScrollDown(3)
				}
				m.logs.follow = m.logs.vp.AtBottom()
			case len(m.items) > 0:
				m.cursor = clamp(m.cursor+delta, 0, len(m.items)-1)
				m.ensureCursorVisible()
			default:
				m.scroll[m.tab] += delta
				m.clampScroll()
			}
		}
	case EventMsg:
		if msg.Type == "daemon_lost" {
			m.quitting = true
			m.Detached = true
			m.DaemonLost = true
			return m, tea.Quit
		}
		cmds := []tea.Cmd{waitForEvent(m.backend.Events())}
		if !m.fetching {
			m.fetching = true
			cmds = append(cmds, fetchSnapshot(m.backend))
		}
		if m.tab == tabLogs {
			cmds = append(cmds, fetchLogSources(m.backend))
			if id := m.logs.currentID(); id != "" && !m.logs.fetching {
				m.logs.fetching = true
				cmds = append(cmds, fetchLogChunk(m.backend, id, m.logs.gen, m.logs.offset))
			}
		}
		return m, tea.Batch(cmds...)
	case snapshotMsg:
		m.fetching = false
		m.snap = msg.snap
		m.refreshRows()
		return m, nil
	case actionDoneMsg:
		if m.pending == nil || m.pending.gen != msg.gen {
			return m, nil
		}
		m.pending = nil
		m.gen++
		if msg.err != nil {
			m.status = status{level: statusErr, text: msg.err.Error(), gen: m.gen}
			return m, expireStatus(m.gen, 6*time.Second)
		}
		text := "switched to " + msg.target
		switch msg.verb {
		case "default":
			text = "default set to " + msg.target
		case "stop":
			text = "stopping " + msg.target
		}
		m.status = status{level: statusOK, text: text, gen: m.gen}
		cmds := []tea.Cmd{expireStatus(m.gen, 3 * time.Second)}
		if !m.fetching {
			m.fetching = true
			cmds = append(cmds, fetchSnapshot(m.backend))
		}
		return m, tea.Batch(cmds...)
	case clearStatusMsg:
		if m.status.gen == msg.gen {
			m.status = status{}
		}
		return m, nil
	case spinner.TickMsg:
		if m.pending == nil {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case logSourcesMsg:
		if msg.err != nil {
			m.logs.err = msg.err.Error()
			return m, nil
		}
		m.logs.err = ""
		return m, m.setLogSources(msg.sources)
	case logChunkMsg:
		if msg.gen != m.logs.gen {
			// Stale: the source/cursor was reset after this fetch was issued.
			// Leave `fetching` alone — the current generation's fetch owns it.
			return m, nil
		}
		m.logs.fetching = false
		if msg.err != nil {
			m.logs.err = msg.err.Error()
			return m, nil
		}
		m.logs.err = ""
		if m.logs.offset > 0 && msg.chunk.NextOffset < m.logs.offset {
			// The file shrank below our cursor (truncated or rotated) — restart
			// the tail from the end rather than stranding the cursor past EOF.
			return m, m.selectLogSource(m.logs.source)
		}
		m.logs.appendChunk(msg.chunk)
		m.refreshLogView()
		if msg.chunk.Truncated {
			m.logs.fetching = true
			return m, fetchLogChunk(m.backend, m.logs.currentID(), m.logs.gen, m.logs.offset)
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) setTab(i int) tea.Cmd {
	if i < 0 || i >= tabCount || i == m.tab {
		return nil
	}
	m.tab = i
	m.cursor = 0
	m.hoverRow = -1
	m.hoverGroup = ""
	m.filterInput.Blur()
	m.filterInput.SetValue(m.filter[i])
	m.refreshRows()
	if i == tabLogs {
		return fetchLogSources(m.backend)
	}
	return nil
}

// refreshRows rebuilds the active tab's nav items and content rows from the
// cached snapshot; geometry consumers (render, keyboard, mouse, filtering)
// all read the same row list.
func (m *Model) refreshRows() {
	filter := m.filter[m.tab]
	switch m.tab {
	case tabGroups:
		m.items, m.rows = buildGroupsTab(m.snap, filter)
	case tabProxies:
		m.items, m.rows = buildProxiesTab(m.snap, filter)
	case tabLogs:
		m.items, m.rows = nil, nil
		m.refreshLogView()
	default:
		m.items, m.rows = buildServicesTab(m.snap, filter)
	}
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.rebuildTabBar()
	m.clampScroll()
	m.ensureCursorVisible()
}

// activateItem starts the backend action for the selected item as an async
// command; while one is pending, further activations are ignored.
func (m *Model) activateItem() tea.Cmd {
	if m.pending != nil {
		return nil
	}
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return nil
	}
	item := m.items[m.cursor]
	if item.Kind != "group" && item.Kind != "server" {
		return nil
	}
	verb := "switch"
	if item.Kind == "server" {
		verb = "default"
	}
	m.gen++
	m.pending = &pendingAction{item: item, gen: m.gen, verb: verb}
	return tea.Batch(runAction(m.backend, item, m.gen), m.spin.Tick)
}

// requestStop opens the stop confirmation for the selected row, or explains
// why it can't be stopped.
func (m *Model) requestStop() tea.Cmd {
	if m.tab != tabProxies && m.tab != tabServices {
		return nil
	}
	if m.pending != nil || m.cursor < 0 || m.cursor >= len(m.items) {
		return nil
	}
	item := m.items[m.cursor]
	if item.Kind != "server" && item.Kind != "service" {
		return nil
	}
	if item.PID <= 0 {
		m.gen++
		m.status = status{level: statusWarn, text: item.Name + " is externally managed — cannot stop", gen: m.gen}
		return expireStatus(m.gen, 4*time.Second)
	}
	m.confirm = &item
	return nil
}

// startStop launches the confirmed stop as an async action.
func (m *Model) startStop(item Item) tea.Cmd {
	if m.pending != nil {
		return nil
	}
	m.gen++
	m.pending = &pendingAction{item: item, gen: m.gen, verb: "stop"}
	return tea.Batch(runStopAction(m.backend, item.Name, m.gen), m.spin.Tick)
}

func (m *Model) updateHover(x, y int) {
	m.hoverRow, m.hoverTabIdx, m.hoverGroup = -1, -1, ""
	h := m.hitTest(x, y)
	switch h.kind {
	case "tab":
		m.hoverTabIdx = h.index
	case "group":
		m.hoverGroup = h.group
		m.hoverRow = h.index
	case "item":
		m.hoverRow = h.index
	}
}

func (m *Model) handleClick(x, y int) tea.Cmd {
	h := m.hitTest(x, y)
	switch h.kind {
	case "tab":
		return m.setTab(h.index)
	case "group", "item":
		if h.index < 0 {
			return nil
		}
		if m.cursor == h.index {
			return m.activateItem()
		}
		m.cursor = h.index
		m.ensureCursorVisible()
	}
	return nil
}

func (m *Model) findItemIndex(kind, name string, proxyPort int) int {
	for i, item := range m.items {
		if item.Kind != kind {
			continue
		}
		switch kind {
		case "group":
			if item.GroupName == name {
				return i
			}
		case "server":
			if item.Name == name && item.ProxyPort == proxyPort {
				return i
			}
		}
	}
	return -1
}

// rowState resolves whether a row renders as selected and/or hovered this
// frame. Selection binds to the cursor item; hover covers whole group spans.
func (m Model) rowState(r row) (selected, hovered bool) {
	selected = r.kind == rowItem && r.itemIndex >= 0 && r.itemIndex == m.cursor && len(m.items) > 0
	if r.group != "" {
		gi := m.findItemIndex("group", r.group, 0)
		hovered = r.group == m.hoverGroup && gi != m.cursor
	} else if r.kind == rowItem {
		hovered = r.itemIndex == m.hoverRow && r.itemIndex != m.cursor
	}
	return selected, hovered
}

func (m Model) viewWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 80
}

// View renders the TUI: fixed chrome (header, tabs, rules, footer) around a
// scrollable content window.
func (m Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	if m.quitting {
		return v
	}
	w := m.viewWidth()
	var b strings.Builder
	b.WriteString(m.renderHeader() + "\n")
	b.WriteString(padLine(m.renderTabBar(), w) + "\n")
	b.WriteString(m.renderRule() + "\n")
	switch {
	case m.confirm != nil:
		b.WriteString(lipgloss.Place(w, m.windowHeight(), lipgloss.Center, lipgloss.Center, m.renderConfirmBox()))
		b.WriteString("\n")
	case m.tab == tabLogs:
		b.WriteString(m.renderLogsHeader() + "\n")
		b.WriteString(m.logs.vp.View() + "\n")
	default:
		win := m.windowHeight()
		start := m.scroll[m.tab]
		for i := 0; i < win; i++ {
			idx := start + i
			if idx >= 0 && idx < len(m.rows) {
				r := m.rows[idx]
				selected, hovered := m.rowState(r)
				rc := rowCtx{st: &m.st, th: &m.th, width: w, selected: selected, hovered: hovered}
				b.WriteString(r.render(rc))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString(m.renderRule() + "\n")
	b.WriteString(m.renderFooter())
	v.SetContent(b.String())
	return v
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
