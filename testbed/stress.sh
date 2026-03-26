#!/bin/sh
#
# Stress test for the mdp orchestrator.
# Requires: the testbed running (./run.sh in another terminal) OR a standalone orchestrator.
# Starts its own orchestrator if none is detected.
#
set -e

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
ROOT_DIR=$(cd "$SCRIPT_DIR/.." && pwd)

CTRL_PORT="${MDP_CONTROL_PORT:-13100}"
CTRL="http://127.0.0.1:${CTRL_PORT}"
PROXY_PORT=3000
PROXY="https://localhost:${PROXY_PORT}"

STARTED_ORCH=""
PIDS=""

cleanup() {
    echo ""
    echo "[stress] cleaning up..."
    for p in $PIDS; do kill "$p" 2>/dev/null; done
    if [ -n "$STARTED_ORCH" ]; then
        "$SCRIPT_DIR/mdp" --stop --control-port "$CTRL_PORT" 2>/dev/null
    fi
    wait 2>/dev/null
    echo "[stress] done."
}
trap cleanup EXIT INT TERM

pass() { printf "  \033[1;32m✓\033[0m %s\n" "$1"; }
fail() { printf "  \033[1;31m✗\033[0m %s\n" "$1"; FAILURES=$((FAILURES+1)); }

FAILURES=0

echo "Building mdp..."
(cd "$ROOT_DIR" && go build -o "$SCRIPT_DIR/mdp" ./cmd/mdp)

echo "Building echo-api..."
(cd "$SCRIPT_DIR/echo-api" && go build -o "$SCRIPT_DIR/echo-api/echo-api" .)

if ! curl -so /dev/null "${CTRL}/__mdp/health" 2>/dev/null; then
    echo "No orchestrator detected, starting one..."
    CERT_DIR="$SCRIPT_DIR/.certs"
    mkdir -p "$CERT_DIR"
    if [ ! -f "$CERT_DIR/localhost.pem" ]; then
      openssl req -x509 -newkey rsa:2048 \
        -keyout "$CERT_DIR/localhost-key.pem" \
        -out "$CERT_DIR/localhost.pem" \
        -days 365 -nodes -subj '/CN=localhost' 2>/dev/null
    fi
    "$SCRIPT_DIR/mdp" -d \
      --control-port "$CTRL_PORT" \
      --tls-cert "$CERT_DIR/localhost.pem" \
      --tls-key "$CERT_DIR/localhost-key.pem"
    STARTED_ORCH=1
    sleep 1
fi

echo ""
echo "=========================================="
echo "  mdp stress test"
echo "=========================================="
echo ""

# ---------------------------------------------------------------------------
# 1. Rapid registration: register many ephemeral servers quickly
# ---------------------------------------------------------------------------
echo "[1] Rapid registration (50 servers)..."
for i in $(seq 1 50); do
    PORT=$((20000 + i))
    curl -sX POST "${CTRL}/__mdp/register" \
      -H 'Content-Type: application/json' \
      -d "{\"name\":\"stress/server-${i}\",\"port\":${PORT},\"proxyPort\":${PROXY_PORT},\"group\":\"stress-a\"}" \
      -o /dev/null &
done
wait

PROXIES=$(curl -s "${CTRL}/__mdp/proxies")
COUNT=$(echo "$PROXIES" | python3 -c "import sys,json; d=json.load(sys.stdin); print(sum(len(p['servers']) for p in d))" 2>/dev/null || echo 0)
if [ "$COUNT" -ge 50 ]; then
    pass "registered 50 servers ($COUNT total)"
else
    fail "expected >=50 servers, got $COUNT"
fi

# ---------------------------------------------------------------------------
# 2. Rapid deregistration: remove all stress servers
# ---------------------------------------------------------------------------
echo "[2] Rapid deregistration (50 servers)..."
for i in $(seq 1 50); do
    curl -sX DELETE "${CTRL}/__mdp/register/stress%2Fserver-${i}" -o /dev/null &
done
wait

PROXIES=$(curl -s "${CTRL}/__mdp/proxies")
REMAINING=$(echo "$PROXIES" | python3 -c "import sys,json; d=json.load(sys.stdin); print(sum(1 for p in d for s in p['servers'] if s['name'].startswith('stress/')))" 2>/dev/null || echo 0)
if [ "$REMAINING" -eq 0 ]; then
    pass "all 50 stress servers deregistered"
