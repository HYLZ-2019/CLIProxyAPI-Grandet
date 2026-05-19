#!/usr/bin/env bash
set -euo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
HOST=${HOST:-127.0.0.1}
BACKEND_PORT=${BACKEND_PORT:-8318}
FRONTEND_PORT=${FRONTEND_PORT:-5174}
CONFIG=${CONFIG:-/tmp/cliproxy-local-server-8318-20260517.yaml}
LOG_DIR=${LOG_DIR:-/tmp/cliproxy-grandet-local}
NODE20="$HOME/.nvm/versions/node/v20.20.2/bin"
GO_FALLBACK="/tmp/cliproxy-go/go/bin"

mkdir -p "$LOG_DIR"

if [[ -d "$NODE20" ]]; then
  export PATH="$NODE20:$PATH"
fi
if ! command -v go >/dev/null 2>&1 && [[ -x "$GO_FALLBACK/go" ]]; then
  export PATH="$GO_FALLBACK:$PATH"
fi

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

stop_port() {
  local port=$1
  local label=$2
  local pids=""

  if command -v lsof >/dev/null 2>&1; then
    pids=$(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true)
  elif command -v fuser >/dev/null 2>&1; then
    pids=$(fuser "${port}/tcp" 2>/dev/null || true)
  fi

  if [[ -n "$pids" ]]; then
    echo "Stopping $label on $HOST:$port: $pids"
    kill $pids 2>/dev/null || true
    sleep 1
    kill -9 $pids 2>/dev/null || true
  fi
}

stop_pattern() {
  local pattern=$1
  local label=$2
  local pids=""
  pids=$(pgrep -f "$pattern" 2>/dev/null || true)
  if [[ -n "$pids" ]]; then
    echo "Stopping $label: $pids"
    kill $pids 2>/dev/null || true
    sleep 1
    kill -9 $pids 2>/dev/null || true
  fi
}

if [[ ! -f "$CONFIG" ]]; then
  echo "Backend config not found: $CONFIG" >&2
  echo "Override with: CONFIG=/path/to/config.yaml $0" >&2
  exit 1
fi
if [[ ! -d "$ROOT/web" ]]; then
  echo "web directory not found under repo root: $ROOT" >&2
  exit 1
fi

require_cmd go
require_cmd npm

stop_port "$FRONTEND_PORT" "frontend"
stop_port "$BACKEND_PORT" "backend"
stop_pattern "go run ./cmd/server -config $CONFIG" "go-run backend wrapper"

BACKEND_LOG="$LOG_DIR/backend-$BACKEND_PORT.log"
FRONTEND_LOG="$LOG_DIR/frontend-$FRONTEND_PORT.log"

echo "Starting backend at http://$HOST:$BACKEND_PORT/"
(
  cd "$ROOT"
  exec go run ./cmd/server -config "$CONFIG" -local-model
) >"$BACKEND_LOG" 2>&1 &
BACKEND_PID=$!

echo "Starting frontend at http://$HOST:$FRONTEND_PORT/"
(
  cd "$ROOT"
  exec npm --prefix web run dev -- --host "$HOST" --port "$FRONTEND_PORT"
) >"$FRONTEND_LOG" 2>&1 &
FRONTEND_PID=$!

sleep 2

if ! kill -0 "$BACKEND_PID" 2>/dev/null; then
  echo "Backend failed to start. Log:" >&2
  tail -80 "$BACKEND_LOG" >&2 || true
  exit 1
fi
if ! kill -0 "$FRONTEND_PID" 2>/dev/null; then
  echo "Frontend failed to start. Log:" >&2
  tail -80 "$FRONTEND_LOG" >&2 || true
  exit 1
fi

echo
echo "Local dev restarted."
echo "Backend:  http://$HOST:$BACKEND_PORT/  pid=$BACKEND_PID  log=$BACKEND_LOG"
echo "Frontend: http://$HOST:$FRONTEND_PORT/  pid=$FRONTEND_PID  log=$FRONTEND_LOG"
echo
echo "Tail logs:"
echo "  tail -f '$BACKEND_LOG'"
echo "  tail -f '$FRONTEND_LOG'"
