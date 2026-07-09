package tui

import "charm.land/bubbles/v2/key"

// keyMap declares all TUI bindings; the footer help is rendered from
// per-tab slices of these.
type keyMap struct {
	Up           key.Binding
	Down         key.Binding
	NextTab      key.Binding
	PrevTab      key.Binding
	EnterSwitch  key.Binding // groups tab
	EnterDefault key.Binding // proxies tab
	Filter       key.Binding
	ClearFilter  key.Binding
	Stop         key.Binding
	LogFollow    key.Binding
	LogPrev      key.Binding
	LogNext      key.Binding
	Detach       key.Binding
	Quit         key.Binding
	Tab1         key.Binding
	Tab2         key.Binding
	Tab3         key.Binding
	Tab4         key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:           key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑↓", "navigate")),
		Down:         key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↑↓", "navigate")),
		NextTab:      key.NewBinding(key.WithKeys("tab", "right"), key.WithHelp("⇥", "tab")),
		PrevTab:      key.NewBinding(key.WithKeys("shift+tab", "left"), key.WithHelp("⇤", "tab")),
		EnterSwitch:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "switch")),
		EnterDefault: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "set default")),
		Filter:       key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		ClearFilter:  key.NewBinding(key.WithKeys("esc")),
		Stop:         key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "stop")),
		LogFollow:    key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "follow")),
		LogPrev:      key.NewBinding(key.WithKeys("[")),
		LogNext:      key.NewBinding(key.WithKeys("]"), key.WithHelp("[ ]", "source")),
		Detach:       key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "detach")),
		Quit:         key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Tab1:         key.NewBinding(key.WithKeys("1")),
		Tab2:         key.NewBinding(key.WithKeys("2")),
		Tab3:         key.NewBinding(key.WithKeys("3")),
		Tab4:         key.NewBinding(key.WithKeys("4")),
	}
}

// forTab returns the bindings shown in the footer help for the given tab.
func (k keyMap) forTab(tab int) []key.Binding {
	switch tab {
	case tabGroups:
		return []key.Binding{k.Up, k.EnterSwitch, k.Filter, k.NextTab, k.Detach, k.Quit}
	case tabProxies:
		return []key.Binding{k.Up, k.EnterDefault, k.Stop, k.Filter, k.NextTab, k.Detach, k.Quit}
	case tabLogs:
		return []key.Binding{k.Up, k.LogFollow, k.LogNext, k.Filter, k.NextTab, k.Detach, k.Quit}
	default:
		return []key.Binding{k.Up, k.Stop, k.Filter, k.NextTab, k.Detach, k.Quit}
	}
}
