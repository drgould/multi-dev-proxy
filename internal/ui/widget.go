package ui

import (
	_ "embed"
	"net/http"
)

//go:embed widget.js
var WidgetJS string

// WidgetHandler returns an HTTP handler for GET /__mdp/widget.js.
func WidgetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(WidgetJS))
	}
}
