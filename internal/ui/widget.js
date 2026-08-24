(() => {
	"use strict";

	const API_SERVERS = "/__mdp/servers";
	const API_CONFIG = "/__mdp/config";

	// Every proxy is constructed with routing.CookieNameForPort(port) —
	// "__mdp_upstream_<port>", never the bare name — so this matches it
	// synchronously from the start, rather than waiting on the async
	// /__mdp/config fetch (poll() below still applies config.cookieName if
	// it ever legitimately differs). Needed because some cookie writes (the
	// reload bounce-correction) happen before that fetch could ever run.
	const CURRENT_PORT = location.port || (location.protocol === "https:" ? "443" : "80");
	let COOKIE = `__mdp_upstream_${CURRENT_PORT}`;
	let config = null;

	function getCookie() {
		const m = document.cookie.match(new RegExp(`(?:^|; )${COOKIE}=([^;]*)`));
		return m ? decodeURIComponent(m[1]) : null;
	}

	function setCookie(name) {
		// biome-ignore lint/suspicious/noDocumentCookie: Cookie Store API is not available in all target browsers.
		document.cookie = `${COOKIE}=${encodeURIComponent(name)}; path=/; SameSite=Lax`;
	}

	// --- Per-tab pin bootstrap ---
	// A tab's pin lives in sessionStorage (isolated per tab, unlike cookies)
	// once it's ever switched explicitly; tabs that never switch keep
	// following the shared cookie exactly as before. The one thing
	// sessionStorage can't influence is the very request that loads a fresh
	// or reloaded document — a Service Worker has no way to recognize "this
	// navigation belongs to the same tab as an earlier one" (clientId is
	// always empty for navigations, a platform constraint, not a gap here),
	// so that request resolves via the normal cookie/default path
	// server-side. window.__mdpServedBy (injected inline — see
	// internal/inject) names which upstream actually produced THIS
	// response; if it disagrees with the tab's stored pin, self-correct via
	// a one-time query-param bounce rather than silently showing the wrong
	// service. If the bounce itself still doesn't land on the pinned
	// service (it's gone/unavailable), give up and accept reality instead
	// of looping forever.
	const PIN_KEY = "__mdp_pin_upstream";

	function getStoredPin() {
		try {
			return sessionStorage.getItem(PIN_KEY);
		} catch {
			return null;
		}
	}
	function setStoredPin(name) {
		try {
			sessionStorage.setItem(PIN_KEY, name);
		} catch { /* ignore */ }
	}

	const urlParams = new URLSearchParams(location.search);
	const urlPin = urlParams.get("__mdp_upstream");
	const servedBy = window.__mdpServedBy || null;
	const storedPin = getStoredPin();

	// Set once the SW has confirmed this tab's pin (or a timeout gives up
	// waiting) — see the registerSW call below. Stripping the URL earlier
	// than that would remove the one thing sw.js's client-URL fallback can
	// read during the unavoidable gap before a postMessage pin arrives.
	let stripPinFromURL = () => {};

	if (urlPin) {
		// Arrived via an explicit switch action or a stale-reload
		// self-correction bounce (below) — trust what the server actually
		// resolved (servedBy) over whatever this tab had stored before.
		// If it matches, adopt the switch/bounce; if the requested service
		// turned out to be unavailable, accept reality instead of bouncing
		// again, which would loop forever.
		setStoredPin(servedBy || urlPin);
		stripPinFromURL = () => {
			const cleaned = new URL(location.href);
			cleaned.searchParams.delete("__mdp_upstream");
			cleaned.searchParams.delete("__mdp_ports");
			history.replaceState(null, "", cleaned.toString());
		};
	} else if (storedPin && servedBy && storedPin !== servedBy) {
		// A plain reload (no explicit pin on this request) landed on a
		// different service than this tab is pinned to — the shared
		// cookie must have changed from another tab. Self-correct with a
		// one-time bounce; the next load carries urlPin and is handled by
		// the branch above, so this never loops. Also reset the cookie to
		// this tab's own pin so the fallback path (before the SW takes
		// over, or if it's unsupported) is consistent with what we're
		// bouncing to, not whatever other tab last changed it to.
		setCookie(storedPin);
		const bounce = new URL(location.href);
		bounce.searchParams.set("__mdp_upstream", storedPin);
		window.location.replace(bounce.toString());
		return;
	} else if (!storedPin && servedBy) {
		// First time this tab establishes a pin: lock onto whatever the
		// server already resolved for THIS exact request — decided before
		// the HTML was even sent, so this is synchronous and immediate.
		// Locking here (rather than waiting on poll()'s fetchConfig() +
		// fetch(servers) round-trip below) closes a real race: if this
		// tab's first poll were delayed (slow dev server, network jitter)
		// past another tab's switch changing the shared cookie, THIS tab
		// would lock onto THAT other tab's choice instead of its own.
		setStoredPin(servedBy);
	}

	// Read fresh rather than reusing urlPin directly: the block above may
	// have adopted servedBy instead of urlPin (the requested pin turned out
	// to be unavailable), and sessionStorage reflects whichever it was.
	//
	// Not const: a tab that never explicitly switches still locks onto
	// whatever it first resolves to (see the auto-lock in poll() below), so
	// this can go from unset to set mid-session. Later reads (the click
	// interceptor, the WebSocket wrapper) close over this binding and pick
	// up that update automatically.
	let activePin = getStoredPin();

	// Vite's HMR client (and similar dev-server live-reload clients) opens
	// its own WebSocket directly — that connection is invisible to the
	// Service Worker (fetch interception never fires for WebSocket
	// handshakes, a platform constraint), so without this it always
	// resolves via the shared cookie/default instead of this tab's own
	// pin. A pinned tab could then have its HMR socket connected to a
	// DIFFERENT app's dev server, applying a Fast Refresh update against a
	// module tree that doesn't exist in this page — corrupting React
	// (symptom: "Cannot read properties of null (reading 'useContext')").
	// The WS handshake is still a normal HTTP request server-side, so
	// tagging it with the same query param used elsewhere is sufficient;
	// this must install before any app code (e.g. /@vite/client) has a
	// chance to construct its socket, which the injection order guarantees.
	if (typeof window.WebSocket === "function") {
		const NativeWebSocket = window.WebSocket;
		function PinnedWebSocket(url, protocols) {
			if (activePin) {
				try {
					const u = new URL(url, location.href);
					// Compare host, not origin: a ws(s): URL's origin string
					// keeps the ws(s) scheme (e.g. "ws://localhost:3000"),
					// which never equals the page's http(s) origin even for
					// the same host — origin equality would always be
					// false here.
					if (u.host === location.host && !u.searchParams.has("__mdp_upstream")) {
						u.searchParams.set("__mdp_upstream", activePin);
						url = u.toString();
					}
				} catch { /* ignore, fall through with original url */ }
			}
			return protocols === undefined
				? new NativeWebSocket(url)
				: new NativeWebSocket(url, protocols);
		}
		PinnedWebSocket.prototype = NativeWebSocket.prototype;
		for (const k of ["CONNECTING", "OPEN", "CLOSING", "CLOSED"]) {
			PinnedWebSocket[k] = NativeWebSocket[k];
		}
		window.WebSocket = PinnedWebSocket;
	}

	function registerSW(ports, onPinned) {
		if (!("serviceWorker" in navigator) || !ports) return;
		navigator.serviceWorker
			.register("/__mdp/sw.js", { scope: "/" })
			.then((reg) => {
				function sendPin(sw) {
					// postMessage is fire-and-forget — the SW processes it
					// whenever its event loop gets to it, not synchronously.
					// Calling onPinned right after posting would strip the
					// URL before clientMap is guaranteed populated, reopening
					// the exact race this is meant to close. A MessageChannel
					// reply lets the SW tell us once it's actually stored.
					if (onPinned) {
						const channel = new MessageChannel();
						channel.port1.onmessage = () => onPinned();
						sw.postMessage({ type: "pin", ports }, [channel.port2]);
					} else {
						sw.postMessage({ type: "pin", ports });
					}
				}
				const sw = reg.active || reg.installing || reg.waiting;
				if (sw && sw.state === "activated") {
					sendPin(sw);
				}
				// Listen for new or activating workers
				function onStateChange() {
					if (this.state === "activated") sendPin(this);
				}
				if (reg.installing) reg.installing.addEventListener("statechange", onStateChange);
				if (reg.waiting) reg.waiting.addEventListener("statechange", onStateChange);
				reg.addEventListener("updatefound", () => {
					if (reg.installing) reg.installing.addEventListener("statechange", onStateChange);
				});
			});
	}

	// Register this tab's pin for its OWN proxy port immediately, rather
	// than waiting on poll()'s fetchConfig()+fetch(servers) round-trip
	// (needed only for the cross-port sibling map). Module scripts and
	// other early sub-resource requests fire right after this script
	// returns control to the parser — if pinning waited on those two
	// network round-trips, those early requests would have no SW pin yet
	// and could fall through to the shared cookie, potentially loading a
	// different upstream's JS than the HTML that was just correctly
	// resolved via __mdp_upstream. poll() re-registers with the fuller
	// sibling-port map once config loads; this only covers this one port
	// in the meantime.
	if (activePin) {
		registerSW({ [CURRENT_PORT]: activePin }, stripPinFromURL);
		// Safety net: if the SW never confirms (unsupported browser,
		// registration failure), still clean up the URL eventually rather
		// than leaving __mdp_upstream visible forever.
		setTimeout(stripPinFromURL, 3000);
	}

	// Build port map from config: locate the group containing activeName,
	// then map each proxy port (current + siblings) to the service registered
	// on that proxy for that group. Driven by config.groupPortMaps so
	// multi-port services — where one service spans several proxies — pin
	// the cookie correctly on every relevant proxy.
	function buildPortMap(activeName) {
		if (!config || !activeName) return null;
		const groups = config.groups || {};
		let groupName = null;
		for (const [gn, members] of Object.entries(groups)) {
			if (members.includes(activeName)) {
				groupName = gn;
				break;
			}
		}
		if (!groupName) return null;
		const portMap = (config.groupPortMaps || {})[groupName] || {};
		const ports = {};
		// Current proxy port → active server (may differ from portMap entry
		// when the user has manually overridden the cookie on this proxy).
		ports[String(config.port)] = activeName;
		if (config.siblings) {
			for (const sib of config.siblings) {
				const name = portMap[String(sib.port)];
				if (name) ports[String(sib.port)] = name;
			}
		}
		return Object.keys(ports).length > 0 ? ports : null;
	}

	function getTheme() {
		const m = document.cookie.match(/(?:^|; )__mdp_theme=([^;]*)/);
		if (m) return m[1];
		return window.matchMedia("(prefers-color-scheme: light)").matches
			? "light"
			: "dark";
	}

	const host = document.createElement("div");
	host.id = "__mdp-widget-host";
	host.style.cssText =
		"position:fixed;top:0;left:50%;transform:translateX(-50%);z-index:2147483647;";
	host.setAttribute("data-theme", getTheme());
	const shadow = host.attachShadow({ mode: "open" });

	window
		.matchMedia("(prefers-color-scheme: light)")
		.addEventListener("change", () => {
			host.setAttribute("data-theme", getTheme());
		});

	const style = document.createElement("style");
	style.textContent = `
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
      margin-top: 4px; min-width: 240px; max-height: 400px; overflow-y: auto;
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
    .section-divider { border-top: 1px solid var(--border); margin: 4px 0; }
    .settings { display:flex; align-items:center; gap:6px; padding:8px 12px; font-size:11px; color:var(--group-label); cursor:pointer; border-top:1px solid var(--border); text-decoration:none; }
    .settings:hover { background:var(--item-hover); color:var(--text); }
    .gear { font-size:13px; }
    .sub-item {
      display: flex; align-items: center; gap: 8px;
      padding: 4px 12px 4px 28px; font-size: 11px; color: var(--group-label);
    }
    .sub-item .item-dot { width: 5px; height: 5px; }
  `;

	shadow.appendChild(style);

	let pillEl, dropdownEl;
	let open = false;
	let servers = {};

	// In single-mode name=`repo/branch` and group=branch, so the returned
	// service equals the group; pillLabel collapses the display in that case.
	function serviceFromName(fullName, repo, group) {
		if (group && fullName.startsWith(`${group}/`)) {
			return fullName.slice(group.length + 1);
		}
		if (repo && fullName.startsWith(`${repo}/`)) {
			return fullName.slice(repo.length + 1);
		}
		const i = fullName.lastIndexOf("/");
		return i >= 0 ? fullName.slice(i + 1) : fullName;
	}

	function pillLabel(data, activeName, allNames) {
		if (allNames.length === 0) return "";
		const name =
			activeName && allNames.includes(activeName) ? activeName : allNames[0];
		for (const repo of Object.keys(data)) {
			const info = data[repo][name];
			if (info) {
				const group = info.group || "";
				let groupCount = 0;
				if (group) {
					for (const r of Object.keys(data)) {
						for (const n of Object.keys(data[r])) {
							if (data[r][n].group === group) groupCount++;
						}
					}
				}
				// Single-mode: name = `<actualRepo>/<group>` (group may contain
				// "/"). The server-side parser splits `repo` at the last slash,
				// so for branches like "feature/foo" the API-given `repo`
				// includes part of the branch; derive it from name instead.
				if (group && name.endsWith(`/${group}`)) {
					const derivedRepo = name.slice(0, name.length - group.length - 1);
					if (derivedRepo === repo || repo.startsWith(`${derivedRepo}/`)) {
						return `${derivedRepo} \u00b7 ${group}`;
					}
				}
				const service = serviceFromName(name, repo, group);
				if (group && service && service !== group && groupCount > 1) {
					return `${repo} \u00b7 ${group} \u00b7 ${service}`;
				}
				return `${repo} \u00b7 ${group || service}`;
			}
		}
		const i = name.lastIndexOf("/");
		if (i < 0) return name;
		return `${name.slice(0, i)} \u00b7 ${name.slice(i + 1)}`;
	}

	function buildLocalGroups(data) {
		const groups = {};
		for (const repo of Object.keys(data)) {
			for (const fullName of Object.keys(data[repo])) {
				const info = data[repo][fullName];
				const g = info.group || "";
				if (!groups[g]) groups[g] = [];
				const branch = serviceFromName(fullName, repo, g);
				groups[g].push({ name: fullName, repo, branch, scheme: info.scheme });
			}
		}
		return groups;
	}

	function render(data, activeName) {
		const names = Object.keys(data).flatMap((repo) => Object.keys(data[repo]));
		if (names.length === 0) {
			if (host.parentNode) host.remove();
			return;
		}
		if (!host.parentNode) document.body.appendChild(host);

		const pillText = pillLabel(data, activeName, names);

		if (!pillEl) {
			pillEl = document.createElement("div");
			pillEl.className = "pill";
			pillEl.onclick = () => {
				open = !open;
				if (dropdownEl) dropdownEl.style.display = open ? "block" : "none";
			};
			shadow.appendChild(pillEl);
		}
		pillEl.innerHTML = `<span class="dot"></span>${pillText}`;

		if (!dropdownEl) {
			dropdownEl = document.createElement("div");
			dropdownEl.className = "dropdown";
			dropdownEl.style.display = "none";
			shadow.appendChild(dropdownEl);
		}
		dropdownEl.innerHTML = "";

		const localGroups = buildLocalGroups(data);
		const groupNames = Object.keys(localGroups).filter((g) => g !== "").sort();
		const ungrouped = localGroups[""] || [];

		for (const gname of groupNames) {
			const members = localGroups[gname];
			const groupActive = members.some((m) => m.name === activeName);
			const item = document.createElement("div");
			item.className = "item";
			item.innerHTML = `<span class="item-dot ${groupActive ? "green" : "gray"}"></span>${gname}`;
			item.onclick = () => switchGroup(gname);
			dropdownEl.appendChild(item);
			if (members.length > 1) {
				for (const svc of members.sort((a, b) => a.name.localeCompare(b.name))) {
					const isActive = svc.name === activeName;
					const sub = document.createElement("div");
					sub.className = "sub-item";
					sub.innerHTML = `<span class="item-dot ${isActive ? "green" : "gray"}"></span>${svc.repo} / ${svc.branch}`;
					dropdownEl.appendChild(sub);
				}
			}
		}

		if (ungrouped.length > 0) {
			if (groupNames.length > 0) {
				const div = document.createElement("div");
				div.className = "section-divider";
				dropdownEl.appendChild(div);
			}
			const label = document.createElement("div");
			label.className = "group-label";
			label.textContent = "ungrouped";
			dropdownEl.appendChild(label);
			for (const svc of ungrouped.sort((a, b) => a.name.localeCompare(b.name))) {
				const isActive = svc.name === activeName;
				const sub = document.createElement("div");
				sub.className = "sub-item";
				sub.innerHTML = `<span class="item-dot ${isActive ? "green" : "gray"}"></span>${svc.repo} / ${svc.branch}`;
				if (!isActive) {
					sub.onclick = () => switchServer(svc.name, svc.scheme);
				}
				dropdownEl.appendChild(sub);
			}
		}

		const link = document.createElement("a");
		link.className = "settings";
		link.href = "/__mdp/switch";
		link.innerHTML = '<span class="gear">\u2699</span> Settings & all servers';
		dropdownEl.appendChild(link);
	}

	// Switching embeds __mdp_upstream in the URL this tab navigates to,
	// which the bootstrap block at the top of this file reads on that
	// fresh load, stores into sessionStorage, and immediately strips from
	// the visible URL — pinning this tab going forward without ever
	// leaving a query param in the address bar. Without this, switching in
	// one tab (which also sets the shared, origin-wide cookie) would change
	// what every OTHER unpinned tab on this proxy displays/routes to, since
	// cookies aren't scoped per tab.
	function switchGroup(name) {
		// Deliberately does NOT call POST /__mdp/groups/{name}/switch — that
		// endpoint sets the shared default on every proxy hosting a member
		// of this group, affecting every OTHER tab's fallback resolution
		// too. This tab's own pin (below) is entirely sufficient for its
		// own routing; mutating shared state for it is pure risk.
		const localGroups = buildLocalGroups(servers);
		const members = localGroups[name] || [];
		if (members.length > 0) {
			const target = members[0].name;
			setCookie(target);
			const url = new URL(location.href);
			url.searchParams.set("__mdp_upstream", target);
			window.location.href = url.toString();
			return;
		}
		window.location.reload();
	}

	async function switchServer(fullName, scheme) {
		setCookie(fullName);
		const targetScheme = (scheme === "https") ? "https" : "http";
		const targetBase = `${targetScheme}://${location.hostname}:${location.port}`;
		let targetPath = "/";
		try {
			const resp = await fetch(`/__mdp/last-path/${encodeURIComponent(fullName)}`);
			if (resp.ok) {
				const lpData = await resp.json();
				if (lpData.path) targetPath = lpData.path;
			}
		} catch { /* ignore */ }
		const url = new URL(targetBase + targetPath);
		url.searchParams.set("__mdp_upstream", fullName);
		window.location.href = url.toString();
	}

	async function fetchConfig() {
		try {
			const resp = await fetch(API_CONFIG, { signal: AbortSignal.timeout(1000) });
			if (resp.ok) {
				config = await resp.json();
				if (config.cookieName) COOKIE = config.cookieName;
			}
		} catch { /* ignore */ }
	}

	let swRegistered = false;

	async function poll() {
		try {
			await fetchConfig();
			const resp = await fetch(API_SERVERS, { signal: AbortSignal.timeout(1000) });
			if (!resp.ok) return;
			servers = await resp.json();
			const active = activePin || getCookie();
			const allNames = Object.keys(servers).flatMap((r) =>
				Object.keys(servers[r]),
			);
			const activeName =
				active && allNames.includes(active) ? active : allNames[0] || null;

			// Lock this tab onto whatever it resolves to on its first
			// successful poll, so a later switch made in a DIFFERENT tab
			// (which changes the shared cookie) doesn't silently change
			// what this tab displays or routes to. Only this tab's own
			// switch action (or its reload bounce-correction) can change
			// it after this point.
			if (activeName && !activePin) {
				activePin = activeName;
				setStoredPin(activeName);
			}

			host.setAttribute("data-theme", getTheme());
			render(servers, activeName);

			// Register the routing Service Worker once we know the active
			// service and its port map.
			if (!swRegistered && activeName && config) {
				const ports = buildPortMap(activeName);
				if (ports) {
					registerSW(ports);
					swRegistered = true;
				}
			}
		} catch {
			/* proxy not reachable */
		}
	}

	poll();
	// SSE for real-time updates. The server sends an initial "connected"
	// event on every (re)connect, so onmessage also resyncs after the
	// browser's automatic EventSource reconnect. If the browser closes the
	// stream permanently (non-200/wrong-MIME response, e.g. an intermediary
	// 502), recreate it after a delay.
	if (typeof EventSource !== "undefined") {
		const connectSSE = () => {
			const es = new EventSource("/__mdp/events");
			es.onmessage = () => poll();
			es.onerror = () => {
				if (es.readyState === EventSource.CLOSED) {
					setTimeout(connectSSE, 5000);
				}
			};
		};
		connectSSE();
	}

	document.addEventListener("click", (e) => {
		if (!host.contains(e.target) && open) {
			open = false;
			if (dropdownEl) dropdownEl.style.display = "none";
		}
	});
})();
