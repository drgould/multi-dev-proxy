#!/bin/sh

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
ROOT_DIR=$(cd "$SCRIPT_DIR/.." && pwd)

cleanup() {
    echo ""
    echo "Stopping..."
    "$SCRIPT_DIR/mdp" --stop --control-port "$CTRL_PORT" 2>/dev/null
    pkill -P $$ 2>/dev/null
    (cd "$SCRIPT_DIR/docker" && docker compose down --timeout 3 2>/dev/null)
    wait 2>/dev/null
    echo "Done."
}
trap cleanup EXIT INT TERM

CTRL_PORT="${MDP_CONTROL_PORT:-13100}"
FRONTEND_PORT=3000
BACKEND_PORT=3001

echo "Building mdp..."
(cd "$ROOT_DIR" && go build -o "$SCRIPT_DIR/mdp" ./cmd/mdp)

echo "Building go-websocket..."
(cd "$SCRIPT_DIR/go-websocket" && go mod tidy && go build -o "$SCRIPT_DIR/go-websocket/go-websocket" .)

echo "Building echo-api..."
(cd "$SCRIPT_DIR/echo-api" && go build -o "$SCRIPT_DIR/echo-api/echo-api" .)

echo "Generating TLS cert..."
CERT_DIR="$SCRIPT_DIR/.certs"
mkdir -p "$CERT_DIR"
if [ ! -f "$CERT_DIR/localhost.pem" ]; then
  openssl req -x509 -newkey rsa:2048 \
    -keyout "$CERT_DIR/localhost-key.pem" \
    -out "$CERT_DIR/localhost.pem" \
    -days 365 -nodes -subj '/CN=localhost' 2>/dev/null
fi

echo "Installing JS deps (parallel)..."
(cd "$SCRIPT_DIR/vite-ts" && npm install --silent) &
(cd "$SCRIPT_DIR/nextjs" && npm install --silent) &
(cd "$SCRIPT_DIR/vue" && npm install --silent) &
(cd "$SCRIPT_DIR/svelte" && npm install --silent) &
wait

echo ""
echo "Starting orchestrator..."
echo ""

"$SCRIPT_DIR/mdp" -d \
  --control-port "$CTRL_PORT" \
  --tls-cert "$CERT_DIR/localhost.pem" \
  --tls-key "$CERT_DIR/localhost-key.pem" \
  --config "$SCRIPT_DIR/mdp.yaml"

if ! curl -so /dev/null "http://127.0.0.1:${CTRL_PORT}/__mdp/health" 2>/dev/null; then
  echo "ERROR: orchestrator failed to start on control port :${CTRL_PORT}"
  exit 1
fi

echo ""
echo "Starting all services from mdp.yaml..."
echo ""

"$SCRIPT_DIR/mdp" run --control-port "$CTRL_PORT" &
MDP_RUN_PID=$!

sleep 5

echo ""
printf "\033[1;36m============================================\033[0m\n"
printf "\033[1;36m  mdp testbed is running!\033[0m\n"
printf "\033[1;36m============================================\033[0m\n"
echo ""
printf "  Control API: \033[1;36mhttp://127.0.0.1:${CTRL_PORT}\033[0m\n"
printf "  Frontend:    \033[1;36mhttps://localhost:${FRONTEND_PORT}\033[0m\n"
printf "  Backend:     \033[1;36mhttps://localhost:${BACKEND_PORT}\033[0m\n"
echo ""
printf "  \033[1;33mdev\033[0m       vite :%s + echo :%s\n" "$FRONTEND_PORT" "$BACKEND_PORT"
printf "  \033[1;35mstaging\033[0m   next :%s + docker :%s\n" "$FRONTEND_PORT" "$BACKEND_PORT"
echo ""
printf "  Extras (ungrouped on :%s): vue, svelte, go-websocket\n" "$FRONTEND_PORT"
echo ""
echo "  Press Ctrl+C to stop everything."
echo ""

wait
