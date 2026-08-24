(() => {
	"use strict";

	// Per-tab routing isolation for same-origin sub-resource requests
	// (fetch/XHR/module imports) within an already-pinned tab. Two sources
	// of the pin, tried in order:
	//   1. clientMap, populated by a postMessage from widget.js. Delivering
	//      a message to a Service Worker is unavoidably async — the app's
	//      own module-script fetches can (and reliably do, for every fresh
	//      pinned load, not just occasionally) fire before this arrives.
	//   2. The requesting client's own URL, which still carries
	//      __mdp_upstream for a brief window after a switch/bounce —
	//      widget.js only strips it once it's confirmed the postMessage
	//      pin above, specifically so this fallback has something to read
	//      during that gap. Synchronously available (no message round-trip
	//      needed), so it closes the race the postMessage path alone can't.
	//
	// Deliberately does NOT touch navigation requests: FetchEvent.clientId is
	// always empty for navigations (no stable tab identity is exposed to a
	// Service Worker across separate top-level navigations — this is a
	// platform constraint, not a gap here), so a stale reload can't be fixed
	// from inside the SW. That's handled client-side instead: widget.js
	// compares sessionStorage against a server-set marker of which upstream
	// actually served the page, and self-corrects via a brief query-param
	// bounce if they disagree.
	const clientMap = new Map();

	// Clients confirmed to have no pin (checked once via portsFromClientURL
	// and found nothing, or never pinned via postMessage). Lets ordinary,
	// unpinned traffic — the overwhelming majority in normal use — skip
	// both the async client-URL lookup and respondWith entirely after the
	// first request, rather than paying that cost (and that fragility: a
	// throw or rejected fetch inside handleFetch would otherwise fail a
	// request that should have gone through completely untouched) on every
	// single one. clientMap is always checked first, so a client that gets
	// pinned later via postMessage is never shadowed by this cache.
	const noPinClients = new Set();

	async function portsFromClientURL(id) {
		if (!id) return null;
		const client = await self.clients.get(id);
		if (!client) return null;
		try {
			const u = new URL(client.url);
			const upstream = u.searchParams.get("__mdp_upstream");
			return upstream ? { [u.port || (u.protocol === "https:" ? "443" : "80")]: upstream } : null;
		} catch {
			return null;
		}
	}

	self.addEventListener("install", () => self.skipWaiting());
	self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

	self.addEventListener("message", (e) => {
		if (e.data && e.data.type === "pin" && e.data.ports && e.source) {
			clientMap.set(e.source.id, e.data.ports);
			noPinClients.delete(e.source.id);
			// Ack once actually stored — widget.js waits for this before
			// stripping __mdp_upstream from the URL, since postMessage
			// delivery/processing is otherwise unobservable from the sender.
			if (e.ports && e.ports[0]) e.ports[0].postMessage({ type: "pinned" });
		}
	});

	async function handleFetch(e) {
		const ports = clientMap.get(e.clientId) || (await portsFromClientURL(e.clientId));
		if (!ports) {
			noPinClients.add(e.clientId);
			return fetch(e.request);
		}

		const url = new URL(e.request.url);
		if (url.pathname.startsWith("/__mdp/")) return fetch(e.request);

		const port = url.port || (url.protocol === "https:" ? "443" : "80");
		const upstream = ports[port];
		if (!upstream) return fetch(e.request);

		// mode: "no-cors" is the default for <img>, classic <script>, CSS,
		// and fonts — regardless of same-origin-ness. The Fetch spec forbids
		// non-safelisted headers on a no-cors request; constructing one with
		// a custom header throws. Only cors/same-origin requests (fetch/XHR,
		// module script imports) can safely carry the pin as a header —
		// everything else, and all cross-origin requests, use the query
		// param instead, which has no such restriction.
		const canUseHeader = url.origin === self.location.origin && e.request.mode !== "no-cors";

		// duplex: "half" is required whenever a body is forwarded and may be
		// a ReadableStream (same-origin POST/PUT/PATCH bodies commonly are
		// in Chromium) — omitting it throws just as readily as the header
		// restriction above.
		const bodyInit = e.request.body ? { duplex: "half" } : {};

		if (canUseHeader) {
			// cache: "no-store" is required here — the URL is unchanged, so
			// the HTTP cache would otherwise key purely on it and could
			// serve a response fetched under a DIFFERENT tab's pin (the
			// browser cache doesn't partition by request header unless the
			// response opts in via Vary, which real dev-server responses
			// don't know to send).
			const headers = new Headers(e.request.headers);
			headers.set("X-Mdp-Pin", upstream);
			return fetch(
				new Request(url, {
					method: e.request.method,
					headers,
					body: e.request.body,
					mode: e.request.mode,
					credentials: e.request.credentials,
					redirect: e.request.redirect,
					referrer: e.request.referrer,
					cache: "no-store",
					...bodyInit,
				}),
			);
		}

		// Query param: used for no-cors requests (any origin) and for
		// cross-origin requests (a sibling proxy port for the same group,
		// e.g. a linked API service, where a custom header would trigger a
		// CORS preflight the real upstream app has no reason to allow. Never
		// visible to the user — this only ever hits the wire.
		if (url.searchParams.get("__mdp_upstream") === upstream) return fetch(e.request);
		url.searchParams.set("__mdp_upstream", upstream);
		return fetch(
			new Request(url, {
				method: e.request.method,
				headers: e.request.headers,
				body: e.request.body,
				mode: e.request.mode,
				credentials: e.request.credentials,
				redirect: e.request.redirect,
				referrer: e.request.referrer,
				...bodyInit,
			}),
		);
	}

	self.addEventListener("fetch", (e) => {
		if (e.request.mode === "navigate") return;
		// Known-unpinned clients skip the SW path entirely — let the
		// browser handle the request exactly as if this SW weren't here.
		if (!clientMap.has(e.clientId) && noPinClients.has(e.clientId)) return;
		e.respondWith(handleFetch(e));
	});
})();
