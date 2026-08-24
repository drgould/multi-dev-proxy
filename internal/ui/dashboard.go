package ui

import (
	"fmt"
	"net/http"
)

// DashboardHandler returns an HTTP handler that serves the dashboard HTML.
// controlPort is the orchestrator control API port used by JS to fetch state.
func DashboardHandler(controlPort int) http.HandlerFunc {
	page := renderDashboard(controlPort)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, page)
	}
}

func renderDashboard(controlPort int) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>mdp Dashboard</title>
  <style>
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      --bg: #0a0a0a; --text: #e5e5e5; --heading: #fff; --muted: #737373;
      --border: #262626; --border-light: #1a1a1a;
      --btn-bg: #262626; --btn-text: #e5e5e5; --btn-border: #404040; --btn-hover: #333;
      --mono: #a3a3a3; --empty-bg: #1a1a1a; --refresh: #525252;
      --active: #22c55e; --tab-active-bg: #1a1a1a; --tab-hover: #1a1a1a;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      background: var(--bg); color: var(--text); min-height: 100vh;
    }
    @media (prefers-color-scheme: light) {
      body:not(.dark) {
        --bg: #fafafa; --text: #1a1a1a; --heading: #111; --muted: #6b7280;
        --border: #e5e7eb; --border-light: #f3f4f6;
        --btn-bg: #f3f4f6; --btn-text: #1a1a1a; --btn-border: #d1d5db; --btn-hover: #e5e7eb;
        --mono: #6b7280; --empty-bg: #f3f4f6; --refresh: #9ca3af;
        --active: #16a34a; --tab-active-bg: #fff; --tab-hover: #f3f4f6;
      }
    }
    body.light {
      --bg: #fafafa; --text: #1a1a1a; --heading: #111; --muted: #6b7280;
      --border: #e5e7eb; --border-light: #f3f4f6;
      --btn-bg: #f3f4f6; --btn-text: #1a1a1a; --btn-border: #d1d5db; --btn-hover: #e5e7eb;
      --mono: #6b7280; --empty-bg: #f3f4f6; --refresh: #9ca3af;
      --active: #16a34a; --tab-active-bg: #fff; --tab-hover: #f3f4f6;
    }
    .topbar {
      display: flex; align-items: center; justify-content: space-between;
      padding: 0.75rem 1.5rem; border-bottom: 1px solid var(--border);
    }
    .topbar h1 { font-size: 1rem; font-weight: 600; color: var(--heading); }
    .topbar-right { display: flex; align-items: center; gap: 0.75rem; }
    .status-dot { width: 8px; height: 8px; border-radius: 50%%; background: var(--muted); display: inline-block; }
    .status-dot.ok { background: var(--active); }
    .tabs {
      display: flex; gap: 0; border-bottom: 1px solid var(--border);
      padding: 0 1.5rem;
    }
    .tab {
      padding: 0.6rem 1rem; font-size: 0.85rem; cursor: pointer;
      color: var(--muted); border-bottom: 2px solid transparent;
      background: none; border-top: none; border-left: none; border-right: none;
      font-family: inherit;
    }
    .tab:hover { color: var(--text); background: var(--tab-hover); }
    .tab.active { color: var(--heading); border-bottom-color: var(--active); }
    .content { padding: 1.5rem; max-width: 900px; margin: 0 auto; }
    .empty { text-align: center; padding: 3rem 1rem; color: var(--muted); font-size: 0.9rem; }
    .section { margin-bottom: 1.5rem; }
    .section-header {
      font-size: 0.8rem; font-weight: 500; color: var(--muted); text-transform: uppercase;
      letter-spacing: 0.05em; margin-bottom: 0.5rem; display: flex; align-items: center; gap: 0.5rem;
    }
    table { width: 100%%; border-collapse: collapse; }
    th { text-align: left; font-size: 0.75rem; font-weight: 500; color: var(--muted); text-transform: uppercase; letter-spacing: 0.05em; padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--border); }
    td { padding: 0.55rem 0.75rem; border-bottom: 1px solid var(--border-light); font-size: 0.9rem; }
    .mono { font-family: "SF Mono", Menlo, monospace; font-size: 0.8rem; color: var(--mono); }
    .indicator { color: var(--active); font-weight: bold; margin-right: 0.25rem; }
    .indicator.inactive { visibility: hidden; }
    .btn {
      background: var(--btn-bg); color: var(--btn-text); border: 1px solid var(--btn-border);
      padding: 0.2rem 0.6rem; border-radius: 5px; font-size: 0.75rem; cursor: pointer; font-family: inherit;
    }
    .btn:hover { background: var(--btn-hover); }
    .btn-group { display: flex; gap: 0.25rem; }
    .group-row {
      display: flex; align-items: center; justify-content: space-between;
      padding: 0.6rem 0.75rem; border-bottom: 1px solid var(--border-light);
    }
    .group-info { display: flex; flex-direction: column; gap: 0.15rem; }
    .group-name { font-size: 0.9rem; display: flex; align-items: center; gap: 0.35rem; }
    .group-members { font-size: 0.75rem; color: var(--muted); }
    .status-badge {
      display: inline-block; font-size: 0.7rem; padding: 0.1rem 0.4rem;
      border-radius: 3px; font-weight: 500;
    }
    .status-running { background: #16a34a22; color: #22c55e; }
    .status-stopped { background: #52525222; color: var(--muted); }
    .status-failed { background: #ef444422; color: #ef4444; }
    .status-starting { background: #eab30822; color: #eab308; }
    .theme-toggle { display: flex; gap: 0; }
    .theme-btn {
      padding: 0.15rem 0.5rem; font-size: 0.7rem; cursor: pointer;
      background: var(--btn-bg); color: var(--btn-text); border: 1px solid var(--btn-border);
      font-family: inherit;
    }
    .theme-btn:first-child { border-radius: 3px 0 0 3px; }
    .theme-btn:last-child { border-radius: 0 3px 3px 0; }
    .theme-btn:not(:first-child) { border-left: none; }
    .theme-btn.active { background: #3b82f6; color: #fff; border-color: #3b82f6; }
  </style>
</head>
<body>
  <div class="topbar">
    <h1>mdp</h1>
    <div class="topbar-right">
      <span class="status-dot" id="status-dot"></span>
      <div class="theme-toggle">
        <button id="theme-auto" class="theme-btn">Auto</button>
        <button id="theme-light" class="theme-btn">Light</button>
        <button id="theme-dark" class="theme-btn">Dark</button>
      </div>
    </div>
  </div>
  <div class="tabs">
    <button class="tab active" data-tab="groups">Groups</button>
    <button class="tab" data-tab="proxies">Proxies</button>
    <button class="tab" data-tab="services">Services</button>
  </div>
  <div id="content" class="content"></div>

  <script>
  (function() {
    var API = 'http://localhost:%d';
    var activeTab = location.hash.slice(1) || 'groups';
    var state = { proxies: [], groups: {}, services: [] };

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

    // Tabs
    var tabs = document.querySelectorAll('.tab');
    tabs.forEach(function(tab) {
      tab.onclick = function() {
        activeTab = tab.getAttribute('data-tab');
        location.hash = activeTab;
        tabs.forEach(function(t) { t.classList.toggle('active', t === tab); });
        render();
      };
    });
    // Set initial active tab from hash
    tabs.forEach(function(t) { t.classList.toggle('active', t.getAttribute('data-tab') === activeTab); });

    // API helpers
    function api(path, opts) {
      return fetch(API + path, opts || {}).then(function(r) { return r.json(); });
    }

    function fetchState() {
      return Promise.all([
        api('/__mdp/proxies'),
        api('/__mdp/groups'),
        api('/__mdp/services')
      ]).then(function(res) {
        state.proxies = res[0];
        state.groups = res[1];
        state.services = res[2];
        document.getElementById('status-dot').classList.add('ok');
        render();
      }).catch(function() {
        document.getElementById('status-dot').classList.remove('ok');
      });
    }

    function switchGroup(name) {
      api('/__mdp/groups/' + encodeURIComponent(name) + '/switch', { method: 'POST' })
        .then(fetchState);
    }

    function setDefault(port, name) {
      api('/__mdp/proxies/' + port + '/default/' + encodeURIComponent(name), { method: 'POST' })
        .then(fetchState);
    }

    function clearDefault(port) {
      api('/__mdp/proxies/' + port + '/default', { method: 'DELETE' })
        .then(fetchState);
    }

    // Rendering
    var content = document.getElementById('content');

    function esc(s) {
      var d = document.createElement('div');
      d.textContent = s;
      return d.innerHTML;
    }

    function render() {
      switch (activeTab) {
        case 'groups': renderGroups(); break;
        case 'proxies': renderProxies(); break;
        case 'services': renderServices(); break;
      }
    }

    function isGroupActive(name) {
      var members = state.groups[name] || [];
      if (members.length === 0) return false;
      var matched = 0;
      state.proxies.forEach(function(p) {
        p.servers.forEach(function(s) {
          if (s.group === name && p.default === s.name) matched++;
        });
      });
      return matched > 0 && matched === countProxiesWithGroup(name);
    }

    function countProxiesWithGroup(name) {
      var count = 0;
      state.proxies.forEach(function(p) {
        var has = p.servers.some(function(s) { return s.group === name; });
        if (has) count++;
      });
      return count;
    }

    function renderGroups() {
      var groups = Object.keys(state.groups).sort();
      if (groups.length === 0) {
        content.innerHTML = '<div class="empty">No groups found. Groups are derived from registered services.</div>';
        return;
      }
      var html = '';
      groups.forEach(function(name) {
        var members = state.groups[name];
        var active = isGroupActive(name);
        html += '<div class="group-row">' +
          '<div class="group-info">' +
            '<div class="group-name">' +
              '<span class="indicator' + (active ? '' : ' inactive') + '">●</span> ' +
              esc(name) +
            '</div>' +
            '<div class="group-members">' + members.map(esc).join(', ') + '</div>' +
          '</div>' +
          '<button class="btn" onclick="window.__switchGroup(\'' + esc(name).replace(/'/g, "\\'") + '\')">' +
            (active ? 'Active' : 'Switch') +
          '</button>' +
        '</div>';
      });
      content.innerHTML = html;
    }

    function renderProxies() {
      var proxies = state.proxies.slice().sort(function(a, b) { return a.port - b.port; });
      if (proxies.length === 0) {
        content.innerHTML = '<div class="empty">No proxies running.</div>';
        return;
      }
      var html = '';
      proxies.forEach(function(p) {
        var label = p.label ? esc(p.label) + ' ' : '';
        html += '<div class="section">' +
          '<div class="section-header">' + label + ':' + p.port + '</div>' +
          '<table><thead><tr><th></th><th>Server</th><th>Group</th><th>Port</th><th>PID</th><th></th></tr></thead><tbody>';
        var servers = (p.servers || []).slice().sort(function(a, b) { return a.name < b.name ? -1 : 1; });
        servers.forEach(function(s) {
          var isDefault = p.default === s.name;
          html += '<tr>' +
            '<td><span class="indicator' + (isDefault ? '' : ' inactive') + '">●</span></td>' +
            '<td>' + esc(s.name) + '</td>' +
            '<td class="mono">' + esc(s.group || '-') + '</td>' +
            '<td class="mono">:' + s.port + '</td>' +
            '<td class="mono">' + (s.pid || '-') + '</td>' +
            '<td class="btn-group">' +
              (isDefault
                ? '<button class="btn" onclick="window.__clearDefault(' + p.port + ')">Clear</button>'
                : '<button class="btn" onclick="window.__setDefault(' + p.port + ',\'' + esc(s.name).replace(/'/g, "\\'") + '\')">Switch</button>'
              ) +
            '</td>' +
          '</tr>';
        });
        html += '</tbody></table></div>';
      });
      content.innerHTML = html;
    }

    function renderServices() {
      var svcs = state.services.slice().sort(function(a, b) { return a.name < b.name ? -1 : 1; });
      if (svcs.length === 0) {
        content.innerHTML = '<div class="empty">No managed services. Services defined in mdp.yaml will appear here.</div>';
        return;
      }
      var html = '<table><thead><tr><th>Name</th><th>Group</th><th>Port</th><th>PID</th><th>Status</th></tr></thead><tbody>';
      svcs.forEach(function(s) {
        var cls = 'status-' + (s.status || 'stopped');
        html += '<tr>' +
          '<td>' + esc(s.name) + '</td>' +
          '<td class="mono">' + esc(s.group || '-') + '</td>' +
          '<td class="mono">:' + s.port + '</td>' +
          '<td class="mono">' + (s.pid || '-') + '</td>' +
          '<td><span class="status-badge ' + cls + '">' + esc(s.status || 'unknown') + '</span></td>' +
        '</tr>';
      });
      html += '</tbody></table>';
      content.innerHTML = html;
    }

    // Expose actions to inline handlers
    window.__switchGroup = switchGroup;
    window.__setDefault = setDefault;
    window.__clearDefault = clearDefault;

    // Initial fetch + SSE for real-time updates; if the browser closes the
    // stream permanently (non-200/wrong-MIME response), recreate it after a
    // delay.
    fetchState();
    if (typeof EventSource !== 'undefined') {
      var connectSSE = function() {
        var es = new EventSource(API + '/__mdp/events');
        es.onmessage = function() { fetchState(); };
        es.onerror = function() {
          if (es.readyState === EventSource.CLOSED) {
            setTimeout(connectSSE, 5000);
          }
        };
      };
      connectSSE();
    }
  })();
  </script>
</body>
</html>`, controlPort)
}
