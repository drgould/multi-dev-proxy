package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	sectionStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	activeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	statusRunning = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	statusStopped = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	tabActive     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Underline(true)
	tabInactive   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	tabHover      = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Underline(true)
	headerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	colDim        = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	hoverBg       = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	hoverBgColor  = lipgloss.Color("236")
	boxStyle      = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(1, 2)
)

const (
	tabGroups   = 0
	tabProxies  = 1
	tabServices = 2
	minBoxWidth = 56
)

// EventMsg wraps an orchestrator event for the TUI update loop.
type EventMsg orchestrator.Event

// Item represents a selectable item in the TUI.
type Item struct {
	Kind      string // "group", "server"
	Label     string
	ProxyPort int
	Name      string
	GroupName string
}

// Model is the bubbletea model for the TUI.
type Model struct {
	backend     Backend
	items       []Item
	cursor      int
	hoverRow    int    // item index under mouse, -1 = none
	hoverTabIdx int    // tab index under mouse, -1 = none
	hoverGroup  string // group name hovered (groups view), "" = none
	tab         int
	tabs        []string
	controlPort int
	width       int
	height      int
	quitting    bool
	Detached    bool
	DaemonLost  bool
	rowYMap     []int
	tabYLine    int
	tabRanges   []tabRange
	groupYSpans []groupYSpan
	contentW    int
}

type tabRange struct {
	x0, x1 int
}

type groupYSpan struct {
	name       string
	yStart     int
	yEnd       int // exclusive
}

// New creates a new TUI model backed by the given Backend.
func New(backend Backend, controlPort int) Model {
	m := Model{
		backend:     backend,
		controlPort: controlPort,
		hoverRow:    -1,
		hoverTabIdx: -1,
	}
	m.rebuildTabs()
	m.refreshItems()
	return m
}

func waitForEvent(events <-chan orchestrator.Event) tea.Cmd {
	return func() tea.Msg {
		e := <-events
		return EventMsg(e)
	}
}

// Init starts listening for backend events.
func (m Model) Init() tea.Cmd {
	return waitForEvent(m.backend.Events())
}

// Update handles input and events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "d":
			m.quitting = true
			m.Detached = true
			return m, tea.Quit
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "tab", "right", "l":
			if len(m.tabs) > 1 {
				m.tab = (m.tab + 1) % len(m.tabs)
				m.cursor = 0
				m.refreshItems()
			}
		case "shift+tab", "left", "h":
			if len(m.tabs) > 1 {
				m.tab = (m.tab - 1 + len(m.tabs)) % len(m.tabs)
				m.cursor = 0
				m.refreshItems()
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter":
			m.activateItem()
		}
	case tea.MouseMsg:
		m.updateHover(msg.X, msg.Y)
		switch msg.Action {
		case tea.MouseActionPress:
			if msg.Button == tea.MouseButtonLeft {
				m.handleClick(msg.X, msg.Y)
			}
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.cursor > 0 {
				m.cursor--
			}
		case tea.MouseButtonWheelDown:
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		}
	case EventMsg:
		if msg.Type == "daemon_lost" {
			m.quitting = true
			m.Detached = true
			m.DaemonLost = true
			return m, tea.Quit
		}
		m.rebuildTabs()
		m.refreshItems()
		return m, waitForEvent(m.backend.Events())
	}
	return m, nil
}

func (m *Model) activateItem() {
	if m.cursor >= 0 && m.cursor < len(m.items) {
		item := m.items[m.cursor]
		switch item.Kind {
		case "group":
			_ = m.backend.SwitchGroup(item.GroupName)
		case "server":
			_ = m.backend.SetDefault(item.ProxyPort, item.Name)
		}
		m.refreshItems()
	}
}

