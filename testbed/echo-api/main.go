package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

var requestCount atomic.Int64

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9090"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", indexHandler(port))
	mux.HandleFunc("/api/echo", echoHandler)
	mux.HandleFunc("/api/info", infoHandler(port))
	mux.HandleFunc("/api/slow", slowHandler)
	mux.HandleFunc("/api/status/{code}", statusHandler)
	mux.HandleFunc("/health", healthHandler)

	addr := ":" + port
	fmt.Printf("Echo API listening on http://localhost:%s\n", port)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func indexHandler(port string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, page, port, requestCount.Load(), time.Now().Format("15:04:05"))
	}
}

func echoHandler(w http.ResponseWriter, r *http.Request) {
	requestCount.Add(1)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"method":  r.Method,
		"path":    r.URL.Path,
		"query":   r.URL.Query(),
		"headers": r.Header,
		"time":    time.Now().Format(time.RFC3339),
	})
}

func infoHandler(port string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"server":   "echo-api",
			"port":     port,
			"requests": requestCount.Load(),
			"time":     time.Now().Format(time.RFC3339),
		})
	}
}

// slowHandler responds after a configurable delay (default 2s, max 30s).
func slowHandler(w http.ResponseWriter, r *http.Request) {
	requestCount.Add(1)
	delay := 2 * time.Second
	if d := r.URL.Query().Get("delay"); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil && parsed <= 30*time.Second {
			delay = parsed
		}
	}
	time.Sleep(delay)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"delayed": delay.String(),
		"time":    time.Now().Format(time.RFC3339),
	})
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	requestCount.Add(1)
	code := 200
	fmt.Sscanf(r.PathValue("code"), "%d", &code)
	if code < 100 || code > 599 {
		code = 200
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{
		"status": code,
		"time":   time.Now().Format(time.RFC3339),
	})
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

const page = `<!DOCTYPE html>
<html>
<head><title>Echo API — Backend</title></head>
<body style="margin:0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;background:linear-gradient(135deg,#1a0a2e,#3d1659,#6b21a8);color:#fff;min-height:100vh;display:flex;align-items:center;justify-content:center">
<div style="max-width:600px;width:100%%;padding:2rem">
  <h1 style="font-size:2.5rem;margin:0 0 0.5rem">Echo API — Backend</h1>
  <div style="background:rgba(255,255,255,0.1);border-radius:12px;padding:1.5rem;margin-bottom:1.5rem">
    <p><strong>Port:</strong> %s</p>
    <p><strong>Requests served:</strong> %d</p>
    <p><strong>Time:</strong> %s</p>
  </div>
  <div style="background:rgba(255,255,255,0.1);border-radius:12px;padding:1.5rem;margin-bottom:1.5rem">
    <h2 style="margin:0 0 0.75rem;font-size:1.1rem">Endpoints</h2>
    <ul style="margin:0;padding-left:1.2rem;line-height:1.8">
      <li><code>/api/echo</code> — reflects request back as JSON</li>
      <li><code>/api/info</code> — server metadata</li>
      <li><code>/api/slow?delay=3s</code> — delayed response</li>
      <li><code>/api/status/404</code> — arbitrary status code</li>
      <li><code>/health</code> — health check</li>
    </ul>
  </div>
  <p style="text-align:center;color:rgba(255,255,255,0.4);font-size:0.8rem;margin-top:2rem">Backend service for mdp multi-proxy testing.</p>
</div>
</body>
</html>`
