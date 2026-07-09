package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var tabNames = [tabCount]string{"Groups", "Proxies", "Services", "Logs"}

// rebuildTabBar recomputes the tab labels (with count badges) and their
// x-ranges for hit-testing. Runs on snapshot changes, not per frame.
func (m *Model) rebuildTabBar() {
	counts := [tabCount]int{len(m.snap.Groups), len(m.snap.Proxies), len(collectServices(m.snap)), len(m.logs.sources)}
	m.tabLabels = m.tabLabels[:0]
	m.tabRanges = m.tabRanges[:0]
	x := 1 // left gutter
	for i, name := range tabNames {
		label := name
		if counts[i] > 0 {
			label = fmt.Sprintf("%s %d", name, counts[i])
		}
		start := x
		x += lipgloss.Width(label)
		m.tabLabels = append(m.tabLabels, label)
		m.tabRanges = append(m.tabRanges, tabRange{x0: start, x1: x})
		x += 3 // " │ "
	}
}

func (m Model) renderTabBar() string {
	var b strings.Builder
	b.WriteString(" ")
	for i, label := range m.tabLabels {
		switch {
		case i == m.tab:
			b.WriteString(m.st.tabActive.Render(label))
		case i == m.hoverTabIdx:
			b.WriteString(m.st.tabHover.Render(label))
		default:
			b.WriteString(m.st.tabInactive.Render(label))
		}
		if i < len(m.tabLabels)-1 {
			b.WriteString(m.st.faint.Render(" │ "))
		}
	}
	return b.String()
}

func (m Model) renderHeader() string {
	serverCount := 0
	for _, p := range m.snap.Proxies {
		serverCount += len(p.Servers)
	}
	left := m.st.chrome.Render(" ") +
		m.st.chromeTitle.Render("mdp") +
		m.st.chromeDim.Render(fmt.Sprintf(" %s  •  ctrl :%d  •  %d proxies · %d servers · %d groups",
			m.version, m.controlPort, len(m.snap.Proxies), serverCount, len(m.snap.Groups)))

	var right string
	switch m.backend.ConnState() {
	case ConnReconnecting:
		right = m.st.chromeWarn.Render("◐ reconnecting ")
	case ConnLost:
		right = m.st.chromeErr.Render("✗ disconnected ")
	default:
		right = m.st.chromeOK.Render("● connected ")
	}
	return m.chromeBar(left, right)
}

func (m Model) renderRule() string {
	return m.st.rule.Render(strings.Repeat("─", m.viewWidth()))
}

func (m Model) footerLeft() string {
	gutter := m.st.chrome.Render(" ")
	switch {
	case m.filterInput.Focused():
		return gutter + m.filterInput.View()
	case m.pending != nil:
		var text string
		switch m.pending.verb {
		case "default":
			text = "setting default " + m.pending.item.Name
		case "stop":
			text = "stopping " + m.pending.item.Name
		default:
			text = "switching to " + m.pending.item.GroupName
		}
		return gutter + m.spin.View() + m.st.chromeDim.Render(" "+text)
	case m.status.level == statusOK:
		return gutter + m.st.chromeOK.Render("✓ "+m.status.text)
	case m.status.level == statusWarn:
		return gutter + m.st.chromeWarn.Render("● "+m.status.text)
	case m.status.level == statusErr:
		return gutter + m.st.chromeErr.Render("✗ "+m.status.text)
	case m.filter[m.tab] != "":
		return gutter + m.st.chromeDim.Render("/ "+m.filter[m.tab]+"  (esc to clear)")
	}
	return ""
}

func (m Model) renderFooter() string {
	right := m.help.ShortHelpView(m.keys.forTab(m.tab)) + m.st.chrome.Render(" ")
	return m.chromeBar(m.footerLeft(), right)
}

// chromeBar lays out left and right segments on a full-width chrome
// background, truncating the left segment if space runs out.
func (m Model) chromeBar(left, right string) string {
	w := m.viewWidth()
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	if lw+rw >= w {
		avail := w - rw - 1
		if avail < 0 {
			avail = 0
		}
		left = ansi.Truncate(left, avail, "…")
		lw = lipgloss.Width(left)
	}
	gap := w - lw - rw
	if gap < 0 {
		gap = 0
	}
	return left + m.st.chrome.Render(strings.Repeat(" ", gap)) + right
}
