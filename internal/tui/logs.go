package tui

import (
	"fmt"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

const (
	logTailBytes = 64 * 1024 // initial backfill: last 64 KiB
	logMaxLines  = 5000      // in-memory cap per source
)

// logsState holds the Logs tab: a viewport tailing one of the daemon's log
// sources via cursor polling.
type logsState struct {
	sources  []LogSource
	source   int   // index into sources
	gen      int   // bumped on every source (re)selection; guards stale responses
	lines    []string
	offset   int64
	follow   bool
	fetching bool
	err      string
	vp       viewport.Model
}

func newLogsState() logsState {
	vp := viewport.New()
	vp.FillHeight = true
	return logsState{follow: true, vp: vp}
}

func (ls *logsState) currentID() string {
	if ls.source < 0 || ls.source >= len(ls.sources) {
		return ""
	}
	return ls.sources[ls.source].ID
}

func (ls *logsState) appendChunk(c LogChunk) {
	ls.offset = c.NextOffset
	if len(c.Lines) == 0 {
		return
	}
	ls.lines = append(ls.lines, c.Lines...)
	if len(ls.lines) > logMaxLines {
		ls.lines = ls.lines[len(ls.lines)-logMaxLines:]
	}
}

// selectLogSource restarts the tail on the given source index. Bumping gen
// invalidates any response still in flight for the previous source/cursor, so
// a late chunk can't be appended to the new source's buffer.
func (m *Model) selectLogSource(i int) tea.Cmd {
	if i < 0 || i >= len(m.logs.sources) {
		return nil
	}
	m.logs.source = i
	m.logs.gen++
	m.logs.lines = nil
	m.logs.offset = -logTailBytes
	m.logs.follow = true
	m.logs.fetching = true
	m.refreshLogView()
	return fetchLogChunk(m.backend, m.logs.currentID(), m.logs.gen, m.logs.offset)
}

// setLogSources reconciles a fresh source list with the current selection:
// same source keeps tailing, otherwise the tail restarts on the first one.
func (m *Model) setLogSources(sources []LogSource) tea.Cmd {
	prevID := m.logs.currentID()
	m.logs.sources = sources
	m.rebuildTabBar()
	if len(sources) == 0 {
		m.logs.source = 0
		m.logs.lines = nil
		m.refreshLogView()
		return nil
	}
	for i, s := range sources {
		if prevID != "" && s.ID == prevID {
			m.logs.source = i
			return nil
		}
	}
	return m.selectLogSource(0)
}

// refreshLogView resizes the viewport and reloads its content, applying the
// Logs tab filter line-wise.
func (m *Model) refreshLogView() {
	h := m.windowHeight() - 1 // one line for the logs sub-header
	if h < 1 {
		h = 1
	}
	m.logs.vp.SetWidth(m.viewWidth())
	m.logs.vp.SetHeight(h)
	lines := m.logs.lines
	if f := m.filter[tabLogs]; f != "" {
		filtered := make([]string, 0, len(lines))
		for _, l := range lines {
			if matchesFilter(f, l) {
				filtered = append(filtered, l)
			}
		}
		lines = filtered
	}
	m.logs.vp.SetContentLines(lines)
	if m.logs.follow {
		m.logs.vp.GotoBottom()
	}
}

// renderLogsHeader is the one-line sub-header above the log viewport.
func (m Model) renderLogsHeader() string {
	if len(m.logs.sources) == 0 {
		text := "No log sources found"
		if m.logs.err != "" {
			text = "logs unavailable: " + m.logs.err
		}
		return " " + m.st.faint.Render(text)
	}
	cur := m.logs.sources[m.logs.source]
	followText := "● following"
	followStyle := m.st.statusOK
	if !m.logs.follow {
		followText = "○ paused"
		followStyle = m.st.statusWarn
	}
	line := " " + m.st.section.Render(cur.Label) +
		m.st.dim.Render(fmt.Sprintf("  %d/%d", m.logs.source+1, len(m.logs.sources))) +
		"  " + followStyle.Render(followText)
	if f := m.filter[tabLogs]; f != "" {
		line += m.st.dim.Render("  / " + f)
	}
	return line
}
