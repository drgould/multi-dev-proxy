package ui

import (
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"

	"github.com/derekgould/multi-dev-proxy/internal/registry"
)

// RenderSwitchPage renders the server switcher HTML page.
// activeServer is the name of the currently active server (from cookie), or "".
func RenderSwitchPage(servers []*registry.ServerEntry) string {
	if len(servers) == 0 {
		return renderEmpty()
	}

	groups := make(map[string][]*registry.ServerEntry)
	for _, e := range servers {
		groups[e.Repo] = append(groups[e.Repo], e)
	}

	repos := make([]string, 0, len(groups))
	for r := range groups {
		repos = append(repos, r)
	}
	sort.Strings(repos)

	var sb strings.Builder
	for _, repo := range repos {
		sb.WriteString(renderRepoGroup(repo, groups[repo]))
	}

	return renderPage(sb.String(), false)
}

func renderRepoGroup(repo string, entries []*registry.ServerEntry) string {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<div class="repo-group"><h2 class="repo-name">%s</h2><table>`, html.EscapeString(repo)))
	sb.WriteString(`<thead><tr><th>Branch</th><th>Port</th><th>PID</th><th></th></tr></thead><tbody>`)
	for _, e := range entries {
		branch := e.Name
		if idx := strings.LastIndex(e.Name, "/"); idx >= 0 {
			branch = e.Name[idx+1:]
		}
		btn := fmt.Sprintf(`<form method="POST" action="/__mdp/switch/%s"><button type="submit" class="btn">Switch</button></form>`, html.EscapeString(e.Name))
		sb.WriteString(fmt.Sprintf(`<tr><td>%s</td><td class="mono">:%d</td><td class="mono">%d</td><td>%s</td></tr>`,
			html.EscapeString(branch), e.Port, e.PID, btn))
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
    body {
      --bg: #0a0a0a; --text: #e5e5e5; --heading: #fff; --muted: #737373;
      --border: #262626; --border-light: #1a1a1a;
      --btn-bg: #262626; --btn-text: #e5e5e5; --btn-border: #404040; --btn-hover: #333;
      --mono: #a3a3a3; --empty-bg: #1a1a1a; --refresh: #525252;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      background: var(--bg); color: var(--text); min-height: 100vh; padding: 2rem;
    }
    @media (prefers-color-scheme: light) {
      body:not(.dark) {
        --bg: #fafafa; --text: #1a1a1a; --heading: #111; --muted: #6b7280;
        --border: #e5e7eb; --border-light: #f3f4f6;
        --btn-bg: #f3f4f6; --btn-text: #1a1a1a; --btn-border: #d1d5db; --btn-hover: #e5e7eb;
        --mono: #6b7280; --empty-bg: #f3f4f6; --refresh: #9ca3af;
      }
    }
    body.light {
      --bg: #fafafa; --text: #1a1a1a; --heading: #111; --muted: #6b7280;
      --border: #e5e7eb; --border-light: #f3f4f6;
      --btn-bg: #f3f4f6; --btn-text: #1a1a1a; --btn-border: #d1d5db; --btn-hover: #e5e7eb;
      --mono: #6b7280; --empty-bg: #f3f4f6; --refresh: #9ca3af;
    }
    .container { width: 100%; max-width: 700px; margin: 0 auto; }
    .header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 1.5rem; }
    h1 { font-size: 1.25rem; font-weight: 600; color: var(--heading); }
    .repo-group { margin-bottom: 2rem; }
    .repo-name { font-size: 0.9rem; font-weight: 500; color: var(--muted); text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: 0.5rem; }
    .empty { text-align: center; padding: 3rem 1rem; color: var(--muted); font-size: 0.9rem; }
    .empty code { background: var(--empty-bg); padding: 0.15rem 0.4rem; border-radius: 4px; font-size: 0.85rem; }
    table { width: 100%; border-collapse: collapse; }
    th { text-align: left; font-size: 0.75rem; font-weight: 500; color: var(--muted); text-transform: uppercase; letter-spacing: 0.05em; padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--border); }
    td { padding: 0.65rem 0.75rem; border-bottom: 1px solid var(--border-light); }
    .mono { font-family: "SF Mono", Menlo, monospace; font-size: 0.85rem; color: var(--mono); }
    .btn { background: var(--btn-bg); color: var(--btn-text); border: 1px solid var(--btn-border); padding: 0.25rem 0.75rem; border-radius: 6px; font-size: 0.8rem; cursor: pointer; }
    .btn:hover { background: var(--btn-hover); }
    .refresh { display: block; margin-top: 1rem; text-align: center; font-size: 0.75rem; color: var(--refresh); }
    .theme-toggle { display: flex; gap: 0; }
    .theme-btn {
      padding: 0.2rem 0.6rem; font-size: 0.75rem; cursor: pointer;
      background: var(--btn-bg); color: var(--btn-text); border: 1px solid var(--btn-border);
    }
    .theme-btn:first-child { border-radius: 4px 0 0 4px; }
    .theme-btn:last-child { border-radius: 0 4px 4px 0; }
    .theme-btn:not(:first-child) { border-left: none; }
    .theme-btn.active { background: #3b82f6; color: #fff; border-color: #3b82f6; }
    .groups-section { margin-bottom: 2rem; }
    .groups-section h2 { font-size: 0.9rem; font-weight: 500; color: var(--muted); text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: 0.5rem; }
    .group-row { display: flex; align-items: center; justify-content: space-between; padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--border-light); }
    .group-name { font-size: 0.9rem; }
    .group-members { font-size: 0.75rem; color: var(--muted); }
    .siblings-section { margin-bottom: 2rem; }
    .siblings-section h2 { font-size: 0.9rem; font-weight: 500; color: var(--muted); text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: 0.5rem; }
    .sibling-row { padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--border-light); }
    .sibling-link { color: #3b82f6; text-decoration: none; font-size: 0.9rem; }
    .sibling-link:hover { text-decoration: underline; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>Dev Server Switcher</h1>
      <div class="theme-toggle">
        <button id="theme-auto" class="theme-btn">Auto</button>
        <button id="theme-light" class="theme-btn">Light</button>
        <button id="theme-dark" class="theme-btn">Dark</button>
      </div>
    </div>
    ` + content + `
    <span class="refresh">Auto-refreshes every 3s</span>
  </div>
  <script>
  (function() {
    function getThemeCookie() {
      var m = document.cookie.match(/(?:^|; )__mdp_theme=([^;]*)/);
      return m ? m[1] : '';
    }
    function setThemeCookie(v) {
      if (v) {
        document.cookie = '__mdp_theme=' + v + '; path=/; SameSite=Lax; max-age=31536000';
      } else {
        document.cookie = '__mdp_theme=; path=/; SameSite=Lax; max-age=0';
      }
    }
    function applyTheme() {
      var pref = getThemeCookie();
      document.body.classList.remove('light', 'dark');
      if (pref) document.body.classList.add(pref);
      ['auto','light','dark'].forEach(function(t) {
        var btn = document.getElementById('theme-' + t);
        if (btn) btn.classList.toggle('active', pref === '' ? t === 'auto' : t === pref);
      });
    }
    document.getElementById('theme-auto').onclick = function() { setThemeCookie(''); applyTheme(); };
    document.getElementById('theme-light').onclick = function() { setThemeCookie('light'); applyTheme(); };
    document.getElementById('theme-dark').onclick = function() { setThemeCookie('dark'); applyTheme(); };
    applyTheme();
    setTimeout(function() { location.reload(); }, 3000);

    var localServerNames = [];
    fetch('/__mdp/servers').then(function(r) { return r.json(); }).then(function(data) {
      Object.keys(data).forEach(function(repo) {
        Object.keys(data[repo]).forEach(function(n) { localServerNames.push(n); });
      });
    }).catch(function() {});

    fetch('/__mdp/config').then(function(r) { return r.json(); }).then(function(cfg) {
      var container = document.querySelector('.container');
      var header = document.querySelector('.header');
      var cookieName = cfg.cookieName || '__mdp_upstream';

      function switchGroup(name) {
        var members = cfg.groups[name] || [];
        fetch('/__mdp/groups/' + name + '/switch', {method:'POST'}).then(function() {
          var local = members.find(function(m) { return localServerNames.indexOf(m) >= 0; });
          if (local) {
            document.cookie = cookieName + '=' + encodeURIComponent(local) + '; path=/; SameSite=Lax';
          }
          window.location.href = '/';
        });
      }

      if (cfg.groups && Object.keys(cfg.groups).length > 0 && cfg.siblings && cfg.siblings.length > 0) {
        var sec = document.createElement('div');
        sec.className = 'groups-section';
        sec.innerHTML = '<h2>Groups</h2>';
        Object.keys(cfg.groups).sort().forEach(function(name) {
          var row = document.createElement('div');
          row.className = 'group-row';
          var members = cfg.groups[name] || [];
          row.innerHTML = '<span class="group-name">' + name + '<span class="group-members"> — ' + members.join(', ') + '</span></span>';
          var btn = document.createElement('button');
          btn.className = 'btn';
          btn.textContent = 'Switch';
          btn.onclick = function() { switchGroup(name); };
          row.appendChild(btn);
          sec.appendChild(row);
        });
        header.after(sec);
      }

      if (cfg.siblings && cfg.siblings.length > 0) {
        var proto = location.protocol;
        cfg.siblings.forEach(function(sib) {
          var sibBase = proto + '//localhost:' + sib.port;
          fetch(sibBase + '/__mdp/servers').then(function(r) { return r.json(); }).then(function(data) {
            var repos = Object.keys(data).sort();
            if (repos.length === 0) return;
            var sibSec = document.createElement('div');
            sibSec.className = 'repo-group';
            var label = sib.label || 'proxy';
            sibSec.innerHTML = '<h2 class="repo-name">' + label + ' :' + sib.port + '</h2>';
            repos.forEach(function(repo) {
              var tbl = document.createElement('table');
              tbl.innerHTML = '<thead><tr><th>Branch</th><th>Port</th><th>PID</th><th></th></tr></thead>';
              var tbody = document.createElement('tbody');
              Object.keys(data[repo]).sort().forEach(function(fullName) {
                var info = data[repo][fullName];
                var branch = fullName.indexOf('/') >= 0 ? fullName.split('/').pop() : fullName;
                var tr = document.createElement('tr');
                var form = document.createElement('form');
                form.method = 'POST';
                form.action = sibBase + '/__mdp/switch/' + encodeURIComponent(fullName);
                var formBtn = document.createElement('button');
                formBtn.type = 'submit';
                formBtn.className = 'btn';
                formBtn.textContent = 'Switch';
                form.appendChild(formBtn);
                var tdName = document.createElement('td'); tdName.textContent = branch;
                var tdPort = document.createElement('td'); tdPort.className = 'mono'; tdPort.textContent = ':' + info.port;
                var tdPid = document.createElement('td'); tdPid.className = 'mono'; tdPid.textContent = info.pid || '0';
                var tdBtn = document.createElement('td'); tdBtn.appendChild(form);
                tr.appendChild(tdName); tr.appendChild(tdPort); tr.appendChild(tdPid); tr.appendChild(tdBtn);
                tbody.appendChild(tr);
              });
              tbl.appendChild(tbody);
              sibSec.appendChild(tbl);
            });
            container.insertBefore(sibSec, container.querySelector('.refresh'));
          }).catch(function() {});
        });
      }
    }).catch(function() {});
  })();
  </script>
</body>
</html>`
}

// SwitchPageHandler returns an HTTP handler for GET /__mdp/switch.
func SwitchPageHandler(reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries := reg.List()
		page := RenderSwitchPage(entries)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, page)
	}
}
