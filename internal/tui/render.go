package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

const colPad = "  "

func padLine(s string, targetW int) string {
	w := lipgloss.Width(s)
	if w < targetW {
		return s + strings.Repeat(" ", targetW-w)
	}
	return s
}

func padLineBg(s string, targetW int, bg color.Color) string {
	w := lipgloss.Width(s)
	if w < targetW {
		return s + lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", targetW-w))
	}
	return s
}

func tableRow(cols []string, widths []int, styles []lipgloss.Style, bgOpt ...color.Color) string {
	var b strings.Builder
	hasBg := len(bgOpt) > 0
	for i, col := range cols {
		s := styles[len(styles)-1]
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

func headerRow(st *styles, cols []string, widths []int) string {
	var b strings.Builder
	for i, col := range cols {
		w := 10
		if i < len(widths) {
			w = widths[i]
		}
		rendered := st.header.Render(col)
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