func (m *Model) updateHover(screenX, screenY int) {
	oy, ox := m.innerOffset()
	localY := screenY - oy
	localX := screenX - ox

	m.hoverRow = -1
	m.hoverTabIdx = -1
	m.hoverGroup = ""

	if localY == m.tabYLine && len(m.tabRanges) > 0 {
		for i, tr := range m.tabRanges {
			if localX >= tr.x0 && localX < tr.x1 {
				m.hoverTabIdx = i
				return
			}
		}
	}

	for _, gs := range m.groupYSpans {
		if localY >= gs.yStart && localY < gs.yEnd {
			m.hoverGroup = gs.name
			m.hoverRow = m.findItemIndex("group", gs.name, 0)
			return
		}
	}

	for i, ry := range m.rowYMap {
		if localY == ry {
			m.hoverRow = i
			return
		}
	}
}

func (m *Model) handleClick(screenX, screenY int) {
	oy, ox := m.innerOffset()
	localY := screenY - oy
	localX := screenX - ox

	if localY == m.tabYLine && len(m.tabRanges) > 0 {
		for i, tr := range m.tabRanges {
			if localX >= tr.x0 && localX < tr.x1 {
				if i != m.tab {
					m.tab = i
					m.cursor = 0
					m.refreshItems()
				}
				return
			}
		}
	}

	for _, gs := range m.groupYSpans {
		if localY >= gs.yStart && localY < gs.yEnd {
			idx := m.findItemIndex("group", gs.name, 0)
			if idx >= 0 {
				if m.cursor == idx {
					m.activateItem()
				} else {
					m.cursor = idx
				}
			}
			return
		}
	}

	for i, ry := range m.rowYMap {
		if localY == ry {
			if m.cursor == i {
				m.activateItem()
			} else {
				m.cursor = i
			}
			return
		}
	}
}

func (m *Model) innerOffset() (int, int) {
	if m.width <= 0 || m.height <= 0 {
		return 0, 0
	}
	inner := m.renderInner()
	boxed := boxStyle.Render(inner)
	lines := strings.Split(boxed, "\n")
	boxH := len(lines)
	boxW := 0
	for _, l := range lines {
		w := lipgloss.Width(l)
		if w > boxW {
			boxW = w
		}
	}
	placeY := (m.height - boxH) / 2
	placeX := (m.width - boxW) / 2
	if placeY < 0 {
		placeY = 0
	}
	if placeX < 0 {
		placeX = 0
	}
	// border (1) + padding top (1) = 2, border (1) + padding left (2) = 3
	return placeY + 2, placeX + 3
}

func (m *Model) rebuildTabs() {
	snap := m.backend.Snapshot()
	hasGroups := len(snap.Groups) > 0 && len(snap.Proxies) > 1
	hasServices := hasManagedServices(snap)

	if !hasGroups {
		m.tabs = nil
		m.tab = tabProxies
		return
	}

	m.tabs = []string{"Groups", "Proxies"}
	if hasServices {
		m.tabs = append(m.tabs, "Services")
	}
	if m.tab >= len(m.tabs) {
		m.tab = 0
	}
}

func (m *Model) activeTab() int {
	if len(m.tabs) == 0 {
		return tabProxies
	}
	switch m.tabs[m.tab] {
	case "Groups":
		return tabGroups
	case "Proxies":
		return tabProxies
	case "Services":
		return tabServices
	}
	return tabProxies
}

// View renders the TUI.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	inner := m.renderInner()
	boxed := boxStyle.Render(inner)
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, boxed)
	}
	return boxed
}

