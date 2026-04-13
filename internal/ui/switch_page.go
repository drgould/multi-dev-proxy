package ui

import (
	"fmt"
	"net/http"
)

// SwitchPageHandler returns an HTTP handler for GET /__mdp/switch.
func SwitchPageHandler() http.HandlerFunc {
	page := renderSwitchPage()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, page)
	}
}

func renderSwitchPage() string {
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
    .btn { background: var(--btn-bg); color: var(--btn-text); border: 1px solid var(--btn-border); padding: 0.25rem 0.75rem; border-radius: 6px; font-size: 0.8rem; cursor: pointer; font-family: inherit; }
    .btn:hover { background: var(--btn-hover); }
    .status { display: block; margin-top: 1rem; text-align: center; font-size: 0.75rem; color: var(--refresh); }
    .theme-toggle { display: flex; gap: 0; }
    .theme-btn {
      padding: 0.2rem 0.6rem; font-size: 0.75rem; cursor: pointer;
      background: var(--btn-bg); color: var(--btn-text); border: 1px solid var(--btn-border);
      font-family: inherit;
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
    <div id="content"></div>
    <span class="status" id="status">Connecting...</span>
  </div>
  <script>
  (function() {
    // Theme
    function getThemeCookie() {
      var m = document.cookie.match(/(?:^|; )__mdp_theme=([^;]*)/);
      return m ? m[1] : '';
    }
    function setThemeCookie(v) {
      if (v) document.cookie = '__mdp_theme=' + v + '; path=/; SameSite=Lax; max-age=31536000';
      else document.cookie = '__mdp_theme=; path=/; SameSite=Lax; max-age=0';
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

    var contentEl = document.getElementById('content');
    var statusEl = document.getElementById('status');
    var cfg = null;
    var localServerNames = [];

    function esc(s) {
      var d = document.createElement('div');
      d.textContent = s;
      return d.innerHTML;
    }

    function switchServer(name) {
      fetch('/__mdp/switch/' + encodeURIComponent(name), { method: 'POST', redirect: 'follow' })
        .then(function() { window.location.href = '/'; });
    }

    function switchGroup(name) {
      var members = (cfg && cfg.groups && cfg.groups[name]) || [];
      fetch('/__mdp/groups/' + encodeURIComponent(name) + '/switch', { method: 'POST' }).then(function() {
        var cookieName = (cfg && cfg.cookieName) || '__mdp_upstream';
        var local = members.find(function(m) { return localServerNames.indexOf(m) >= 0; });
        if (local) {
          document.cookie = cookieName + '=' + encodeURIComponent(local) + '; path=/; SameSite=Lax';
        }
        window.location.href = '/';
      });
    }

    function render(servers) {
      // Build local server names list
      localServerNames = [];
      Object.keys(servers).forEach(function(repo) {
        Object.keys(servers[repo]).forEach(function(n) { localServerNames.push(n); });
      });

      var html = '';

      // Groups section
      if (cfg && cfg.groups && Object.keys(cfg.groups).length > 0 && cfg.siblings && cfg.siblings.length > 0) {
        html += '<div class="groups-section"><h2>Groups</h2>';
        Object.keys(cfg.groups).sort().forEach(function(name) {
          var members = cfg.groups[name] || [];
          html += '<div class="group-row"><span class="group-name">' + esc(name) +
            '<span class="group-members"> \u2014 ' + members.map(esc).join(', ') + '</span></span>' +
            '<button class="btn" onclick="window.__switchGroup(\'' + esc(name).replace(/'/g, "\\'") + '\')">Switch</button></div>';
        });
        html += '</div>';
      }

      // Server tables by repo
      var repos = Object.keys(servers).sort();
      if (repos.length === 0) {
        html += '<div class="empty"><p>No dev servers registered.</p><p style="margin-top:0.75rem">Run <code>mdp run &lt;command&gt;</code> to start one.</p></div>';
      }
      repos.forEach(function(repo) {
        html += '<div class="repo-group"><h2 class="repo-name">' + esc(repo) + '</h2><table>' +
          '<thead><tr><th>Branch</th><th>Port</th><th>PID</th><th></th></tr></thead><tbody>';
        Object.keys(servers[repo]).sort().forEach(function(fullName) {
          var info = servers[repo][fullName];
          var branch = fullName.indexOf('/') >= 0 ? fullName.split('/').pop() : fullName;
          html += '<tr><td>' + esc(branch) + '</td>' +
            '<td class="mono">:' + info.port + '</td>' +
            '<td class="mono">' + (info.pid || '0') + '</td>' +
            '<td><button class="btn" onclick="window.__switchServer(\'' + esc(fullName).replace(/'/g, "\\'") + '\')">Switch</button></td></tr>';
        });
        html += '</tbody></table></div>';
      });

      contentEl.innerHTML = html;
    }

    window.__switchServer = switchServer;
    window.__switchGroup = switchGroup;

    function fetchAndRender() {
      Promise.all([
        fetch('/__mdp/servers').then(function(r) { return r.json(); }),
        fetch('/__mdp/config').then(function(r) { return r.json(); })
      ]).then(function(res) {
        var servers = res[0];
        cfg = res[1];
        render(servers);
      }).catch(function() {});
    }

    // SSE for real-time updates
    if (typeof EventSource !== 'undefined') {
      var es = new EventSource('/__mdp/events');
      es.onopen = function() { statusEl.textContent = 'Live'; };
      es.onmessage = function() { fetchAndRender(); };
      es.onerror = function() { statusEl.textContent = 'Reconnecting...'; };
    }

    // Polling fallback
    setInterval(fetchAndRender, 5000);

    // Initial fetch
    fetchAndRender();
  })();
  </script>
</body>
</html>`
}
