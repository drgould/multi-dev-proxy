(() => {
	"use strict";

	// Per-client port→serverName map for per-tab/per-iframe routing isolation.
	// clientId is unique per tab/iframe, so each gets independent routing.
	const clientMap = new Map();

	self.addEventListener("install", () => self.skipWaiting());
	self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

	self.addEventListener("message", (e) => {
		if (e.data && e.data.type === "pin" && e.data.ports && e.source) {
			clientMap.set(e.source.id, e.data.ports);
		}
	});

	self.addEventListener("fetch", (e) => {
		const id = e.clientId || e.resultingClientId;
		const ports = clientMap.get(id);
		if (!ports) return;

		const url = new URL(e.request.url);
		if (url.pathname.startsWith("/__mdp/")) return;

		const port = url.port || (url.protocol === "https:" ? "443" : "80");
		const upstream = ports[port];
		if (!upstream) return;

		url.searchParams.set("__mdp_upstream", upstream);

		// For navigation requests, carry the port map so the widget can re-register
		if (e.request.mode === "navigate") {
			const portStr = Object.entries(ports)
				.map(([p, n]) => p + ":" + n)
				.join(",");
			url.searchParams.set("__mdp_ports", portStr);
		}

		const newReq = new Request(url, {
			method: e.request.method,
			headers: e.request.headers,
			body: e.request.body,
			mode: e.request.mode,
			credentials: e.request.credentials,
			redirect: e.request.redirect,
			referrer: e.request.referrer,
		});
		e.respondWith(fetch(newReq));
	});
})();
