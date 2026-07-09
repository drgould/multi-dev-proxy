package tui

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// renderConfirmBox is the stop-confirmation modal shown centered in the
// content area while m.confirm is set.
func (m Model) renderConfirmBox() string {
	it := *m.confirm
	title := m.st.statusWarn.Bold(true).Render("Stop service?")
	body := m.st.fg.Render(it.Name) + m.st.dim.Render(fmt.Sprintf("  (PID %d)", it.PID))
	note := m.st.faint.Render("stops the registered process")
	help := m.st.dim.Render("enter confirm · esc cancel")
	inner := lipgloss.JoinVertical(lipgloss.Center, title, "", body, note, "", help)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.th.warn).
		Padding(1, 3).
		Render(inner)
}
