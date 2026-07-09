package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Fixed chrome geometry: header (y=0), tab bar (y=1), rule (y=2), content,
// rule (height-2), footer (height-1).
const (
	tabBarY    = 1
	contentTop = 3
	chromeRows = 5
)

type rowKind int

const (
	rowText   rowKind = iota // non-interactive: headers, sections, spacers, empty states
	rowItem                  // selectable; bound to items[itemIndex]
	rowMember                // group member line; selects/activates its parent group
)

// rowCtx is what a row needs to render itself for the current frame.
type rowCtx struct {
	st       *styles
	th       *theme
	width    int
	selected bool
	hovered  bool
}

// row is one content line: geometry and interactivity are fixed at build
// time, styling is resolved per frame via the render closure.
type row struct {
	kind      rowKind
	itemIndex int    // -1 unless kind == rowItem
	group     string // non-empty joins the group hover/click span
	render    func(rc rowCtx) string
}

func textRow(render func(rc rowCtx) string) row {
	return row{kind: rowText, itemIndex: -1, render: render}
}

func blankRow() row {
	return textRow(func(rowCtx) string { return "" })
}

// bg returns the row background for the current frame, if any: selection
// wins over hover.
func (rc rowCtx) bg() (color.Color, bool) {
	switch {
	case rc.selected:
		return rc.th.selBg, true
	case rc.hovered:
		return rc.th.hoverBg, true
	}
	return nil, false
}

// finish pads a rendered line to the full content width, extending the row
// background when one is active.
func (rc rowCtx) finish(line string) string {
	if c, ok := rc.bg(); ok {
		return padLineBg(line, rc.width, c)
	}
	return padLine(line, rc.width)
}

func emptyStateRows(text string) []row {
	return []row{
		blankRow(),
		textRow(func(rc rowCtx) string {
			return lipgloss.PlaceHorizontal(rc.width, lipgloss.Center, rc.st.faint.Render(text))
		}),
	}
}

func noMatchRows(filter string) []row {
	return emptyStateRows("no matches for \"" + filter + "\" — esc to clear")
}

type tabRange struct {
	x0, x1 int
}

type hit struct {
	kind  string // "tab", "item", "group", "none"
	index int    // tab index or item index
	group string
}

// hitTest maps a screen coordinate to what it touches. Pure: depends only on
// the cached rows, tab ranges, and scroll offset.
func (m *Model) hitTest(x, y int) hit {
	if y == tabBarY {
		for i, tr := range m.tabRanges {
			if x >= tr.x0 && x < tr.x1 {
				return hit{kind: "tab", index: i}
			}
		}
		return hit{kind: "none"}
	}
	cy := y - contentTop + m.scroll[m.tab]
	if y >= contentTop && y < contentTop+m.windowHeight() && cy >= 0 && cy < len(m.rows) {
		r := m.rows[cy]
		switch {
		case r.group != "":
			return hit{kind: "group", index: m.findItemIndex("group", r.group, 0), group: r.group}
		case r.kind == rowItem && r.itemIndex >= 0:
			return hit{kind: "item", index: r.itemIndex}
		}
	}
	return hit{kind: "none"}
}

// windowHeight is the number of content rows that fit on screen.
func (m *Model) windowHeight() int {
	if m.height <= 0 {
		return len(m.rows)
	}
	h := m.height - chromeRows
	if h < 1 {
		h = 1
	}
	return h
}

func (m *Model) clampScroll() {
	maxScroll := len(m.rows) - m.windowHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll[m.tab] > maxScroll {
		m.scroll[m.tab] = maxScroll
	}
	if m.scroll[m.tab] < 0 {
		m.scroll[m.tab] = 0
	}
}

// ensureCursorVisible scrolls the active tab so the cursor's row is on screen.
func (m *Model) ensureCursorVisible() {
	cursorRow := -1
	for i, r := range m.rows {
		if r.kind == rowItem && r.itemIndex == m.cursor {
			cursorRow = i
			break
		}
	}
	if cursorRow < 0 {
		m.clampScroll()
		return
	}
	win := m.windowHeight()
	switch {
	case m.cursor == 0:
		// Reveal the header rows above the first item.
		m.scroll[m.tab] = 0
	case m.cursor == len(m.items)-1:
		// Last item: scroll to the end so trailing non-item rows (e.g. the
		// final group's member lines) are reachable.
		m.scroll[m.tab] = len(m.rows) - win
	default:
		if cursorRow < m.scroll[m.tab] {
			m.scroll[m.tab] = cursorRow
		}
		if cursorRow >= m.scroll[m.tab]+win {
			m.scroll[m.tab] = cursorRow - win + 1
		}
	}
	m.clampScroll()
}