else
    fail "expected 0 remaining stress servers, got $REMAINING"
fi

# ---------------------------------------------------------------------------
# 3. Multi-group registration and group switching
# ---------------------------------------------------------------------------
echo "[3] Multi-group registration + switching..."
for g in alpha beta gamma delta; do
    for i in 1 2 3; do
        PORT=$((30000 + $(echo "$g" | cksum | cut -d' ' -f1 | head -c4) + i))
        curl -sX POST "${CTRL}/__mdp/register" \
          -H 'Content-Type: application/json' \
          -d "{\"name\":\"${g}/svc-${i}\",\"port\":${PORT},\"proxyPort\":${PROXY_PORT},\"group\":\"${g}\"}" \
          -o /dev/null
    done
done

GROUPS=$(curl -s "${CTRL}/__mdp/groups")
GROUP_COUNT=$(echo "$GROUPS" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo 0)
if [ "$GROUP_COUNT" -ge 4 ]; then
    pass "created $GROUP_COUNT groups"
else
    fail "expected >=4 groups, got $GROUP_COUNT"
fi

for g in alpha beta gamma delta; do
    STATUS=$(curl -sX POST "${CTRL}/__mdp/groups/${g}/switch" -w '%{http_code}' -o /dev/null)
    if [ "$STATUS" = "200" ]; then
        pass "switched to group '$g'"
    else
        fail "switch to group '$g' returned $STATUS"
    fi
done

# ---------------------------------------------------------------------------
# 4. Rapid group switching under load (concurrent switches)
# ---------------------------------------------------------------------------
echo "[4] Rapid group switching (100 switches, concurrent)..."
for _ in $(seq 1 100); do
    g=$(echo "alpha beta gamma delta" | tr ' ' '\n' | shuf -n1 2>/dev/null || echo "alpha")
    curl -sX POST "${CTRL}/__mdp/groups/${g}/switch" -o /dev/null &
done
wait
pass "100 concurrent group switches completed"

# ---------------------------------------------------------------------------
# 5. Register real server and verify proxy routing
# ---------------------------------------------------------------------------
echo "[5] Live proxy routing with real backend..."
"$SCRIPT_DIR/echo-api/echo-api" &
ECHO_PID=$!
PIDS="$PIDS $ECHO_PID"
sleep 1

ECHO_PORT=9090
curl -sX POST "${CTRL}/__mdp/register" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"stress/live-echo\",\"port\":${ECHO_PORT},\"proxyPort\":${PROXY_PORT},\"group\":\"stress-live\"}" \
  -o /dev/null

curl -sX POST "${CTRL}/__mdp/proxies/${PROXY_PORT}/default/stress%2Flive-echo" -o /dev/null

sleep 1
HTTP_CODE=$(curl -kso /dev/null -w '%{http_code}' "${PROXY}/api/info")
if [ "$HTTP_CODE" = "200" ]; then
    pass "proxy routes to live echo-api (status $HTTP_CODE)"
else
    fail "proxy returned status $HTTP_CODE (expected 200)"
fi

# ---------------------------------------------------------------------------
# 6. Concurrent proxy requests during switching
# ---------------------------------------------------------------------------
echo "[6] Concurrent proxy requests during group switching..."
for _ in $(seq 1 50); do
    curl -kso /dev/null "${PROXY}/api/info" &
done
for g in alpha beta gamma delta stress-live; do
    curl -sX POST "${CTRL}/__mdp/groups/${g}/switch" -o /dev/null &
done
wait
pass "50 proxy requests + 5 group switches completed concurrently"

# ---------------------------------------------------------------------------
# 7. Default upstream set/clear cycle
# ---------------------------------------------------------------------------
echo "[7] Default upstream set/clear cycle (50 iterations)..."
for _ in $(seq 1 50); do
    curl -sX POST "${CTRL}/__mdp/proxies/${PROXY_PORT}/default/stress%2Flive-echo" -o /dev/null
    curl -sX DELETE "${CTRL}/__mdp/proxies/${PROXY_PORT}/default" -o /dev/null
