package tui

import (
	"bytes"
	"io"
	"regexp"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
)

// The renderer interleaves cursor-movement escapes with text, so substring
// assertions must strip ANSI sequences first.
var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;?<>=]*[a-zA-Z]|\x1b\\][^\x07\x1b]*(\x07|\x1b\\\\)|\x1b[=>]")

func newTestProgram(t *testing.T, b Backend) *teatest.TestModel {
	t.Helper()
	return teatest.NewTestModel(t, New(b, 13100, "test"), teatest.WithInitialTermSize(120, 40))
}

func waitForOutput(t *testing.T, tm *teatest.TestModel, want string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(ansiSeq.ReplaceAll(bts, nil), []byte(want))
	}, teatest.WithDuration(3*time.Second))
}

func TestTeaQuitStopsProgram(t *testing.T) {
	b := newMockBackend(testSnapshot())
	tm := newTestProgram(t, b)

	waitForOutput(t, tm, "mdp")
	tm.Type("q")
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	final := tm.FinalModel(t).(Model)
	if final.Detached {
		t.Error("quit must not report detached")
	}
	if final.DaemonLost {
		t.Error("quit must not report daemon lost")
	}
}

func TestTeaDetach(t *testing.T) {
	b := newMockBackend(testSnapshot())
	tm := newTestProgram(t, b)

	waitForOutput(t, tm, "mdp")
	tm.Type("d")
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	final := tm.FinalModel(t).(Model)
	if !final.Detached {
		t.Error("d should detach")
	}
}

func TestTeaDaemonLost(t *testing.T) {
	b := newMockBackend(testSnapshot())
	tm := newTestProgram(t, b)

	waitForOutput(t, tm, "mdp")
	b.events <- orchestrator.Event{Type: "daemon_lost"}
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	final := tm.FinalModel(t).(Model)
	if !final.DaemonLost {
		t.Error("daemon_lost event should set DaemonLost")
	}
}

func TestTeaMouseClickSwitchesTab(t *testing.T) {
	b := newMockBackend(testSnapshot())
	tm := newTestProgram(t, b)

	waitForOutput(t, tm, "GROUP") // groups tab rendered first for this snapshot

	// Click the Proxies tab label; its x-range is stable for this snapshot:
	// gutter(1) + "Groups 2"(8) + " │ "(3) puts Proxies at x=12.
	tm.Send(tea.MouseClickMsg{X: 13, Y: tabBarY, Button: tea.MouseLeft})
	waitForOutput(t, tm, "SERVER")

	tm.Type("q")
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	final := tm.FinalModel(t).(Model)
	if final.tab != tabProxies {
		t.Errorf("click should switch to Proxies tab, got %d", final.tab)
	}
}

func TestTeaRendersChrome(t *testing.T) {
	b := newMockBackend(testSnapshot())
	tm := newTestProgram(t, b)

	// One WaitFor for both strings: the reader is consumed as it's checked,
	// and the diff renderer emits nothing new on an unchanged screen.
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		clean := ansiSeq.ReplaceAll(bts, nil)
		return bytes.Contains(clean, []byte("connected")) && bytes.Contains(clean, []byte("4 servers"))
	}, teatest.WithDuration(3*time.Second))

	tm.Type("q")
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	_, _ = io.ReadAll(tm.Output())
}
