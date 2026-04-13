package api

import (
	"fmt"
	"net/http"

	"github.com/derekgould/multi-dev-proxy/internal/ui"
)

// SSEHandler returns an HTTP handler for GET /__mdp/events.
// It streams server-sent events to the client whenever state changes.
func SSEHandler(broadcaster *ui.Broadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		// Send initial event so the client knows the connection is live.
		fmt.Fprint(w, "data: {\"type\":\"connected\"}\n\n")
		flusher.Flush()

		ch, unsub := broadcaster.Subscribe()
		defer unsub()

		for {
			select {
			case <-ch:
				fmt.Fprint(w, "data: {\"type\":\"update\"}\n\n")
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}
