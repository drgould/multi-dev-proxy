package tui

import (
	"fmt"
	"image/color"
	"sort"

	"charm.land/lipgloss/v2"

	"github.com/derekgould/multi-dev-proxy/internal/orchestrator"
)

// svcRow is one Services-tab entry: registry-derived, with orchestrator-
// managed service info merged in when present.
type svcRow struct {
	Name      string
	Group     string
	Status    string
	ProxyPort int
	Port      int
	PID       int
}

// collectServices derives the service list from registered servers (liveness
// is implied — the pruner removes dead entries), then merges in managed
// services reported by the orchestrator, which carry a real status.
func collectServices(snap orchestrator.Snapshot) []svcRow {
	var out []svcRow
	// name -> row indices: a name can be registered on more than one proxy, so
	// keep every registration (don't collapse twins) and patch them all when a
	// managed status arrives for that name.
	index := make(map[string][]int)
	for _, pi := range snap.Proxies {
		for _, srv := range pi.Servers {
			status := "running"
			if srv.PID == 0 {
				status = "external"
			}
			index[srv.Name] = append(index[srv.Name], len(out))
			out = append(out, svcRow{
				Name:      srv.Name,
				Group:     srv.Group,
				Status:    status,
				ProxyPort: pi.Port,
				Port:      srv.Port,
				PID:       srv.PID,
			})
		}
	}
	for _, svc := range snap.Services {
		if svc.Status == "" {
			continue
		}
		if idxs, ok := index[svc.Name]; ok {
			for _, i := range idxs {
				out[i].Status = svc.Status
				if out[i].PID == 0 {
					out[i].PID = svc.PID
				}
			}
			continue
		}
		out = append(out, svcRow{
			Name:   svc.Name,
			Group:  svc.Group,
			Status: svc.Status,
			Port:   svc.Port,
			PID:    svc.PID,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// buildServicesTab produces the nav items and content rows for the Services
// tab. Rows are selectable (for row actions) but enter has no effect here.
func buildServicesTab(snap orchestrator.Snapshot, filter string) ([]Item, []row) {
	all := collectServices(snap)
	svcs := make([]svcRow, 0, len(all))
	for _, s := range all {
		if matchesFilter(filter, s.Name, s.Group, s.Status) {
			svcs = append(svcs, s)
		}
	}
	if len(svcs) == 0 {
		if filter != "" {
			return nil, noMatchRows(filter)
		}
		return nil, emptyStateRows("No services registered — start services with `mdp run`.")
	}

	nameW := lipgloss.Width("SERVICE")
	groupW := lipgloss.Width("GROUP")
	for _, s := range svcs {
		if w := lipgloss.Width(s.Name); w > nameW {
			nameW = w
		}
		if w := lipgloss.Width(s.Group); w > groupW {
			groupW = w
		}
	}
	colWidths := []int{nameW, groupW, 7, 6, 10, 10}

	items := make([]Item, 0, len(svcs))
	rows := make([]row, 0, len(svcs)+1)
	rows = append(rows, textRow(func(rc rowCtx) string {
		return "   " + headerRow(rc.st, []string{"SERVICE", "GROUP", "PROXY", "PORT", "PID", "STATUS"}, colWidths)
	}))
	for i, svc := range svcs {
		svc := svc
		idx := i
		items = append(items, Item{
			Kind:      "service",
			Label:     svc.Name,
			ProxyPort: svc.ProxyPort,
			Name:      svc.Name,
			PID:       svc.PID,
		})
		rows = append(rows, row{
			kind:      rowItem,
			itemIndex: idx,
			render: func(rc rowCtx) string {
				statusStyle := rc.st.statusWarn
				switch svc.Status {
				case "running":
					statusStyle = rc.st.statusOK
				case "failed", "stopped":
					statusStyle = rc.st.statusErr
				case "external":
					statusStyle = rc.st.dim
				}
				nameStyle := rc.st.fg
				prefixText := "   "
				prefixStyle := lipgloss.NewStyle()
				if rc.selected {
					prefixText = " ▸ "
					prefixStyle = rc.st.selected
					nameStyle = rc.st.selected
				}
				proxyStr := "—"
				if svc.ProxyPort > 0 {
					proxyStr = fmt.Sprintf(":%d", svc.ProxyPort)
				}
				portStr := "—"
				if svc.Port > 0 {
					portStr = fmt.Sprintf(":%d", svc.Port)
				}
				pidStr := "(external)"
				if svc.PID > 0 {
					pidStr = fmt.Sprintf("%d", svc.PID)
				}
				var bg []color.Color
				if c, ok := rc.bg(); ok {
					bg = []color.Color{c}
					prefixStyle = prefixStyle.Background(c)
				}
				line := prefixStyle.Render(prefixText) + tableRow(
					[]string{svc.Name, svc.Group, proxyStr, portStr, pidStr, svc.Status},
					colWidths,
					[]lipgloss.Style{nameStyle, rc.st.dim, rc.st.dim, rc.st.dim, rc.st.dim, statusStyle},
					bg...,
				)
				return rc.finish(line)
			},
		})
	}
	return items, rows
}
