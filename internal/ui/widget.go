package ui

import "net/http"

// WidgetJS is the standalone JavaScript for the floating dev server switcher widget.
// It uses Shadow DOM for style isolation and is served at /__mdp/widget.js.
const WidgetJS = `(function() {
  'use strict';

  const COOKIE = '__mdp_upstream';
  const POLL_MS = 5000;
  const API = '/__mdp/servers';

  function getCookie() {
    const m = document.cookie.match(new RegExp('(?:^|; )' + COOKIE + '=([^;]*)'));
    return m ? decodeURIComponent(m[1]) : null;
  }

  function setCookie(name) {
    document.cookie = COOKIE + '=' + encodeURIComponent(name) + '; path=/; SameSite=Lax';
  }

  function getTheme() {
    const m = document.cookie.match(/(?:^|; )__mdp_theme=([^;]*)/);
    if (m) return m[1];
    return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  }

  // Create host element and attach shadow
  const host = document.createElement('div');
  host.id = '__mdp-widget-host';
  host.style.cssText = 'position:fixed;top:0;left:50%;transform:translateX(-50%);z-index:2147483647;';
  host.setAttribute('data-theme', getTheme());
  const shadow = host.attachShadow({ mode: 'open' });

  window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', () => {
    host.setAttribute('data-theme', getTheme());
  });

  const style = document.createElement('style');
  style.textContent = ` + "`" + `
    :host {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      --bg: #1a1a1a; --bg-hover: #262626; --text: #e5e5e5; --border: #333;
      --dropdown-bg: #1a1a1a; --dropdown-shadow: rgba(0,0,0,0.4);
      --item-hover: #262626; --item-active-bg: #0a1a0a; --item-active-text: #4ade80;
      --group-label: #525252; --dot-gray: #404040;
    }
    :host([data-theme="light"]) {
      --bg: #ffffff; --bg-hover: #f5f5f5; --text: #1a1a1a; --border: #e0e0e0;
      --dropdown-bg: #ffffff; --dropdown-shadow: rgba(0,0,0,0.1);
      --item-hover: #f5f5f5; --item-active-bg: #ecfdf5; --item-active-text: #16a34a;
      --group-label: #9ca3af; --dot-gray: #d1d5db;
    }
    .pill {
      display: inline-flex; align-items: center; gap: 6px;
      background: var(--bg); color: var(--text); border: 1px solid var(--border);
      padding: 4px 12px 4px 8px; border-radius: 0 0 8px 8px;
      font-size: 12px; cursor: pointer; white-space: nowrap;
      user-select: none;
    }
    .pill:hover { background: var(--bg-hover); }
    .dot { width: 6px; height: 6px; border-radius: 50%; background: #22c55e; box-shadow: 0 0 5px #22c55e80; flex-shrink: 0; }
    .dropdown {
      position: absolute; top: 100%; left: 50%; transform: translateX(-50%);
      background: var(--dropdown-bg); border: 1px solid var(--border); border-radius: 6px;
      margin-top: 4px; min-width: 200px; overflow: hidden;
      box-shadow: 0 4px 16px var(--dropdown-shadow);
    }
    .item {
      display: flex; align-items: center; gap: 8px;
      padding: 8px 12px; font-size: 12px; cursor: pointer; color: var(--text);
    }
    .item:hover { background: var(--item-hover); }
    .item.active { background: var(--item-active-bg); color: var(--item-active-text); cursor: default; }
    .item-dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; }
    .item-dot.green { background: #22c55e; }
    .item-dot.gray  { background: var(--dot-gray); }
    .group-label { padding: 6px 12px 2px; font-size: 10px; color: var(--group-label); text-transform: uppercase; letter-spacing: 0.05em; }
    .settings { display:flex; align-items:center; gap:6px; padding:8px 12px; font-size:11px; color:var(--group-label); cursor:pointer; border-top:1px solid var(--border); text-decoration:none; }
    .settings:hover { background:var(--item-hover); color:var(--text); }
    .gear { font-size:13px; }
  ` + "`" + `;

  shadow.appendChild(style);

  let pillEl, dropdownEl;
  let open = false;
  let servers = {};
  let pollTimer = null;

  function render(data, activeName) {
    const names = Object.keys(data).flatMap(repo => Object.keys(data[repo]));
    if (names.length === 0) {
      if (host.parentNode) host.remove();
      return;
    }
    if (!host.parentNode) document.body.appendChild(host);

    const branch = activeName ? activeName.split('/').pop() : names[0].split('/').pop();

    if (!pillEl) {
      pillEl = document.createElement('div');
      pillEl.className = 'pill';
      pillEl.onclick = () => {
        open = !open;
        if (dropdownEl) dropdownEl.style.display = open ? 'block' : 'none';
      };
      shadow.appendChild(pillEl);
    }
    pillEl.innerHTML = '<span class="dot"></span>' + branch;

    if (!dropdownEl) {
      dropdownEl = document.createElement('div');
      dropdownEl.className = 'dropdown';
      dropdownEl.style.display = 'none';
      shadow.appendChild(dropdownEl);
    }
    dropdownEl.innerHTML = '';
    for (const repo of Object.keys(data).sort()) {
      const label = document.createElement('div');
      label.className = 'group-label';
      label.textContent = repo;
      dropdownEl.appendChild(label);
      for (const fullName of Object.keys(data[repo]).sort()) {
        const isActive = fullName === activeName;
        const item = document.createElement('div');
        item.className = 'item' + (isActive ? ' active' : '');
        item.innerHTML = '<span class="item-dot ' + (isActive ? 'green' : 'gray') + '"></span>' + fullName.split('/').pop();
        if (!isActive) {
          item.onclick = () => { setCookie(fullName); window.location.reload(); };
        }
        dropdownEl.appendChild(item);
      }
    }
    const link = document.createElement('a');
    link.className = 'settings';
    link.href = '/__mdp/switch';
    link.innerHTML = '<span class="gear">\u2699</span> Settings & all servers';
    dropdownEl.appendChild(link);
  }

  async function poll() {
    try {
      const resp = await fetch(API, { signal: AbortSignal.timeout(1000) });
      if (!resp.ok) return;
      servers = await resp.json();
      const active = getCookie();
      const allNames = Object.keys(servers).flatMap(r => Object.keys(servers[r]));
      const activeName = active && allNames.includes(active) ? active : (allNames[0] || null);
      host.setAttribute('data-theme', getTheme());
      render(servers, activeName);
    } catch (e) { /* proxy not reachable */ }
  }

  poll();
  pollTimer = setInterval(poll, POLL_MS);

  // Close dropdown on outside click
  document.addEventListener('click', (e) => {
    if (!host.contains(e.target) && open) {
      open = false;
      if (dropdownEl) dropdownEl.style.display = 'none';
    }
  });
})();`

// WidgetHandler returns an HTTP handler for GET /__mdp/widget.js.
func WidgetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(WidgetJS))
	}
}
