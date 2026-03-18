package ui

import (
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"

	"github.com/derekgould/multi-dev-proxy/internal/registry"
	"github.com/derekgould/multi-dev-proxy/internal/routing"
)

// RenderSwitchPage renders the server switcher HTML page.
// activeServer is the name of the currently active server (from cookie), or "".
func RenderSwitchPage(servers []*registry.ServerEntry, activeServer string) string {
	if len(servers) == 0 {
		return renderEmpty()
	}

	groups := make(map[string][]*registry.ServerEntry)
	for _, e := range servers {
		groups[e.Repo] = append(groups[e.Repo], e)
	}

	// Sort repo names for deterministic output
	repos := make([]string, 0, len(groups))
	for r := range groups {
		repos = append(repos, r)
	}
	sort.Strings(repos)

	var sb strings.Builder
	for _, repo := range repos {
		sb.WriteString(renderRepoGroup(repo, groups[repo], activeServer))
	}

	return renderPage(sb.String(), false)
}

func renderRepoGroup(repo string, entries []*registry.ServerEntry, activeServer string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<div class="repo-group"><h2 class="repo-name">%s</h2><table>`, html.EscapeString(repo)))
	sb.WriteString(`<thead><tr><th>Branch</th><th>Port</th><th>PID</th><th></th></tr></thead><tbody>`)
	for _, e := range entries {
		isActive := e.Name == activeServer
		dot := `<span class="dot dot-inactive"></span>`
		if isActive {
			dot = `<span class="dot dot-active"></span>`
		}
		badge := fmt.Sprintf(`<form method="POST" action="/__mdp/switch/%s"><button type="submit" class="btn">Switch</button></form>`, html.EscapeString(e.Name))
		if isActive {
			badge = `<span class="badge badge-active">Active</span>`
		}
		rowClass := ""
		if isActive {
			rowClass = ` class="active"`
		}
		branch := e.Name
		if idx := strings.LastIndex(e.Name, "/"); idx >= 0 {
			branch = e.Name[idx+1:]
		}
		sb.WriteString(fmt.Sprintf(`<tr%s><td>%s%s</td><td class="mono">:%d</td><td class="mono">%d</td><td>%s</td></tr>`,
			rowClass, dot, html.EscapeString(branch), e.Port, e.PID, badge))
	}
	sb.WriteString(`</tbody></table></div>`)
	return sb.String()
}

func renderEmpty() string {
	return renderPage(`<div class="empty"><p>No dev servers registered.</p><p style="margin-top:0.75rem">Run <code>mdp run &lt;command&gt;</code> to start one.</p></div>`, true)
}

func renderPage(content string, isEmpty bool) string {
	_ = isEmpty
	return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Dev Server Switcher — mdp</title>
  <style>
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #0a0a0a; color: #e5e5e5; min-height: 100vh; padding: 2rem; }
    .container { width: 100%; max-width: 700px; margin: 0 auto; }
    h1 { font-size: 1.25rem; font-weight: 600; margin-bottom: 1.5rem; color: #fff; }
    .repo-group { margin-bottom: 2rem; }
    .repo-name { font-size: 0.9rem; font-weight: 500; color: #737373; text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: 0.5rem; }
    .empty { text-align: center; padding: 3rem 1rem; color: #737373; font-size: 0.9rem; }
    .empty code { background: #1a1a1a; padding: 0.15rem 0.4rem; border-radius: 4px; font-size: 0.85rem; }
    table { width: 100%; border-collapse: collapse; }
    th { text-align: left; font-size: 0.75rem; font-weight: 500; color: #737373; text-transform: uppercase; letter-spacing: 0.05em; padding: 0.5rem 0.75rem; border-bottom: 1px solid #262626; }
    td { padding: 0.65rem 0.75rem; border-bottom: 1px solid #1a1a1a; }
    tr.active td { background: #0a1a0a; }
    .mono { font-family: "SF Mono", Menlo, monospace; font-size: 0.85rem; color: #a3a3a3; }
    .dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 0.5rem; vertical-align: middle; }
    .dot-active { background: #22c55e; box-shadow: 0 0 6px #22c55e80; }
    .dot-inactive { background: #404040; }
    .badge { display: inline-block; padding: 0.15rem 0.5rem; border-radius: 9999px; font-size: 0.75rem; font-weight: 500; }
    .badge-active { background: #14532d; color: #4ade80; }
    .btn { background: #262626; color: #e5e5e5; border: 1px solid #404040; padding: 0.25rem 0.75rem; border-radius: 6px; font-size: 0.8rem; cursor: pointer; }
    .btn:hover { background: #333; }
    .refresh { display: block; margin-top: 1rem; text-align: center; font-size: 0.75rem; color: #525252; }
  </style>
</head>
<body>
  <div class="container">
    <h1>Dev Server Switcher</h1>
    ` + content + `
    <span class="refresh">Auto-refreshes every 3s</span>
  </div>
  <script>setTimeout(() => location.reload(), 3000)</script>
</body>
</html>`
}

// SwitchPageHandler returns an HTTP handler for GET /__mdp/switch.
func SwitchPageHandler(reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookies := routing.ParseCookies(r.Header.Get("Cookie"))
		active := cookies[routing.CookieName]
		entries := reg.List()
		html := RenderSwitchPage(entries, active)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, html)
	}
}