func (m *Model) renderInner() string {
	var b strings.Builder
	y := 0

	// title
	titleLine := titleStyle.Render("mdp") + dimStyle.Render(fmt.Sprintf("  ctrl :%d", m.controlPort))
	b.WriteString(padLine(titleLine, m.contentW))
	b.WriteString("\n")
	y++

	// tabs
	m.tabRanges = nil
	if len(m.tabs) > 0 {
		m.tabYLine = y
		var tabLine strings.Builder
		xPos := 0
		for i, name := range m.tabs {
			start := xPos
			switch {
			case i == m.tab:
				tabLine.WriteString(tabActive.Render(name))
			case i == m.hoverTabIdx:
				tabLine.WriteString(tabHover.Render(name))
			default:
				tabLine.WriteString(tabInactive.Render(name))
			}
			xPos += lipgloss.Width(name)
			m.tabRanges = append(m.tabRanges, tabRange{x0: start, x1: xPos})
			if i < len(m.tabs)-1 {
				sep := dimStyle.Render("  │  ")
				tabLine.WriteString(sep)
				xPos += lipgloss.Width(sep)
			}
		}
		b.WriteString(padLine(tabLine.String(), m.contentW))
		b.WriteString("\n")
		y++
	} else {
		m.tabYLine = -1
	}
	b.WriteString("\n")
	y++

	// body
	snap := m.backend.Snapshot()
	m.rowYMap = nil
	m.groupYSpans = nil

	switch m.activeTab() {
	case tabGroups:
		_ = m.renderGroups(&b, snap, y)
	case tabProxies:
		_ = m.renderProxies(&b, snap, y)
	case tabServices:
		_ = m.renderServices(&b, snap, y)
	}

	// help
	help := "↑↓ navigate  enter switch  d detach  q quit"
	if len(m.tabs) > 1 {
		help = "←→ tab  ↑↓ navigate  enter switch  d detach  q quit"
	}
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func padLine(s string, targetW int) string {
	w := lipgloss.Width(s)
	if w < targetW {
		return s + strings.Repeat(" ", targetW-w)
	}
	return s
}

func padLineBg(s string, targetW int, bg lipgloss.Color) string {
	w := lipgloss.Width(s)
	if w < targetW {
		return s + lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", targetW-w))
	}
	return s
}

// ─── table helpers ──────────────────────────────────────────────────────────

const colPad = "  "

func tableRow(cols []string, widths []int, styles []lipgloss.Style, bgOpt ...lipgloss.Color) string {
	var b strings.Builder
	hasBg := len(bgOpt) > 0
	for i, col := range cols {
		s := dimStyle
		if i < len(styles) {
			s = styles[i]
		}
		if hasBg {
			s = s.Background(bgOpt[0])
		}
		w := 10
		if i < len(widths) {
			w = widths[i]
		}
		rendered := s.Render(col)
		visible := lipgloss.Width(rendered)
		b.WriteString(rendered)
		if i < len(cols)-1 {
			pad := w - visible
			if pad < 0 {
				pad = 0
			}
			spacer := strings.Repeat(" ", pad) + colPad
			if hasBg {
				spacer = lipgloss.NewStyle().Background(bgOpt[0]).Render(spacer)
			}
			b.WriteString(spacer)
		}
	}
	return b.String()
}

func headerRow(cols []string, widths []int) string {
	var b strings.Builder
	for i, col := range cols {
		w := 10
		if i < len(widths) {
			w = widths[i]
		}
		rendered := headerStyle.Render(col)
		visible := lipgloss.Width(rendered)
		b.WriteString(rendered)
		if i < len(cols)-1 {
			if visible < w {
				b.WriteString(strings.Repeat(" ", w-visible))
			}
			b.WriteString(colPad)
		}
	}
	return b.String()
}

// ─── groups view ────────────────────────────────────────────────────────────

func (m *Model) renderGroups(b *strings.Builder, snap orchestrator.Snapshot, y int) int {
	groupNames := sortedKeys(snap.Groups)
	serversByGroup := buildServersByGroup(snap)

	colWidths := []int{2, 20, 12}

	b.WriteString("  " + headerRow([]string{"", "GROUP", "STATUS"}, colWidths))
	b.WriteString("\n")
	y++

	for gi, name := range groupNames {
		groupStart := y
		isActive := isGroupActive(snap, name)
		idx := m.findItemIndex("group", name, 0)
		hovered := m.hoverGroup == name && idx != m.cursor

		marker := "  "
		markerStyle := dimStyle
		nameStyle := dimStyle
		statusText := ""
		if isActive {
			marker = "● "
			markerStyle = activeStyle
			nameStyle = activeStyle
			statusText = "active"
		}

		prefix := "  "
		if idx == m.cursor {
			prefix = selectedStyle.Render("▸ ")
			nameStyle = selectedStyle
		}

		var bg []lipgloss.Color
		if hovered {
			bg = []lipgloss.Color{hoverBgColor}
			prefix = hoverBg.Render(prefix)
		}

		row := tableRow(
			[]string{marker, name, statusText},
			colWidths,
			[]lipgloss.Style{markerStyle, nameStyle, activeStyle},
			bg...,
		)
		groupRow := prefix + row
		if hovered {
			groupRow = padLineBg(groupRow, m.contentW, hoverBgColor)
		} else {
			groupRow = padLine(groupRow, m.contentW)
		}
		b.WriteString(groupRow + "\n")
		m.rowYMap = append(m.rowYMap, y)
		y++

		members := serversByGroup[name]
		sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
		for _, srv := range members {
			isDefault := false
			for _, pi := range snap.Proxies {
				if pi.Default == srv.Name {
					isDefault = true
					break
				}
			}
			bStyle := dimStyle
			nStyle := dimStyle
			if isDefault {
				bStyle = activeStyle
				nStyle = activeStyle
			}
			indent := "      "
			sep1 := " "
			sep2 := "  "
			pStyle := dimStyle
			if hovered {
				bStyle = bStyle.Background(hoverBgColor)
				nStyle = nStyle.Background(hoverBgColor)
				pStyle = pStyle.Background(hoverBgColor)
				indent = hoverBg.Render(indent)
				sep1 = hoverBg.Render(sep1)
				sep2 = hoverBg.Render(sep2)
			}
			memberLine := indent + bStyle.Render("•") + sep1 +
				nStyle.Render(fmt.Sprintf("%-18s", srv.Name)) + sep2 +
				pStyle.Render(fmt.Sprintf(":%d", srv.Port))
			if hovered {
				memberLine = padLineBg(memberLine, m.contentW, hoverBgColor)
			} else {
				memberLine = padLine(memberLine, m.contentW)
			}
			b.WriteString(memberLine + "\n")
			y++
		}

		m.groupYSpans = append(m.groupYSpans, groupYSpan{name: name, yStart: groupStart, yEnd: y})

		if gi < len(groupNames)-1 {
			b.WriteString("\n")
			y++
		}
	}
	b.WriteString("\n")
	y++
	return y
}

// ─── proxies view ───────────────────────────────────────────────────────────

func (m *Model) renderProxies(b *strings.Builder, snap orchestrator.Snapshot, y int) int {
	sort.Slice(snap.Proxies, func(i, j int) bool {
		return snap.Proxies[i].Port < snap.Proxies[j].Port
	})

	colWidths := []int{2, 24, 8, 12}

	for pi, proxy := range snap.Proxies {
		label := proxy.Label
		if label == "" {
			label = "proxy"
		}
		b.WriteString(sectionStyle.Render(fmt.Sprintf(":%d  %s", proxy.Port, label)))
		b.WriteString("\n")
		y++

		b.WriteString("  " + headerRow([]string{"", "SERVER", "PORT", "PID"}, colWidths))
		b.WriteString("\n")
		y++

		servers := make([]srvEntry, len(proxy.Servers))
		for i, s := range proxy.Servers {
			servers[i] = srvEntry{Name: s.Name, Port: s.Port, PID: s.PID}
		}
		sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })

		for _, srv := range servers {
			isDefault := srv.Name == proxy.Default
			idx := m.findItemIndex("server", srv.Name, proxy.Port)
			isHov := idx >= 0 && idx == m.hoverRow && idx != m.cursor

			marker := "  "
			markerStyle := dimStyle
			nameStyle := dimStyle
			if isDefault {
				marker = "● "
				markerStyle = activeStyle
				nameStyle = activeStyle
			}

			prefix := "  "
			if idx == m.cursor {
				prefix = selectedStyle.Render("▸ ")
				nameStyle = selectedStyle
			}

			pidStr := "(external)"
			if srv.PID > 0 {
				pidStr = fmt.Sprintf("%d", srv.PID)
			}

			var bg []lipgloss.Color
			if isHov {
				bg = []lipgloss.Color{hoverBgColor}
				prefix = hoverBg.Render(prefix)
			}

			row := tableRow(
				[]string{marker, srv.Name, fmt.Sprintf(":%d", srv.Port), pidStr},
				colWidths,
				[]lipgloss.Style{markerStyle, nameStyle, colDim, colDim},
				bg...,
			)
			full := prefix + row
			if isHov {
				full = padLineBg(full, m.contentW, hoverBgColor)
			} else {
				full = padLine(full, m.contentW)
			}
			b.WriteString(full + "\n")
			m.rowYMap = append(m.rowYMap, y)
			y++
		}

		if pi < len(snap.Proxies)-1 {
			b.WriteString("\n")
			y++
		}
	}
	b.WriteString("\n")
	y++
	return y
}

