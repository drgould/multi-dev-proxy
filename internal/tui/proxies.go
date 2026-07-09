package tui

import (
	"fmt"
	"image/color"
	"sort"

	"charm.land/lipgloss/v2"

	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
)

// buildProxiesTab produces the nav items and content rows for the Proxies
// tab. Servers match the filter by name or group; sections with no matching
// servers are hidden.
func buildProxiesTab(snap orchestrator.Snapshot, filter string) ([]Item, []row) {
	proxies := append([]orchestrator.ProxySnapshot(nil), snap.Proxies...)
	sort.Slice(proxies, func(i, j int) bool { return proxies[i].Port < proxies[j].Port })

	if len(proxies) == 0 {
		return nil, emptyStateRows("No proxies running — start services with `mdp run`.")
	}

	nameW := lipgloss.Width("SERVER")
	groupW := lipgloss.Width("GROUP")
	for _, p := range proxies {
		for _, s := range p.Servers {
			if w := lipgloss.Width(s.Name); w > nameW {
				nameW = w
			}
			if w := lipgloss.Width(s.Group); w > groupW {
				groupW = w
			}
		}
	}
	colWidths := []int{2, nameW, groupW, 6, 10}

	var items []Item
	var rows []row
	itemIdx := 0
	matched := 0
	for _, proxy := range proxies {
		proxy := proxy

		servers := make([]srvEntry, 0, len(proxy.Servers))
		for _, s := range proxy.Servers {
			if matchesFilter(filter, s.Name, s.Group) {
				servers = append(servers, srvEntry{Name: s.Name, Port: s.Port, PID: s.PID, Group: s.Group})
			}
		}
		// When filtering, hide sections with no matching servers. With no
		// filter, keep the section so a proxy that momentarily has no
		// registered servers stays visible and consistent with the tab badge
		// and header count.
		if len(servers) == 0 && filter != "" {
			continue
		}
		sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
		if matched > 0 {
			rows = append(rows, blankRow())
		}
		matched++

		label := proxy.Label
		if label == "" {
			label = "proxy"
		}
		title := fmt.Sprintf(":%d  %s", proxy.Port, label)
		if proxy.Default != "" {
			title += " — default: " + proxy.Default
		}
		rows = append(rows, textRow(func(rc rowCtx) string {
			return " " + rc.st.section.Render(title)
		}))
		rows = append(rows, textRow(func(rc rowCtx) string {
			return "   " + headerRow(rc.st, []string{"", "SERVER", "GROUP", "PORT", "PID"}, colWidths)
		}))
		if len(servers) == 0 {
			rows = append(rows, textRow(func(rc rowCtx) string {
				return "   " + rc.st.faint.Render("(no servers registered)")
			}))
		}

		for _, srv := range servers {
			srv := srv
			idx := itemIdx
			itemIdx++
			isDefault := srv.Name == proxy.Default
			items = append(items, Item{
				Kind:      "server",
				Label:     srv.Name,
				ProxyPort: proxy.Port,
				Name:      srv.Name,
				PID:       srv.PID,
			})
			rows = append(rows, row{
				kind:      rowItem,
				itemIndex: idx,
				render: func(rc rowCtx) string {
					marker := "  "
					markerStyle := rc.st.dim
					nameStyle := rc.st.fg
					if isDefault {
						marker = "● "
						markerStyle = rc.st.active
						nameStyle = rc.st.active
					}
					prefixText := "   "
					prefixStyle := lipgloss.NewStyle()
					if rc.selected {
						prefixText = " ▸ "
						prefixStyle = rc.st.selected
						nameStyle = rc.st.selected
					}
					pidStr := "(external)"
					if srv.PID > 0 {
						pidStr = fmt.Sprintf("%d", srv.PID)
					}
					var bg []color.Color
					if c, ok := rc.bg(); ok {
						bg = []color.Color{c}
						prefixStyle = prefixStyle.Background(c)
					}
					line := prefixStyle.Render(prefixText) + tableRow(
						[]string{marker, srv.Name, srv.Group, fmt.Sprintf(":%d", srv.Port), pidStr},
						colWidths,
						[]lipgloss.Style{markerStyle, nameStyle, rc.st.dim, rc.st.dim, rc.st.dim},
						bg...,
					)
					return rc.finish(line)
				},
			})
		}

	}
	if len(rows) == 0 {
		if filter != "" {
			return nil, noMatchRows(filter)
		}
		return nil, emptyStateRows("No servers registered — start services with `mdp run`.")
	}
	return items, rows
}
