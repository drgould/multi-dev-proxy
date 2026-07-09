package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
)

func waitForEvent(events <-chan orchestrator.Event) tea.Cmd {
	return func() tea.Msg {
		e := <-events
		return EventMsg(e)
	}
}

// fetchSnapshot fetches a fresh snapshot off the update loop.
func fetchSnapshot(b Backend) tea.Cmd {
	return func() tea.Msg {
		return snapshotMsg{snap: b.Snapshot()}
	}
}

// runAction executes a backend action off the update loop and reports the
// outcome as an actionDoneMsg.
func runAction(b Backend, item Item, gen int) tea.Cmd {
	return func() tea.Msg {
		switch item.Kind {
		case "group":
			return actionDoneMsg{verb: "switch", target: item.GroupName, gen: gen, err: b.SwitchGroup(item.GroupName)}
		default:
			return actionDoneMsg{verb: "default", target: item.Name, gen: gen, err: b.SetDefault(item.ProxyPort, item.Name)}
		}
	}
}

func expireStatus(gen int, after time.Duration) tea.Cmd {
	return tea.Tick(after, func(time.Time) tea.Msg {
		return clearStatusMsg{gen: gen}
	})
}

// runStopAction asks the daemon to stop a server's process off the update loop.
func runStopAction(b Backend, name string, gen int) tea.Cmd {
	return func() tea.Msg {
		return actionDoneMsg{verb: "stop", target: name, gen: gen, err: b.StopServer(name)}
	}
}

func fetchLogSources(b Backend) tea.Cmd {
	return func() tea.Msg {
		sources, err := b.ListLogs()
		return logSourcesMsg{sources: sources, err: err}
	}
}

func fetchLogChunk(b Backend, id string, gen int, offset int64) tea.Cmd {
	return func() tea.Msg {
		chunk, err := b.FetchLog(id, offset)
		return logChunkMsg{id: id, gen: gen, chunk: chunk, err: err}
	}
}
