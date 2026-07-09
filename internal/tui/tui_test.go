package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

type mockBackend struct {
	snap   orchestrator.Snapshot
	events chan orchestrator.Event
	switchedGroup string
	setDefaultCalls []setDefaultCall
	switchErr     error
	setDefaultErr error
	logSources    []LogSource
	logChunks     map[string][]LogChunk // served in order per id
	logFetches    []string
	stopCalls     []string
	stopErr       error
}

type setDefaultCall struct {
	Port int
	Name string
}

func newMockBackend(snap orchestrator.Snapshot) *mockBackend {
	return &mockBackend{
		snap:   snap,
		events: make(chan orchestrator.Event, 64),
	}
}

func (m *mockBackend) Events() <-chan orchestrator.Event { return m.events }
func (m *mockBackend) Snapshot() orchestrator.Snapshot   { return m.snap }
func (m *mockBackend) ConnState() ConnState              { return ConnConnected }
func (m *mockBackend) SwitchGroup(name string) error {
	m.switchedGroup = name
	return m.switchErr
}
func (m *mockBackend) SetDefault(proxyPort int, name string) error {
	m.setDefaultCalls = append(m.setDefaultCalls, setDefaultCall{Port: proxyPort, Name: name})
	return m.setDefaultErr
}
func (m *mockBackend) StopServer(name string) error {
	m.stopCalls = append(m.stopCalls, name)
	return m.stopErr
}
func (m *mockBackend) ListLogs() ([]LogSource, error) { return m.logSources, nil }
func (m *mockBackend) FetchLog(id string, offset int64) (LogChunk, error) {
	m.logFetches = append(m.logFetches, id)
	chunks := m.logChunks[id]
	if len(chunks) == 0 {
		return LogChunk{NextOffset: offset}, nil
	}
	chunk := chunks[0]
	m.logChunks[id] = chunks[1:]
	return chunk, nil
}

