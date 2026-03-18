package proxy

import (
	"net/http"
	"strings"
)

// wsHeaders maps Go's canonicalized (wrong) form to the correct RFC form.
var wsHeaders = map[string]string{
	"Sec-Websocket-Key":        "Sec-WebSocket-Key",
	"Sec-Websocket-Version":    "Sec-WebSocket-Version",
	"Sec-Websocket-Extensions": "Sec-WebSocket-Extensions",
	"Sec-Websocket-Protocol":   "Sec-WebSocket-Protocol",
	"Sec-Websocket-Accept":     "Sec-WebSocket-Accept",
}

// FixWebSocketHeaders corrects Go's canonicalization of Sec-WebSocket-* headers.
// Go's net/http canonicalizes "Sec-WebSocket-Key" to "Sec-Websocket-Key" (wrong).
// This function moves them to the correct casing by directly manipulating the header map.
func FixWebSocketHeaders(h http.Header) {
	for wrong, correct := range wsHeaders {
		if vals, ok := h[wrong]; ok {
			delete(h, wrong)
			h[correct] = vals
		}
	}
}

// IsWebSocketUpgrade returns true if the request is a WebSocket upgrade request.
func IsWebSocketUpgrade(r *http.Request) bool {
	return r.Header.Get("Upgrade") == "websocket" ||
		containsCI(r.Header.Get("Connection"), "upgrade") &&
			containsCI(r.Header.Get("Upgrade"), "websocket")
}

func containsCI(s, substr string) bool {
	s = strings.ToLower(s)
	substr = strings.ToLower(substr)
	return strings.Contains(s, substr)
}
