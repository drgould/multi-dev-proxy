package ui

import (
	_ "embed"
	"net/http"
)

//go:embed sw.js
var ServiceWorkerJS string

// ServiceWorkerHandler returns an HTTP handler for GET /__mdp/sw.js.
func ServiceWorkerHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ServiceWorkerJS))
	}
}