done
pass "50 set/clear default cycles completed"

# ---------------------------------------------------------------------------
# 8. Health endpoint under load
# ---------------------------------------------------------------------------
echo "[8] Health endpoint bombardment (200 requests)..."
for _ in $(seq 1 200); do
    curl -so /dev/null "${CTRL}/__mdp/health" &
done
wait
pass "200 health requests completed"

# ---------------------------------------------------------------------------
# 9. Multi-proxy: register on multiple ports simultaneously
# ---------------------------------------------------------------------------
echo "[9] Multi-proxy registration (3 proxy ports, 10 servers each)..."
for port in 4000 4001 4002; do
    for i in $(seq 1 10); do
        SPORT=$((40000 + port + i))
        curl -sX POST "${CTRL}/__mdp/register" \
          -H 'Content-Type: application/json' \
          -d "{\"name\":\"multi/port${port}-svc${i}\",\"port\":${SPORT},\"proxyPort\":${port},\"group\":\"multi-${port}\"}" \
          -o /dev/null &
    done
done
wait

PROXIES=$(curl -s "${CTRL}/__mdp/proxies")
PROXY_COUNT=$(echo "$PROXIES" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo 0)
if [ "$PROXY_COUNT" -ge 4 ]; then
    pass "created $PROXY_COUNT proxy instances"
else
    fail "expected >=4 proxy instances, got $PROXY_COUNT"
fi

# ---------------------------------------------------------------------------
# 10. Config endpoint returns correct data
# ---------------------------------------------------------------------------
echo "[10] Config endpoint validation..."
CONFIG=$(curl -ks "${PROXY}/__mdp/config")
COOKIE_NAME=$(echo "$CONFIG" | python3 -c "import sys,json; print(json.load(sys.stdin).get('cookieName',''))" 2>/dev/null || echo "")
if [ "$COOKIE_NAME" = "__mdp_upstream_${PROXY_PORT}" ]; then
    pass "config returns correct cookieName: $COOKIE_NAME"
else
    fail "expected cookieName __mdp_upstream_${PROXY_PORT}, got '$COOKIE_NAME'"
fi

SIBLING_COUNT=$(echo "$CONFIG" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('siblings',[])))" 2>/dev/null || echo 0)
if [ "$SIBLING_COUNT" -ge 1 ]; then
    pass "config reports $SIBLING_COUNT sibling proxies"
else
    fail "expected >=1 sibling proxies, got $SIBLING_COUNT"
fi

# ---------------------------------------------------------------------------
# 11. CORS headers on /__mdp/* endpoints
# ---------------------------------------------------------------------------
echo "[11] CORS headers..."
CORS_ORIGIN=$(curl -ks -H "Origin: http://example.com" -I "${PROXY}/__mdp/health" 2>/dev/null | grep -i 'access-control-allow-origin' | tr -d '\r' | awk '{print $2}')
if [ -n "$CORS_ORIGIN" ]; then
    pass "CORS origin header present: $CORS_ORIGIN"
else
    fail "no CORS origin header on /__mdp/health"
fi

# ---------------------------------------------------------------------------
# 12. Large payload registration (name with special characters)
# ---------------------------------------------------------------------------
echo "[12] Special character handling in server names..."
curl -sX POST "${CTRL}/__mdp/register" \
  -H 'Content-Type: application/json' \
  -d '{"name":"edge/name-with-dashes_and_underscores.v2","port":50001,"proxyPort":3000,"group":"edge"}' \
  -o /dev/null
STATUS=$(curl -sX DELETE "${CTRL}/__mdp/register/edge%2Fname-with-dashes_and_underscores.v2" -w '%{http_code}' -o /dev/null)
if [ "$STATUS" = "200" ]; then
    pass "special character names handled correctly"
else
    fail "deregister of special name returned $STATUS"
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
echo "=========================================="
if [ "$FAILURES" -eq 0 ]; then
    printf "  \033[1;32mAll stress tests passed!\033[0m\n"
else
    printf "  \033[1;31m%d test(s) failed\033[0m\n" "$FAILURES"
fi
echo "=========================================="
echo ""

exit "$FAILURES"
