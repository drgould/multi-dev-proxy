#!/bin/sh
# Feel-test for interactive hook focus: a chatty service floods the terminal
# while two setup hooks prompt for input.
#
# What to expect:
#   1. "log line N" scrolls continuously.
#   2. ~7s in, prompter's "What is your name?" takes focus — the log lines
#      STOP (buffered in memory) and the prompt is replayed.
#   3. Type a name + Enter. The hook echoes a greeting; ~3s after your last
#      keystroke focus auto-releases and the buffered log lines flush.
#   4. prompter2's "Favorite color:" then takes focus the same way (it was
#      queued while prompter had focus, or stalls shortly after).
#   5. Ctrl-] detaches early at any time. Ctrl-C while focused goes to the
#      hook, not mdp — detach first if you want to stop everything.
#
# Run standalone (not alongside the main testbed — the dashboard port would
# collide). Ctrl-C when no hook has focus stops everything.

set -e

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
ROOT_DIR=$(cd "$SCRIPT_DIR/../.." && pwd)
CTRL_PORT="${MDP_CONTROL_PORT:-13190}"

cleanup() {
    echo ""
    echo "Stopping..."
    "$SCRIPT_DIR/mdp" --stop --control-port "$CTRL_PORT" 2>/dev/null
    echo "Done."
}
trap cleanup EXIT INT TERM

echo "Building mdp..."
(cd "$ROOT_DIR" && go build -o "$SCRIPT_DIR/mdp" ./cmd/mdp)

echo "Starting orchestrator on control port :$CTRL_PORT..."
"$SCRIPT_DIR/mdp" -d \
  --control-port "$CTRL_PORT" \
  --config "$SCRIPT_DIR/mdp.yaml"

if ! curl -so /dev/null "http://127.0.0.1:${CTRL_PORT}/__mdp/health" 2>/dev/null; then
  echo "ERROR: orchestrator failed to start on control port :${CTRL_PORT}"
  exit 1
fi

echo ""
echo "Starting services — watch for the prompt to take focus (~7s)..."
echo ""

# Foreground so mdp run owns the terminal (hook focus needs the TTY).
cd "$SCRIPT_DIR"
./mdp run --control-port "$CTRL_PORT"
