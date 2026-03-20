#!/bin/sh
set -e

DIR=$(cd "$(dirname "$0")" && pwd)
cd "$DIR"

docker compose up -d --wait

HOST_PORT=$(docker compose port nginx 80 | cut -d: -f2)
echo "Docker server listening on http://localhost:${HOST_PORT}"

cleanup() {
    docker compose down --timeout 3 2>/dev/null
}
trap cleanup EXIT INT TERM

docker compose logs -f
