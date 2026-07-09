package tui

import (
	"fmt"
	"image/color"
	"sort"

	"charm.land/lipgloss/v2"

	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
)

// buildGroupsTab produces the nav items and content rows for the Groups tab.
// A group matches the filter by its own name or any member's name.
func buildGroupsTab(snap orchestrator.Snapshot, filter string) ([]Item, []row) {
	serversByGroup := buildServersByGroup(snap)

	var groupNames []string
	for _, name := range sortedKeys(snap.Groups) {
		fields := []string{name}
		for _, srv := range serversByGroup[name] {
			fields = append(fields, srv.Name)
		}
		if matchesFilter(filter, fields...) {
			groupNames = append(groupNames, name)
		}
	}

	items := make([]Item, 0, len(groupNames))
	for _, name := range groupNames {
		items = append(items, Item{Kind: "group", Label: name, GroupName: name})
	}

	if len(groupNames) == 0 {
		if filter != "" {
			return items, noMatchRows(filter)
		}
		return items, emptyStateRows("No groups found. Groups are derived from registered services.")
	}

	nameW := lipgloss.Width("GROUP")
	for _, n := range groupNames {
		if w := lipgloss.Width(n); w > nameW {
			nameW = w
		}
	}
	colWidths := []int{2, nameW, 8}

	rows := make([]row, 0, len(groupNames)*3+1)
	rows = append(rows, textRow(func(rc rowCtx) string {
		return "   " + headerRow(rc.st, []string{"", "GROUP", "STATUS"}, colWidths)
	}))

	for gi, name := range groupNames {
		name := name
		itemIdx := gi
		isActive := isGroupActive(snap, name)

		rows = append(rows, row{
			kind:      rowItem,
			itemIndex: itemIdx,
			group:     name,
			render: func(rc rowCtx) string {
				marker := "  "
				markerStyle := rc.st.dim
				nameStyle := rc.st.fg
				statusText := ""
				if isActive {
					marker = "● "
					markerStyle = rc.st.active
					nameStyle = rc.st.active
					statusText = "active"
				}
				prefixText := "   "
				prefixStyle := lipgloss.NewStyle()
				if rc.selected {
					prefixText = " ▸ "
					prefixStyle = rc.st.selected
					nameStyle = rc.st.selected
				}
				var bg []color.Color
				if c, ok := rc.bg(); ok {
					bg = []color.Color{c}
					prefixStyle = prefixStyle.Background(c)
				}
				line := prefixStyle.Render(prefixText) + tableRow(
					[]string{marker, name, statusText},
					colWidths,
					[]lipgloss.Style{markerStyle, nameStyle, rc.st.active},
					bg...,
				)
				return rc.finish(line)
			},
		})

		members := append([]groupMember(nil), serversByGroup[name]...)
		sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
		for _, srv := range members {
			srv := srv
			isDefault := isServerDefault(snap, srv.Name)
			rows = append(rows, row{
				kind:  rowMember,
				group: name,
				render: func(rc rowCtx) string {
					bulletStyle := rc.st.dim
					nameStyle := rc.st.dim
					detailStyle := rc.st.dim
					if isDefault {
						bulletStyle = rc.st.active
						nameStyle = rc.st.active
					}
					suffix := ""
					if isDefault {
						suffix = "  ● default"
					}
					pid := ""
					if srv.PID > 0 {
						pid = fmt.Sprintf("  PID %d", srv.PID)
					}
					apply := func(s lipgloss.Style) lipgloss.Style {
						if c, ok := rc.bg(); ok {
							return s.Background(c)
						}
						return s
					}
					line := apply(lipgloss.NewStyle()).Render("      ") +
						apply(bulletStyle).Render("•") +
						apply(lipgloss.NewStyle()).Render(" ") +
						apply(nameStyle).Render(fmt.Sprintf("%-*s", nameW, srv.Name)) +
						apply(detailStyle).Render(fmt.Sprintf("  :%d%s", srv.Port, pid)) +
						apply(rc.st.active).Render(suffix)
					return rc.finish(line)
				},
			})
		}

		if gi < len(groupNames)-1 {
			rows = append(rows, blankRow())
		}
	}
	return items, rows
}
