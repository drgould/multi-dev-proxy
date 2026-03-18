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

  // Create host element and attach shadow
  const host = document.createElement('div');
  host.id = '__mdp-widget-host';
  host.style.cssText = 'position:fixed;top:0;left:50%;transform:translateX(-50%);z-index:2147483647;';
  const shadow = host.attachShadow({ mode: 'open' });

  const style = document.createElement('style');
  style.textContent = ` + "`" + `
    :host { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    .pill {
      display: inline-flex; align-items: center; gap: 6px;
      background: #1a1a1a; color: #e5e5e5; border: 1px solid #333;
      padding: 4px 12px 4px 8px; border-radius: 0 0 8px 8px;
      font-size: 12px; cursor: pointer; white-space: nowrap;
      user-select: none;
    }
    .pill:hover { background: #262626; }
    .dot { width: 6px; height: 6px; border-radius: 50%; background: #22c55e; box-shadow: 0 0 5px #22c55e80; flex-shrink: 0; }
    .dropdown {
      position: absolute; top: 100%; left: 50%; transform: translateX(-50%);
      background: #1a1a1a; border: 1px solid #333; border-radius: 6px;
      margin-top: 4px; min-width: 200px; overflow: hidden;
      box-shadow: 0 4px 16px rgba(0,0,0,0.4);
    }
    .item {
      display: flex; align-items: center; gap: 8px;
      padding: 8px 12px; font-size: 12px; cursor: pointer; color: #e5e5e5;
    }
    .item:hover { background: #262626; }
    .item.active { background: #0a1a0a; color: #4ade80; cursor: default; }
    .item-dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; }
    .item-dot.green { background: #22c55e; }
    .item-dot.gray  { background: #404040; }
    .group-label { padding: 6px 12px 2px; font-size: 10px; color: #525252; text-transform: uppercase; letter-spacing: 0.05em; }
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
        if (names.length <= 1) return;
        open = !open;
        if (dropdownEl) dropdownEl.style.display = open ? 'block' : 'none';
      };
      shadow.appendChild(pillEl);
    }
    pillEl.innerHTML = '<span class="dot"></span>' + branch;

    if (names.length > 1) {
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
    }
  }

  async function poll() {
    try {
      const resp = await fetch(API, { signal: AbortSignal.timeout(1000) });
      if (!resp.ok) return;
      servers = await resp.json();
      const active = getCookie();
      const allNames = Object.keys(servers).flatMap(r => Object.keys(servers[r]));
      const activeName = active && allNames.includes(active) ? active : (allNames[0] || null);
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
