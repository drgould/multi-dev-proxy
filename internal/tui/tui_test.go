package tui

import (
	"testing"

	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

type mockBackend struct {
	snap   orchestrator.Snapshot
	events chan orchestrator.Event
	switchedGroup string
	setDefaultCalls []setDefaultCall
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
func (m *mockBackend) SwitchGroup(name string) error {
	m.switchedGroup = name
	return nil
}
func (m *mockBackend) SetDefault(proxyPort int, name string) error {
	m.setDefaultCalls = append(m.setDefaultCalls, setDefaultCall{Port: proxyPort, Name: name})
	return nil
}

func testSnapshot() orchestrator.Snapshot {
	return orchestrator.Snapshot{
		Proxies: []orchestrator.ProxySnapshot{
			{
				Port:    3000,
				Label:   "frontend",
				Default: "app/dev",
				Servers: []*registry.ServerEntry{
					{Name: "app/dev", Port: 4001, PID: 100, Group: "dev"},
					{Name: "app/staging", Port: 4002, PID: 200, Group: "staging"},
				},
			},
			{
				Port:    3001,
				Label:   "backend",
				Default: "api/dev",
				Servers: []*registry.ServerEntry{
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
				Servers: []*registry.ServerEntry{
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

func TestHasManagedServices(t *testing.T) {
	empty := orchestrator.Snapshot{}
	if hasManagedServices(empty) {
		t.Error("empty snapshot should have no managed services")
	}

	withEmpty := orchestrator.Snapshot{
		Services: []orchestrator.ServiceSnapshot{{Name: "web", Status: ""}},
	}
	if hasManagedServices(withEmpty) {
		t.Error("services with empty status should not count")
	}

	withRunning := orchestrator.Snapshot{
		Services: []orchestrator.ServiceSnapshot{{Name: "web", Status: "running"}},
	}
	if !hasManagedServices(withRunning) {
		t.Error("expected to find managed services")
	}
}

func TestModelRebuildTabsMultiProxy(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100)

	if len(m.tabs) != 2 {
		t.Fatalf("expected 2 tabs (Groups, Proxies), got %d: %v", len(m.tabs), m.tabs)
	}
	if m.tabs[0] != "Groups" || m.tabs[1] != "Proxies" {
		t.Errorf("unexpected tabs: %v", m.tabs)
	}
}

func TestModelRebuildTabsSingleProxy(t *testing.T) {
	snap := orchestrator.Snapshot{
		Proxies: []orchestrator.ProxySnapshot{
			{Port: 3000, Servers: []*registry.ServerEntry{
				{Name: "app/dev", Port: 4001, Group: "dev"},
			}},
		},
		Groups: map[string][]string{"dev": {"app/dev"}},
	}
	b := newMockBackend(snap)
	m := New(b, 13100)

	if len(m.tabs) != 0 {
		t.Errorf("single proxy should have no tabs, got %v", m.tabs)
	}
	if m.activeTab() != tabProxies {
		t.Errorf("single proxy should default to proxies tab, got %d", m.activeTab())
	}
}

func TestModelRefreshItemsProxies(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100)
	m.tab = 1 // Proxies tab

	m.refreshItems()
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
	m := New(b, 13100)
	m.tab = 0 // Groups tab

	m.refreshItems()
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
	m := New(b, 13100)
	m.tab = 0
	m.refreshItems()

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
	m := New(b, 13100)
	m.tab = 0
	m.refreshItems()
	m.cursor = 0

	m.activateItem()
	if b.switchedGroup == "" {
		t.Error("expected SwitchGroup to be called")
	}
}

func TestModelActivateServer(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100)
	m.tab = 1
	m.refreshItems()
	m.cursor = 0

	m.activateItem()
	if len(b.setDefaultCalls) == 0 {
		t.Error("expected SetDefault to be called")
	}
}

func TestModelViewNotEmpty(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100)

	view := m.View()
	if len(view) == 0 {
		t.Error("View() returned empty string")
	}
}

func TestModelViewQuitting(t *testing.T) {
	snap := testSnapshot()
	b := newMockBackend(snap)
	m := New(b, 13100)
	m.quitting = true

	view := m.View()
	if view != "" {
		t.Errorf("quitting View() should return empty, got %q", view)
	}
}
