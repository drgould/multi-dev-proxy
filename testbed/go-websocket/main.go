package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"golang.org/x/net/websocket"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	tlsCert := os.Getenv("TLS_CERT")
	tlsKey := os.Getenv("TLS_KEY")

	http.HandleFunc("/", indexHandler(port))
	http.Handle("/ws", websocket.Handler(echoHandler))
	http.HandleFunc("/api/info", infoHandler(port))

	addr := ":" + port
	if tlsCert != "" && tlsKey != "" {
		fmt.Printf("Go WebSocket server listening on https://localhost:%s\n", port)
		if err := http.ListenAndServeTLS(addr, tlsCert, tlsKey, nil); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Printf("Go WebSocket server listening on http://localhost:%s\n", port)
		if err := http.ListenAndServe(addr, nil); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
}

func echoHandler(ws *websocket.Conn) {
	var msg string
	for {
		if err := websocket.Message.Receive(ws, &msg); err != nil {
			return
		}
		websocket.Message.Send(ws, "echo: "+msg)
	}
}

func infoHandler(port string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"server": "go-websocket",
			"port":   port,
			"time":   time.Now().Format(time.RFC3339),
		})
	}
}

func indexHandler(port string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, page, port, time.Now().Format("15:04:05"))
	}
}

const page = `<!DOCTYPE html>
<html>
<head><title>Go WebSocket</title></head>
<body style="margin:0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;background:linear-gradient(135deg,#0f2027,#1e3a5f,#2c5364);color:#fff;min-height:100vh;display:flex;align-items:center;justify-content:center">
<div style="max-width:600px;width:100%%;padding:2rem">
  <h1 style="font-size:2.5rem;margin:0 0 0.5rem">Go WebSocket</h1>
  <div style="background:rgba(255,255,255,0.1);border-radius:12px;padding:1.5rem;margin-bottom:1.5rem">
    <p><strong>Port:</strong> %s</p>
    <p><strong>Time:</strong> %s</p>
    <p><strong>Features:</strong> WebSocket echo, counter</p>
  </div>

  <div style="background:rgba(255,255,255,0.1);border-radius:12px;padding:1.5rem;margin-bottom:1.5rem">
    <h2 style="margin:0 0 0.75rem;font-size:1.1rem">Counter</h2>
    <span id="count" style="font-size:2rem;font-weight:700">0</span>
    <button onclick="document.getElementById('count').textContent=++window._c" style="margin-left:1rem;padding:0.5rem 1rem;border-radius:6px;border:none;background:#3b82f6;color:#fff;cursor:pointer;font-size:1rem">+1</button>
    <script>window._c=0</script>
  </div>

  <div style="background:rgba(255,255,255,0.1);border-radius:12px;padding:1.5rem;margin-bottom:1.5rem">
    <h2 style="margin:0 0 0.75rem;font-size:1.1rem">WebSocket Echo</h2>
    <div style="display:flex;gap:0.5rem;margin-bottom:0.75rem">
      <input id="wsin" value="hello from browser" style="flex:1;padding:0.5rem;border-radius:6px;border:1px solid #555;background:#1a1a1a;color:#fff">
      <button id="wsbtn" style="padding:0.5rem 1rem;border-radius:6px;border:none;background:#3b82f6;color:#fff;cursor:pointer">Send</button>
    </div>
    <pre id="wsout" style="background:#0a0a0a;padding:0.75rem;border-radius:6px;min-height:2rem;font-size:0.85rem;white-space:pre-wrap">Connecting...</pre>
    <script>
    (function(){
      var out=document.getElementById('wsout'), ws;
      function connect(){
        var proto=location.protocol==='https:'?'wss:':'ws:';
        ws=new WebSocket(proto+'//'+location.host+'/ws');
        ws.onopen=function(){out.textContent='Connected. Type a message and click Send.'};
        ws.onmessage=function(e){out.textContent+='\n'+e.data};
        ws.onclose=function(){out.textContent+='\nDisconnected.';setTimeout(connect,2000)};
        ws.onerror=function(){out.textContent='WebSocket error.'};
      }
      connect();
      document.getElementById('wsbtn').onclick=function(){
        var msg=document.getElementById('wsin').value;
        if(ws&&ws.readyState===1){ws.send(msg);out.textContent+='\n> '+msg}
      };
    })();
    </script>
  </div>

  <p style="text-align:center;color:rgba(255,255,255,0.4);font-size:0.8rem;margin-top:2rem">If mdp is working, you should see a floating switcher widget at the top of this page.</p>
</div>
</body>
</html>`
