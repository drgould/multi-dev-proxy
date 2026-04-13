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
	name := os.Getenv("NAME")
	if name == "" {
		name = "server"
	}
	color := os.Getenv("COLOR")
	if color == "" {
		color = "#1a1a2e,#16213e,#0f3460"
	}
	mode := os.Getenv("MODE") // "api" for JSON-only, anything else serves HTML
	apiURL := os.Getenv("API_URL")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/identity", identityHandler(name, color))
	mux.HandleFunc("/api/echo", echoHandler(name))
	mux.HandleFunc("/api/info", infoHandler(name, port))
	mux.HandleFunc("/health", healthHandler)

	if mode == "api" {
		mux.HandleFunc("/", apiRootHandler(name, port))
	} else {
		mux.HandleFunc("/", webHandler(name, color, port, apiURL))
	}

	addr := ":" + port
	fmt.Printf("%s listening on http://localhost:%s\n", name, port)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func identityHandler(name, color string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(map[string]string{
			"name":  name,
			"color": color,
		})
	}
}

func apiRootHandler(name, port string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"server":    name,
			"port":      port,
			"endpoints": []string{"/api/identity", "/api/echo", "/api/info", "/health"},
		})
	}
}

func webHandler(name, color, port, apiURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		requestCount.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, page, color, name, name, port, requestCount.Load(), time.Now().Format("15:04:05"), apiURL)
	}
}

func echoHandler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"server":  name,
			"method":  r.Method,
			"path":    r.URL.Path,
			"query":   r.URL.Query(),
			"headers": r.Header,
			"time":    time.Now().Format(time.RFC3339),
		})
	}
}

func infoHandler(name, port string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"server":   name,
			"port":     port,
			"requests": requestCount.Load(),
			"time":     time.Now().Format(time.RFC3339),
		})
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

const page = `<!DOCTYPE html>
<html>
<head><title>%[2]s</title></head>
<body style="margin:0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;background:linear-gradient(135deg,%[1]s);color:#fff;min-height:100vh;display:flex;align-items:center;justify-content:center">
<div style="max-width:500px;width:100%%;padding:2rem">
  <h1 style="font-size:2.5rem;margin:0 0 1rem">%[3]s</h1>
  <div style="background:rgba(255,255,255,0.1);border-radius:12px;padding:1.5rem;margin-bottom:1.5rem">
    <p><strong>Port:</strong> %[4]s</p>
    <p><strong>Requests:</strong> %[5]d</p>
    <p><strong>Time:</strong> %[6]s</p>
  </div>
  <div id="api-status" style="background:rgba(255,255,255,0.1);border-radius:12px;padding:1.5rem;margin-bottom:1.5rem;display:none">
    <h2 style="margin:0 0 0.75rem;font-size:1rem">Connected API</h2>
    <p id="api-name" style="font-size:1.1rem;font-weight:600"></p>
  </div>
</div>
<script>
(function() {
  var apiURL = %[7]q;
  if (!apiURL) return;
  function poll() {
    fetch(apiURL + '/api/identity')
      .then(function(r) { return r.json(); })
      .then(function(data) {
        var el = document.getElementById('api-status');
        var nameEl = document.getElementById('api-name');
        el.style.display = 'block';
        nameEl.textContent = data.name;
        nameEl.style.opacity = '1';
      })
      .catch(function() {
        var el = document.getElementById('api-status');
        var nameEl = document.getElementById('api-name');
        el.style.display = 'block';
        nameEl.textContent = '(unreachable)';
        nameEl.style.opacity = '0.5';
      });
  }
  poll();
  setInterval(poll, 5000);
})();
</script>
</body>
</html>`