// drainCmd executes a command tree (flattening batches) and returns the
// produced messages. Only safe for commands known not to block.
func drainCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, drainCmd(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func findActionDone(msgs []tea.Msg) (actionDoneMsg, bool) {
	for _, msg := range msgs {
		if d, ok := msg.(actionDoneMsg); ok {
			return d, true
		}
	}
	return actionDoneMsg{}, false
}

func testSnapshot() orchestrator.Snapshot {
	return orchestrator.Snapshot{
		Proxies: []orchestrator.ProxySnapshot{
			{
				Port:    3000,
				Label:   "frontend",
				Default: "app/dev",
				Servers: []registry.ServerEntry{
					{Name: "app/dev", Port: 4001, PID: 100, Group: "dev"},
					{Name: "app/staging", Port: 4002, PID: 200, Group: "staging"},
				},
			},
			{
				Port:    3001,
				Label:   "backend",
				Default: "api/dev",
				Servers: []registry.ServerEntry{
					{Name: "api/dev", Port: 5001, PID: 300, Group: "dev"},
					{Name: "api/staging", Port: 5002, PID: 400, Group: "staging"},
				},
			},
		},
		Groups: map[string][]string{
			"dev":     {"app/dev", "api/dev"},
			"staging": {"app/staging", "api/staging"},
		},
	}
}

func TestSortedKeys(t *testing.T) {
	m := map[string][]string{
		"staging": {"a"},
		"dev":     {"b"},
		"alpha":   {"c"},
	}
	keys := sortedKeys(m)
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	if keys[0] != "alpha" || keys[1] != "dev" || keys[2] != "staging" {
		t.Errorf("expected [alpha dev staging], got %v", keys)
	}
}

func TestSortedKeysEmpty(t *testing.T) {
	keys := sortedKeys(map[string][]string{})
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

func TestBuildServersByGroup(t *testing.T) {
	snap := testSnapshot()
	groups := buildServersByGroup(snap)

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if len(groups["dev"]) != 2 {
		t.Errorf("expected 2 dev members, got %d", len(groups["dev"]))
	}
	if len(groups["staging"]) != 2 {
		t.Errorf("expected 2 staging members, got %d", len(groups["staging"]))
	}
}

func TestBuildServersByGroupSkipsUngrouped(t *testing.T) {
	snap := orchestrator.Snapshot{
		Proxies: []orchestrator.ProxySnapshot{
			{
				Port: 3000,
				Servers: []registry.ServerEntry{
					{Name: "app/main", Port: 4001, Group: ""},
					{Name: "app/dev", Port: 4002, Group: "dev"},
				},
			},
		},
	}
	groups := buildServersByGroup(snap)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group (ungrouped excluded), got %d", len(groups))
	}
	if _, ok := groups["dev"]; !ok {
		t.Error("expected dev group")
	}
}

func TestIsGroupActive(t *testing.T) {
	snap := testSnapshot()
	if !isGroupActive(snap, "dev") {
		t.Error("dev should be active (app/dev is default on proxy 3000)")
	}
	if isGroupActive(snap, "staging") {
		t.Error("staging should not be active")
	}
	if isGroupActive(snap, "nonexistent") {
		t.Error("nonexistent group should not be active")
	}
}

func TestCollectServicesFromRegistry(t *testing.T) {
	svcs := collectServices(testSnapshot())
	if len(svcs) != 4 {
		t.Fatalf("expected 4 registry-derived services, got %d", len(svcs))
	}
	if svcs[0].Name != "api/dev" {
		t.Errorf("expected name-sorted services, got %q first", svcs[0].Name)
	}
	for _, s := range svcs {
		if s.Status != "running" {
			t.Errorf("registered server with PID should imply running, got %q", s.Status)
		}
		if s.ProxyPort == 0 {
			t.Errorf("service %s should carry its proxy port", s.Name)
		}
	}
}

func TestCollectServicesMergesManaged(t *testing.T) {
	snap := testSnapshot()
	snap.Services = []orchestrator.ServiceSnapshot{
		{Name: "app/dev", Group: "dev", Status: "failed", PID: 100},
		{Name: "worker", Group: "dev", Status: "starting", PID: 999},
		{Name: "ignored", Status: ""},
	}
	svcs := collectServices(snap)
	if len(svcs) != 5 {
		t.Fatalf("expected 4 registry + 1 unmatched managed, got %d", len(svcs))
	}
	byName := map[string]svcRow{}
	for _, s := range svcs {
		byName[s.Name] = s
	}
	if byName["app/dev"].Status != "failed" {
		t.Errorf("managed status should override registry-derived, got %q", byName["app/dev"].Status)
	}
	if byName["worker"].Status != "starting" || byName["worker"].PID != 999 {
		t.Errorf("unmatched managed service should be appended, got %+v", byName["worker"])
	}
	if byName["app/dev"].ProxyPort != 3000 {
		t.Error("merge must keep registry proxy port")
	}
}

func TestCollectServicesExternal(t *testing.T) {
	snap := orchestrator.Snapshot{
		Proxies: []orchestrator.ProxySnapshot{
			{Port: 3000, Servers: []registry.ServerEntry{
				{Name: "docker/svc", Port: 4001, PID: 0, Group: "dev"},
			}},
		},
	}
	svcs := collectServices(snap)
	if len(svcs) != 1 || svcs[0].Status != "external" {
		t.Errorf("PID-less server should show external status, got %+v", svcs)
	}
}

func TestFilterNarrowsProxies(t *testing.T) {
	b := newMockBackend(testSnapshot())
	m := New(b, 13100, "test")
	m.setTab(tabProxies)

	m.filter[tabProxies] = "api"
	m.refreshRows()
	if len(m.items) != 2 {
		t.Fatalf("expected 2 api servers, got %d: %+v", len(m.items), m.items)
	}
	for _, it := range m.items {
		if !strings.HasPrefix(it.Name, "api/") {
			t.Errorf("unexpected item %+v", it)
		}
	}

	m.filter[tabProxies] = ""
	m.refreshRows()
	if len(m.items) != 4 {
		t.Errorf("clearing the filter should restore all items, got %d", len(m.items))
	}
}

func TestFilterGroupsByMemberName(t *testing.T) {
	b := newMockBackend(testSnapshot())
	m := New(b, 13100, "test")
	m.setTab(tabProxies)
	m.setTab(tabGroups)

	// "app/s" only matches the member app/staging, so only its parent group
	// should remain.
	m.filter[tabGroups] = "app/s"
	m.refreshRows()
	if len(m.items) != 1 || m.items[0].GroupName != "staging" {
		t.Fatalf("expected only staging group, got %+v", m.items)
	}
}

func TestFilterNoMatches(t *testing.T) {
	b := newMockBackend(testSnapshot())
	m := New(b, 13100, "test")
	m.setTab(tabProxies)

	m.filter[tabProxies] = "zzz"
	m.refreshRows()
	if len(m.items) != 0 {
		t.Fatalf("expected no items, got %d", len(m.items))
	}
	found := false
	rc := rowCtx{st: &m.st, th: &m.th, width: 80}
	for _, r := range m.rows {
		if strings.Contains(r.render(rc), "no matches") {
			found = true
		}
	}
	if !found {
		t.Error("expected a no-matches empty state row")
	}
}

func TestFilterKeyFlow(t *testing.T) {
	b := newMockBackend(testSnapshot())
	m := New(b, 13100, "test")
	m.setTab(tabProxies)

	// "/" focuses the filter input.
	next, _ := m.Update(tea.KeyPressMsg{Text: "/", Code: '/'})
	mm := next.(Model)
	if !mm.filterInput.Focused() {
		t.Fatal("/ should focus the filter input")
	}

	// Typed characters go to the filter, not global bindings ("q" must not quit).
	next, _ = mm.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
	mm = next.(Model)
	if mm.quitting {
		t.Fatal("q while filtering must not quit")
	}
	if mm.filter[tabProxies] != "q" {
		t.Fatalf("expected filter %q, got %q", "q", mm.filter[tabProxies])
	}
	if len(mm.items) != 0 {
		t.Errorf("filter should apply live, got %d items", len(mm.items))
	}

	// enter accepts and blurs, keeping the filter.
	next, _ = mm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	mm = next.(Model)
	if mm.filterInput.Focused() {
		t.Error("enter should blur the filter input")
	}
	if mm.filter[tabProxies] != "q" {
		t.Error("enter should keep the filter")
	}

	// esc (unfocused) clears it.
	next, _ = mm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	mm = next.(Model)
	if mm.filter[tabProxies] != "" {
		t.Error("esc should clear the filter")
	}
	if len(mm.items) != 4 {
		t.Errorf("clearing should restore items, got %d", len(mm.items))
	}
}

func TestLogsTabFlow(t *testing.T) {
	b := newMockBackend(testSnapshot())
	b.logSources = []LogSource{{ID: "daemon", Label: "daemon"}, {ID: "run-app_dev", Label: "run app_dev"}}
	b.logChunks = map[string][]LogChunk{
		"daemon":      {{Lines: []string{"d1", "d2"}, NextOffset: 10}},
		"run-app_dev": {{Lines: []string{"r1"}, NextOffset: 5}},
	}
	m := New(b, 13100, "test")
	m.width, m.height = 100, 30

	// Entering the Logs tab fetches sources.
	cmd := m.setTab(tabLogs)
	if cmd == nil {
		t.Fatal("entering logs tab should fetch sources")
	}
	msgs := drainCmd(cmd)
	if len(msgs) != 1 {
		t.Fatalf("expected one sources msg, got %d", len(msgs))
	}

	// Sources arrive: first source is selected and its tail starts.
	next, cmd := m.Update(msgs[0])
	mm := next.(Model)
	if mm.logs.currentID() != "daemon" {
		t.Fatalf("expected daemon selected, got %q", mm.logs.currentID())
	}
	if cmd == nil {
		t.Fatal("selecting a source should fetch its tail")
	}
	if mm.tabLabels[tabLogs] != "Logs 2" {
		t.Errorf("logs badge should count sources, got %q", mm.tabLabels[tabLogs])
	}

	// Chunk arrives: lines land, cursor advances, follow stays on.
	next, _ = mm.Update(drainCmd(cmd)[0])
	mm = next.(Model)
	if len(mm.logs.lines) != 2 || mm.logs.lines[0] != "d1" {
		t.Fatalf("unexpected lines: %+v", mm.logs.lines)
	}
	if mm.logs.offset != 10 {
		t.Errorf("expected cursor 10, got %d", mm.logs.offset)
	}
	if !strings.Contains(mm.View().Content, "d2") {
		t.Error("viewport should render the log lines")
	}

	// "]" cycles to the run log and restarts the tail.
	next, cmd = mm.Update(tea.KeyPressMsg{Text: "]", Code: ']'})
	mm = next.(Model)
	if mm.logs.currentID() != "run-app_dev" {
		t.Fatalf("expected run log selected, got %q", mm.logs.currentID())
	}
	if len(mm.logs.lines) != 0 {
		t.Error("switching sources should clear buffered lines")
	}
	next, _ = mm.Update(drainCmd(cmd)[0])
	mm = next.(Model)
	if len(mm.logs.lines) != 1 || mm.logs.lines[0] != "r1" {
		t.Errorf("unexpected run log lines: %+v", mm.logs.lines)
	}
}

func TestLogsStaleChunkIgnored(t *testing.T) {
	b := newMockBackend(testSnapshot())
	b.logSources = []LogSource{{ID: "daemon", Label: "daemon"}, {ID: "run-x", Label: "run x"}}
	m := New(b, 13100, "test")
	m.width, m.height = 100, 30
	_ = m.setTab(tabLogs)

	// Sources arrive → daemon selected, gen 1, a fetch in flight.
	next, _ := m.Update(logSourcesMsg{sources: b.logSources})
	m = next.(Model)
	// Switch to run-x → gen 2, run-x fetch in flight, buffer cleared.
	next, _ = m.Update(tea.KeyPressMsg{Text: "]", Code: ']'})
	m = next.(Model)
	genNow := m.logs.gen

	// The earlier daemon fetch (gen 1) resolves late: it must be discarded and
	// must NOT release the single-flight guard the run-x fetch now holds.
	next, _ = m.Update(logChunkMsg{id: "daemon", gen: 1, chunk: LogChunk{Lines: []string{"stale"}, NextOffset: 500}})
	m = next.(Model)
	if len(m.logs.lines) != 0 {
		t.Errorf("stale chunk must not append to the new source, got %v", m.logs.lines)
	}
	if !m.logs.fetching {
		t.Error("stale chunk must not clear the in-flight guard")
	}
	if m.logs.gen != genNow {
		t.Error("stale chunk must not change the generation")
	}
}

func TestLogsShrinkRestartsTail(t *testing.T) {
	b := newMockBackend(testSnapshot())
	b.logSources = []LogSource{{ID: "daemon", Label: "daemon"}}
	b.logChunks = map[string][]LogChunk{"daemon": {{Lines: []string{"a"}, NextOffset: 20}}}
	m := New(b, 13100, "test")
	m.width, m.height = 100, 30
	_ = m.setTab(tabLogs)
	next, cmd := m.Update(logSourcesMsg{sources: b.logSources})
	m = next.(Model)
	next, _ = m.Update(drainCmd(cmd)[0]) // append → offset 20
	m = next.(Model)
	if m.logs.offset != 20 {
		t.Fatalf("precondition: expected offset 20, got %d", m.logs.offset)
	}
	genBefore := m.logs.gen

	// A poll returns a cursor behind our position → the file shrank; restart.
	next, cmd = m.Update(logChunkMsg{id: "daemon", gen: genBefore, chunk: LogChunk{Lines: []string{"boom"}, NextOffset: 5}})
	m = next.(Model)
	if m.logs.gen != genBefore+1 {
		t.Error("shrink should restart the tail (new generation)")
	}
	if len(m.logs.lines) != 0 {
		t.Errorf("shrink should clear the buffer, got %v", m.logs.lines)
	}
	if m.logs.offset != -logTailBytes {
		t.Errorf("shrink should re-tail from the end, got offset %d", m.logs.offset)
	}
	if cmd == nil {
		t.Error("shrink should issue a fresh fetch")
	}
}

func TestLogsChunkTruncatedFetchesMore(t *testing.T) {
	b := newMockBackend(testSnapshot())
	b.logSources = []LogSource{{ID: "daemon", Label: "daemon"}}
	b.logChunks = map[string][]LogChunk{
		"daemon": {
			{Lines: []string{"a"}, NextOffset: 2, Truncated: true},
			{Lines: []string{"b"}, NextOffset: 4},
		},
	}
	m := New(b, 13100, "test")
	m.width, m.height = 100, 30
	cmd := m.setTab(tabLogs)
	next, cmd := m.Update(drainCmd(cmd)[0])
	mm := next.(Model)

	// First chunk is truncated → an immediate follow-up fetch is issued.
	next, cmd = mm.Update(drainCmd(cmd)[0])
	mm = next.(Model)
	if cmd == nil {
		t.Fatal("truncated chunk should trigger a follow-up fetch")
	}
	next, _ = mm.Update(drainCmd(cmd)[0])
	mm = next.(Model)
	if len(mm.logs.lines) != 2 || mm.logs.offset != 4 {
		t.Errorf("expected both chunks consumed, got %+v offset %d", mm.logs.lines, mm.logs.offset)
	}
}

func TestLogsFollowToggleAndFilter(t *testing.T) {
	b := newMockBackend(testSnapshot())
	b.logSources = []LogSource{{ID: "daemon", Label: "daemon"}}
	b.logChunks = map[string][]LogChunk{
		"daemon": {{Lines: []string{"alpha line", "beta line"}, NextOffset: 20}},
	}
	m := New(b, 13100, "test")
	m.width, m.height = 100, 30
	cmd := m.setTab(tabLogs)
	next, cmd := m.Update(drainCmd(cmd)[0])
	mm := next.(Model)
	next, _ = mm.Update(drainCmd(cmd)[0])
	mm = next.(Model)

	next, _ = mm.Update(tea.KeyPressMsg{Text: "f", Code: 'f'})
	mm = next.(Model)
	if mm.logs.follow {
		t.Error("f should toggle follow off")
	}
	if !strings.Contains(mm.View().Content, "paused") {
		t.Error("sub-header should show paused")
	}

	// Line-wise filter narrows the viewport content.
	mm.filter[tabLogs] = "beta"
	mm.refreshLogView()
	content := mm.View().Content
	if strings.Contains(content, "alpha line") || !strings.Contains(content, "beta line") {
		t.Error("filter should narrow log lines")
	}
}

func TestCtrlCQuitsDuringModalAndFilter(t *testing.T) {
	b := newMockBackend(testSnapshot())

	// Modal open → ctrl+c still quits.
	m := New(b, 13100, "test")
	_ = m.setTab(tabProxies)
	m.cursor = 0
	next, _ := m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	m = next.(Model)
	if m.confirm == nil {
		t.Fatal("precondition: confirm modal open")
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = next.(Model)
	if !m.quitting || cmd == nil {
		t.Error("ctrl+c should quit with the modal open")
	}

	// Filter focused → ctrl+c still quits.
	m2 := New(b, 13100, "test")
	_ = m2.setTab(tabProxies)
	next, _ = m2.Update(tea.KeyPressMsg{Text: "/", Code: '/'})
	m2 = next.(Model)
	if !m2.filterInput.Focused() {
		t.Fatal("precondition: filter focused")
	}
	next, cmd = m2.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m2 = next.(Model)
	if !m2.quitting || cmd == nil {
		t.Error("ctrl+c should quit while filtering")
	}
}

func TestProxiesZeroServerSectionVisible(t *testing.T) {
	snap := orchestrator.Snapshot{
		Proxies: []orchestrator.ProxySnapshot{
			{Port: 3000, Label: "frontend"}, // no servers
			{Port: 3001, Label: "backend", Servers: []registry.ServerEntry{
				{Name: "api/dev", Port: 5001, PID: 1, Group: "dev"},
			}},
		},
	}
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.width, m.height = 100, 30
	content := m.View().Content
	if !strings.Contains(content, "frontend") || !strings.Contains(content, "no servers registered") {
		t.Errorf("empty proxy section should stay visible, got:\n%s", content)
	}
}

func TestGroupsTrailingMembersReachable(t *testing.T) {
	snap := orchestrator.Snapshot{
		Proxies: []orchestrator.ProxySnapshot{
			{Port: 3000, Default: "a/dev", Servers: []registry.ServerEntry{
				{Name: "a/dev", Port: 4001, PID: 1, Group: "ga"},
				{Name: "a/x", Port: 4002, PID: 2, Group: "ga"},
				{Name: "b/dev", Port: 4003, PID: 3, Group: "gb"},
				{Name: "b/x", Port: 4004, PID: 4, Group: "gb"},
				{Name: "c/dev", Port: 4005, PID: 5, Group: "gc"},
				{Name: "c/x", Port: 4006, PID: 6, Group: "gc"},
			}},
			{Port: 3001, Servers: []registry.ServerEntry{{Name: "z/dev", Port: 4100, PID: 7, Group: "ga"}}},
		},
		Groups: map[string][]string{"ga": {"a/dev", "a/x", "z/dev"}, "gb": {"b/dev", "b/x"}, "gc": {"c/dev", "c/x"}},
	}
	b := newMockBackend(snap)
	m := New(b, 13100, "test") // multi-proxy + groups → starts on Groups
	if m.tab != tabGroups {
		t.Fatalf("precondition: expected Groups tab, got %d", m.tab)
	}
	m.width, m.height = 100, chromeRows+5 // 5 visible content rows

	m.cursor = len(m.items) - 1 // last group
	m.ensureCursorVisible()
	if m.scroll[tabGroups]+m.windowHeight() < len(m.rows) {
		t.Errorf("last group's trailing member rows unreachable: scroll=%d win=%d rows=%d",
			m.scroll[tabGroups], m.windowHeight(), len(m.rows))
	}
}

func TestStopFlowConfirmAndRun(t *testing.T) {
	b := newMockBackend(testSnapshot())
	m := New(b, 13100, "test")
	m.width, m.height = 100, 30
	m.setTab(tabProxies)
	m.cursor = 0 // app/dev (proxy 3000 sorts first), PID 100

	// x opens the confirmation.
	next, _ := m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	mm := next.(Model)
	if mm.confirm == nil {
		t.Fatal("x should open the stop confirmation")
	}
	if !strings.Contains(mm.View().Content, "Stop service?") {
		t.Error("confirm modal should render")
	}

	// q while confirming must cancel, not quit.
	next, _ = mm.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
	mm = next.(Model)
	if mm.quitting {
		t.Fatal("q during confirm must not quit")
	}
	if mm.confirm != nil {
		t.Fatal("q should cancel the confirmation")
	}

	// Re-open and confirm with enter.
	next, _ = mm.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	mm = next.(Model)
	next, cmd := mm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	mm = next.(Model)
	if mm.confirm != nil {
		t.Error("enter should close the confirmation")
	}
	if mm.pending == nil || mm.pending.verb != "stop" {
		t.Fatalf("expected pending stop, got %+v", mm.pending)
	}
	done, ok := findActionDone(drainCmd(cmd))
	if !ok {
		t.Fatal("expected an actionDoneMsg")
	}
	if len(b.stopCalls) != 1 || b.stopCalls[0] != "app/dev" {
		t.Fatalf("expected StopServer(app/dev), got %v", b.stopCalls)
	}
	next, _ = mm.Update(done)
	mm = next.(Model)
	if mm.status.level != statusOK || !strings.Contains(mm.status.text, "stopping app/dev") {
		t.Errorf("unexpected status: %+v", mm.status)
	}
}

func TestStopExternalRejected(t *testing.T) {
	snap := testSnapshot()
	snap.Proxies[0].Servers[0].PID = 0 // app/dev becomes external
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.setTab(tabProxies)
	m.cursor = 1 // app/dev (sorted after api/... no: proxy 3000 sorted first, servers sorted → app/dev, app/staging)

	// Find app/dev's index explicitly to keep the test robust.
	m.cursor = m.findItemIndex("server", "app/dev", 3000)
	if m.cursor < 0 {
		t.Fatal("app/dev item not found")
	}
	next, _ := m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	mm := next.(Model)
	if mm.confirm != nil {
		t.Error("external process must not open a confirmation")
	}
	if mm.status.level != statusWarn || !strings.Contains(mm.status.text, "externally managed") {
		t.Errorf("expected warn status, got %+v", mm.status)
	}
	if len(b.stopCalls) != 0 {
		t.Error("StopServer must not be called")
	}
}

func TestStopIgnoredOnGroupsTab(t *testing.T) {
	b := newMockBackend(testSnapshot())
	m := New(b, 13100, "test")
	m.setTab(tabProxies)
	m.setTab(tabGroups)
	m.cursor = 0

	next, _ := m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	mm := next.(Model)
	if mm.confirm != nil {
		t.Error("x on the Groups tab should do nothing")
	}
}

func TestStopErrorSurfaces(t *testing.T) {
	b := newMockBackend(testSnapshot())
	b.stopErr = errors.New("stop api/dev: server has no PID (externally managed)")
	m := New(b, 13100, "test")
	m.setTab(tabProxies)
	m.cursor = 0

	next, _ := m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	mm := next.(Model)
	next, cmd := mm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	mm = next.(Model)
	done, _ := findActionDone(drainCmd(cmd))
	next, _ = mm.Update(done)
	mm = next.(Model)
	if mm.status.level != statusErr || !strings.Contains(mm.status.text, "no PID") {
		t.Errorf("expected error status, got %+v", mm.status)
	}
}

func TestFilterPersistsPerTab(t *testing.T) {
	b := newMockBackend(testSnapshot())
	m := New(b, 13100, "test")
	m.setTab(tabProxies)
	m.filter[tabProxies] = "api"
	m.refreshRows()

	m.setTab(tabGroups)
	if len(m.items) != 2 {
		t.Errorf("groups tab should be unfiltered, got %d items", len(m.items))
	}
	if m.filterInput.Value() != "" {
		t.Errorf("filter input should sync to the new tab, got %q", m.filterInput.Value())
	}

	m.setTab(tabProxies)
	if len(m.items) != 2 {
		t.Errorf("proxies filter should persist, got %d items", len(m.items))
	}
	if m.filterInput.Value() != "api" {
		t.Errorf("filter input should restore the tab's filter, got %q", m.filterInput.Value())
	}
}

func TestModelInitialTabMultiProxy(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")

	if m.tab != tabGroups {
		t.Errorf("multi-proxy with groups should start on Groups, got %d", m.tab)
	}
	if len(m.tabLabels) != tabCount {
		t.Fatalf("expected %d tab labels, got %v", tabCount, m.tabLabels)
	}
	if m.tabLabels[0] != "Groups 2" || m.tabLabels[1] != "Proxies 2" {
		t.Errorf("expected count badges, got %v", m.tabLabels)
	}
	if m.tabLabels[2] != "Services 4" {
		t.Errorf("services badge should count registry-derived services, got %q", m.tabLabels[2])
	}
	if len(m.tabRanges) != tabCount {
		t.Fatalf("expected %d tab ranges, got %d", tabCount, len(m.tabRanges))
	}
}

func TestModelInitialTabSingleProxy(t *testing.T) {
	snap := orchestrator.Snapshot{
		Proxies: []orchestrator.ProxySnapshot{
			{Port: 3000, Servers: []registry.ServerEntry{
				{Name: "app/dev", Port: 4001, Group: "dev"},
			}},
		},
		Groups: map[string][]string{"dev": {"app/dev"}},
	}
	b := newMockBackend(snap)
	m := New(b, 13100, "test")

	if m.tab != tabProxies {
		t.Errorf("single proxy should start on Proxies, got %d", m.tab)
	}
	if len(m.tabLabels) != tabCount {
		t.Errorf("all tabs should always render, got %v", m.tabLabels)
	}
}

func TestModelHitTest(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.width = 100
	m.height = 30
	m.tab = tabProxies
	m.refreshRows()

	// Proxies rows: section(0), header(1), item rows at 2 and 3, blank(4),
	// section(5), header(6), items at 7 and 8.
	h := m.hitTest(2, contentTop+2)
	if h.kind != "item" || h.index != 0 {
		t.Errorf("expected first server item, got %+v", h)
	}

	h = m.hitTest(m.tabRanges[0].x0, tabBarY)
	if h.kind != "tab" || h.index != 0 {
		t.Errorf("expected first tab, got %+v", h)
	}

	h = m.hitTest(2, contentTop)
	if h.kind != "none" {
		t.Errorf("section header should not be interactive, got %+v", h)
	}

	// Groups tab: member rows hit their parent group span.
	m.setTab(tabGroups)
	h = m.hitTest(2, contentTop+2) // first member row under the dev group
	if h.kind != "group" || h.group != "dev" {
		t.Errorf("expected dev group span, got %+v", h)
	}
}

func TestModelScrollFollowsCursor(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.tab = tabProxies
	m.refreshRows()
	m.width = 100
	m.height = chromeRows + 3 // 3 visible content rows

	m.cursor = len(m.items) - 1
	m.ensureCursorVisible()
	if m.scroll[m.tab] == 0 {
		t.Error("scroll should follow the cursor beyond the window")
	}

	m.cursor = 0
	m.ensureCursorVisible()
	if m.scroll[m.tab] != 0 {
		t.Errorf("scroll should return to top, got %d", m.scroll[m.tab])
	}
}

func TestModelRefreshItemsProxies(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.tab = 1 // Proxies tab

	m.refreshRows()
	if len(m.items) != 4 {
		t.Fatalf("expected 4 server items across 2 proxies, got %d", len(m.items))
	}
	for _, item := range m.items {
		if item.Kind != "server" {
			t.Errorf("expected kind 'server', got %q", item.Kind)
		}
	}
}

func TestModelRefreshItemsGroups(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.tab = 0 // Groups tab

	m.refreshRows()
	if len(m.items) != 2 {
		t.Fatalf("expected 2 group items, got %d", len(m.items))
	}
	if m.items[0].Kind != "group" {
		t.Errorf("expected kind 'group', got %q", m.items[0].Kind)
	}
}

func TestModelFindItemIndex(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.tab = 0
	m.refreshRows()

	idx := m.findItemIndex("group", "dev", 0)
	if idx < 0 {
		t.Error("expected to find dev group")
	}

	idx = m.findItemIndex("group", "nonexistent", 0)
	if idx != -1 {
		t.Errorf("expected -1 for nonexistent, got %d", idx)
	}
}

func TestModelActivateGroup(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.tab = 0
	m.refreshRows()
	m.cursor = 0

	cmd := m.activateItem()
	if cmd == nil {
		t.Fatal("expected a command from activateItem")
	}
	if m.pending == nil {
		t.Fatal("expected a pending action")
	}
	if b.switchedGroup != "" {
		t.Error("SwitchGroup must not run synchronously in the update loop")
	}

	done, ok := findActionDone(drainCmd(cmd))
	if !ok {
		t.Fatal("expected an actionDoneMsg")
	}
	if b.switchedGroup != "dev" {
		t.Errorf("expected SwitchGroup(dev), got %q", b.switchedGroup)
	}

	next, _ := m.Update(done)
	mm := next.(Model)
	if mm.pending != nil {
		t.Error("pending should be cleared after actionDoneMsg")
	}
	if mm.status.level != statusOK || !strings.Contains(mm.status.text, "switched to dev") {
		t.Errorf("unexpected status: %+v", mm.status)
	}
}

func TestModelActivateServer(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.tab = 1
	m.refreshRows()
	m.cursor = 0

	cmd := m.activateItem()
	if cmd == nil {
		t.Fatal("expected a command from activateItem")
	}
	done, ok := findActionDone(drainCmd(cmd))
	if !ok {
		t.Fatal("expected an actionDoneMsg")
	}
	if len(b.setDefaultCalls) == 0 {
		t.Fatal("expected SetDefault to be called")
	}

	next, _ := m.Update(done)
	mm := next.(Model)
	if mm.status.level != statusOK || !strings.Contains(mm.status.text, "default set to") {
		t.Errorf("unexpected status: %+v", mm.status)
	}
}

func TestModelActivateEnterKey(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.tab = 0
	m.refreshRows()
	m.cursor = 0

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	mm := next.(Model)
	if mm.pending == nil {
		t.Fatal("enter should start a pending action")
	}
	if cmd == nil {
		t.Fatal("enter should return a command")
	}
}

func TestModelActivateIgnoredWhilePending(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.tab = 0
	m.refreshRows()
	m.cursor = 0

	first := m.activateItem()
	if first == nil {
		t.Fatal("expected first activation to produce a command")
	}
	gen := m.pending.gen
	if cmd := m.activateItem(); cmd != nil {
		t.Error("second activation while pending should be ignored")
	}
	if m.pending.gen != gen {
		t.Error("pending action should be unchanged")
	}
}

func TestModelActionError(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	b.switchErr = errors.New("switch group failed (status 503)")
	m := New(b, 13100, "test")
	m.tab = 0
	m.refreshRows()
	m.cursor = 0

	cmd := m.activateItem()
	done, ok := findActionDone(drainCmd(cmd))
	if !ok {
		t.Fatal("expected an actionDoneMsg")
	}
	next, _ := m.Update(done)
	mm := next.(Model)
	if mm.status.level != statusErr || !strings.Contains(mm.status.text, "503") {
		t.Errorf("expected error status with cause, got %+v", mm.status)
	}
	if mm.pending != nil {
		t.Error("pending should be cleared on error too")
	}
}

func TestModelStaleActionDoneIgnored(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.tab = 0
	m.refreshRows()
	m.cursor = 0

	_ = m.activateItem()
	next, _ := m.Update(actionDoneMsg{verb: "switch", target: "old", gen: m.pending.gen - 1})
	mm := next.(Model)
	if mm.pending == nil {
		t.Error("stale actionDoneMsg must not clear the live pending action")
	}
	if mm.status.level != statusNone {
		t.Error("stale actionDoneMsg must not set a status")
	}
}

func TestModelClearStatusStaleGen(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.status = status{level: statusOK, text: "switched to dev", gen: 5}

	next, _ := m.Update(clearStatusMsg{gen: 4})
	mm := next.(Model)
	if mm.status.level != statusOK {
		t.Error("stale clearStatusMsg must not wipe a newer status")
	}

	next, _ = mm.Update(clearStatusMsg{gen: 5})
	mm = next.(Model)
	if mm.status.level != statusNone {
		t.Error("matching clearStatusMsg should clear the status")
	}
}

func TestModelEventTriggersGuardedFetch(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")

	next, cmd := m.Update(EventMsg{Type: "poll"})
	mm := next.(Model)
	if !mm.fetching {
		t.Error("poll event should mark a fetch in flight")
	}
	if cmd == nil {
		t.Error("poll event should return commands")
	}

	// A second event while fetching must not clear or duplicate the guard.
	next, _ = mm.Update(EventMsg{Type: "update"})
	mm = next.(Model)
	if !mm.fetching {
		t.Error("fetch guard should remain set while a fetch is in flight")
	}
}

func TestModelSnapshotMsgRebuilds(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	if m.tabLabels[1] != "Proxies 2" {
		t.Fatalf("precondition: expected 2-proxy badge, got %v", m.tabLabels)
	}
	m.fetching = true

	single := orchestrator.Snapshot{
		Proxies: []orchestrator.ProxySnapshot{
			{Port: 3000, Servers: []registry.ServerEntry{
				{Name: "app/dev", Port: 4001, Group: "dev"},
			}},
		},
		Groups: map[string][]string{"dev": {"app/dev"}},
	}
	next, _ := m.Update(snapshotMsg{snap: single})
	mm := next.(Model)
	if mm.fetching {
		t.Error("snapshotMsg should clear the fetch guard")
	}
	if mm.tabLabels[1] != "Proxies 1" {
		t.Errorf("tab badges should update from the new snapshot, got %v", mm.tabLabels)
	}
	if len(mm.snap.Proxies) != 1 {
		t.Errorf("snapshot should be replaced, got %d proxies", len(mm.snap.Proxies))
	}
}

func TestModelViewNotEmpty(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")

	view := m.View()
	if len(view.Content) == 0 {
		t.Error("View() returned empty content")
	}
}

func TestModelViewQuitting(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100, "test")
	m.quitting = true

	view := m.View()
	if view.Content != "" {
		t.Errorf("quitting View() should return empty content, got %q", view.Content)
	}
}
