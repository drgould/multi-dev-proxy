// Package tui implements the interactive terminal UI for the mdp
// orchestrator: a full-width tabbed dashboard over the control API.
package tui

import (
	"sort"
	"strings"

	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
)

const (
	tabGroups   = 0
	tabProxies  = 1
	tabServices = 2
	tabLogs     = 3
	tabCount    = 4
)

// Item represents a selectable item in the TUI.
type Item struct {
	Kind      string // "group", "server", "service"
	Label     string
	ProxyPort int
	Name      string
	GroupName string
	PID       int // 0 for groups and externally managed processes
}

// ─── snapshot helpers ───────────────────────────────────────────────────────

type srvEntry struct {
	Name  string
	Port  int
	PID   int
	Group string
}

type groupMember struct {
	Name string
	Port int
	PID  int
}

func buildServersByGroup(snap orchestrator.Snapshot) map[string][]groupMember {
	m := make(map[string][]groupMember)
	for _, pi := range snap.Proxies {
		for _, srv := range pi.Servers {
			if srv.Group != "" {
				m[srv.Group] = append(m[srv.Group], groupMember{
					Name: srv.Name,
					Port: srv.Port,
					PID:  srv.PID,
				})
			}
		}
	}
	return m
}

func isGroupActive(snap orchestrator.Snapshot, groupName string) bool {
	for _, pi := range snap.Proxies {
		for _, srv := range pi.Servers {
			if srv.Group == groupName && srv.Name == pi.Default {
				return true
			}
		}
	}
	return false
}

func isServerDefault(snap orchestrator.Snapshot, name string) bool {
	for _, pi := range snap.Proxies {
		if pi.Default == name {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// matchesFilter reports whether any field contains the filter,
// case-insensitively. An empty filter matches everything.
func matchesFilter(filter string, fields ...string) bool {
	if filter == "" {
		return true
	}
	f := strings.ToLower(filter)
	for _, s := range fields {
		if strings.Contains(strings.ToLower(s), f) {
			return true
		}
	}
	return false
}