// ─── services view ──────────────────────────────────────────────────────────

func (m *Model) renderServices(b *strings.Builder, snap orchestrator.Snapshot, y int) int {
	colWidths := []int{24, 10, 8, 10}

	b.WriteString("  " + headerRow([]string{"SERVICE", "GROUP", "PID", "STATUS"}, colWidths))
	b.WriteString("\n")
	y++

	for _, svc := range snap.Services {
		if svc.Status == "" {
			continue
		}
		statusStr := svc.Status
		ss := dimStyle
		if statusStr == "running" {
			ss = statusRunning
		} else if statusStr == "failed" || statusStr == "stopped" {
			ss = statusStopped
		}
		pidStr := ""
		if svc.PID > 0 {
			pidStr = fmt.Sprintf("%d", svc.PID)
		}

		row := tableRow(
			[]string{svc.Name, svc.Group, pidStr, statusStr},
			colWidths,
			[]lipgloss.Style{dimStyle, colDim, colDim, ss},
		)
		b.WriteString("  " + row + "\n")
		y++
	}
	b.WriteString("\n")
	y++
	return y
}

// ─── item management ────────────────────────────────────────────────────────

func (m *Model) refreshItems() {
	snap := m.backend.Snapshot()
	m.items = nil

	// compute content width from snapshot
	m.contentW = minBoxWidth
	switch m.activeTab() {
	case tabProxies:
		for _, pi := range snap.Proxies {
			for _, s := range pi.Servers {
				if w := lipgloss.Width(s.Name) + 20; w > m.contentW {
					m.contentW = w
				}
			}
		}
	}

	switch m.activeTab() {
	case tabGroups:
		groupNames := sortedKeys(snap.Groups)
		for _, name := range groupNames {
			m.items = append(m.items, Item{
				Kind:      "group",
				Label:     name,
				GroupName: name,
			})
		}
	case tabProxies:
		sort.Slice(snap.Proxies, func(i, j int) bool {
			return snap.Proxies[i].Port < snap.Proxies[j].Port
		})
		for _, pi := range snap.Proxies {
			servers := make([]srvEntry, len(pi.Servers))
			for i, s := range pi.Servers {
				servers[i] = srvEntry{Name: s.Name, Port: s.Port, PID: s.PID}
			}
			sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
			for _, srv := range servers {
				m.items = append(m.items, Item{
					Kind:      "server",
					Label:     srv.Name,
					ProxyPort: pi.Port,
					Name:      srv.Name,
				})
			}
		}
	case tabServices:
		// services tab is read-only
	}

	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
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

// ─── data types and helpers ─────────────────────────────────────────────────

type srvEntry struct {
	Name string
	Port int
	PID  int
}

type groupMember struct {
	Name string
	Port int
}

func buildServersByGroup(snap orchestrator.Snapshot) map[string][]groupMember {
	m := make(map[string][]groupMember)
	for _, pi := range snap.Proxies {
		for _, srv := range pi.Servers {
			if srv.Group != "" {
				m[srv.Group] = append(m[srv.Group], groupMember{
					Name: srv.Name,
					Port: srv.Port,
				})
			}
		}
	}
	return m
}

func isGroupActive(snap orchestrator.Snapshot, groupName string) bool {
	for _, pi := range snap.Proxies {
		for _, srv := range pi.Servers {
			if srv.Group == groupName && srv.Name == pi.Default {
				return true
			}
		}
	}
	return false
}

func hasManagedServices(snap orchestrator.Snapshot) bool {
	for _, svc := range snap.Services {
		if svc.Status != "" {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
