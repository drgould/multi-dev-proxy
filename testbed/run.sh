#!/bin/sh

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
ROOT_DIR=$(cd "$SCRIPT_DIR/.." && pwd)

cleanup() {
    echo ""
    echo "Stopping..."
    pkill -P $$ 2>/dev/null
    if [ -n "$DOCKER_RUNNING" ]; then
      (cd "$SCRIPT_DIR/docker" && docker compose down --timeout 3 2>/dev/null)
    fi
    wait 2>/dev/null
    echo "Done."
}
trap cleanup EXIT INT TERM

prefix() {
  awk -v c="$1" -v l="$2" '{printf "\033[%sm[%-6s]\033[0m %s\n", c, l, $0; fflush()}'
}

C_PROXY="1;36"
C_BLUE="1;34"
C_GREEN="1;32"
C_PURPLE="1;35"
C_ORANGE="1;33"
C_RED="1;31"
C_TEAL="0;96"

echo "Building mdp..."
(cd "$ROOT_DIR" && go build -o "$SCRIPT_DIR/mdp" ./cmd/mdp)

echo "Building go-websocket..."
(cd "$SCRIPT_DIR/go-websocket" && go mod tidy && go build -o "$SCRIPT_DIR/go-websocket/go-websocket" .)

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
echo "Starting proxy and all servers..."
echo ""

"$SCRIPT_DIR/mdp" start 2>&1 | prefix "$C_PROXY" "proxy" &
sleep 2

if ! curl -kso /dev/null "https://localhost:3000/__mdp/health" 2>/dev/null; then
  echo "ERROR: proxy failed to start on :3000"
  exit 1
fi

TLS_CERT="$CERT_DIR/localhost.pem" TLS_KEY="$CERT_DIR/localhost-key.pem" \
  "$SCRIPT_DIR/mdp" run --name "testbed/go-websocket" \
  --tls-cert "$CERT_DIR/localhost.pem" --tls-key "$CERT_DIR/localhost-key.pem" \
  -- "$SCRIPT_DIR/go-websocket/go-websocket" 2>&1 | prefix "$C_BLUE" "go-ws" &

"$SCRIPT_DIR/mdp" run --name "testbed/vite-ts" \
  -- npm --prefix "$SCRIPT_DIR/vite-ts" run dev 2>&1 | prefix "$C_GREEN" "vite" &

"$SCRIPT_DIR/mdp" run --name "frontend-app/nextjs" \
  -- npm --prefix "$SCRIPT_DIR/nextjs" run dev 2>&1 | prefix "$C_PURPLE" "next" &

"$SCRIPT_DIR/mdp" run --name "frontend-app/vue" \
  -- npm --prefix "$SCRIPT_DIR/vue" run dev 2>&1 | prefix "$C_ORANGE" "vue" &

"$SCRIPT_DIR/mdp" run --name "testbed/svelte" \
  -- npm --prefix "$SCRIPT_DIR/svelte" run dev 2>&1 | prefix "$C_RED" "svelte" &

if command -v docker >/dev/null 2>&1; then
  echo "Starting Docker stack..."
  (cd "$SCRIPT_DIR/docker" && docker compose up -d --wait --build 2>&1) || true
  DOCKER_PORT=$(cd "$SCRIPT_DIR/docker" && docker compose port nginx 80 2>/dev/null | cut -d: -f2)
  if [ -n "$DOCKER_PORT" ]; then
    "$SCRIPT_DIR/mdp" register --port "$DOCKER_PORT" "backend-api/docker"
    DOCKER_RUNNING=1
    (cd "$SCRIPT_DIR/docker" && docker compose logs -f 2>&1) | prefix "$C_TEAL" "docker" &
    PIDS="$PIDS $!"
  else
    echo "Docker stack failed to start"
  fi
else
  echo "Docker not found, skipping docker testbed server"
fi

sleep 5

echo ""
printf "\033[1;36m============================================\033[0m\n"
printf "\033[1;36m  mdp testbed is running!\033[0m\n"
printf "\033[1;36m============================================\033[0m\n"
echo ""
printf "  Open: \033[1;36mhttps://localhost:3000\033[0m\n"
echo ""
printf "  \033[1;34mgo-websocket\033[0m  Go + WebSocket echo (HTTPS)\n"
printf "  \033[1;32mvite-ts\033[0m       Vite + TypeScript + SQLite CRUD\n"
printf "  \033[1;35mnextjs\033[0m        Next.js with SSR\n"
printf "  \033[1;33mvue\033[0m           Vue 3 + TypeScript\n"
printf "  \033[1;31msvelte\033[0m        SvelteKit + TypeScript\n"
if [ -n "$DOCKER_RUNNING" ]; then
printf "  \033[0;96mdocker\033[0m        nginx + GraphQL + Postgres\n"
fi
echo ""
echo "  Press Ctrl+C to stop everything."
echo ""

wait
