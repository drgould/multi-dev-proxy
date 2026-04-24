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

echo "Building mdp..."
(cd "$ROOT_DIR" && go build -o "$SCRIPT_DIR/mdp" ./cmd/mdp)

echo "Building testbed server..."
(cd "$SCRIPT_DIR/server" && go build -o "$SCRIPT_DIR/server/server" .)

echo "Building udp-echo..."
(cd "$SCRIPT_DIR/udp-echo" && go build -o "$SCRIPT_DIR/udp-echo/udp-echo" .)

echo "Generating TLS cert..."
CERT_DIR="$SCRIPT_DIR/.certs"
mkdir -p "$CERT_DIR"
if [ ! -f "$CERT_DIR/localhost.pem" ]; then
  openssl req -x509 -newkey rsa:2048 \
    -keyout "$CERT_DIR/localhost-key.pem" \
    -out "$CERT_DIR/localhost.pem" \
    -days 365 -nodes -subj '/CN=localhost' 2>/dev/null
fi

echo ""
echo "Starting orchestrator..."
echo ""

"$SCRIPT_DIR/mdp" -d \
  --control-port "$CTRL_PORT" \
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

sleep 3

echo ""
printf "\033[1;36m============================================\033[0m\n"
printf "\033[1;36m  mdp testbed is running!\033[0m\n"
printf "\033[1;36m============================================\033[0m\n"
echo ""
printf "  Control API: \033[1;36mhttp://127.0.0.1:${CTRL_PORT}\033[0m\n"
printf "  Dashboard:   \033[1;36mhttp://localhost:6370\033[0m\n"
printf "  Frontend:    \033[1;36mhttp://localhost:3000\033[0m (TLS on main)\n"
printf "  Backend:     \033[1;36mhttp://localhost:3001\033[0m (Docker on main)\n"
echo ""
printf "  Groups:\n"
printf "    \033[1;36mmain\033[0m       web (TLS) + api (Docker) + udp-echo\n"
printf "    \033[1;32mfeature-a\033[0m  web + api\n"
printf "    \033[1;31mfeature-b\033[0m  web + api\n"
echo ""
printf "  UDP echo (main group): port shown as \$UDP_PORT in .mdp.env\n"
printf "    printf 'hi' | socat -t1 - UDP:127.0.0.1:\$UDP_PORT\n"
echo ""
echo "  Press Ctrl+C to stop everything."
echo ""

wait
