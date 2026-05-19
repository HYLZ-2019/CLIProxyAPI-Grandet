#!/usr/bin/env bash
set -euo pipefail

HOST=${HOST:-127.0.0.1}
PORT=${PORT:-5174}
ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
WEB_DIR="$ROOT/web"
NODE20="$HOME/.nvm/versions/node/v20.20.2/bin"

if [[ ! -d "$WEB_DIR" ]]; then
  echo "web directory not found: $WEB_DIR" >&2
  exit 1
fi

if [[ -d "$NODE20" ]]; then
  export PATH="$NODE20:$PATH"
fi

if ! command -v npm >/dev/null 2>&1; then
  echo "npm not found. Please install/use Node.js first." >&2
  exit 1
fi

stop_port() {
  local port=$1
  local pids=""

  if command -v lsof >/dev/null 2>&1; then
    pids=$(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true)
  elif command -v fuser >/dev/null 2>&1; then
    pids=$(fuser "${port}/tcp" 2>/dev/null || true)
  fi

  if [[ -n "$pids" ]]; then
    echo "Stopping existing frontend process on $HOST:$port: $pids"
    kill $pids 2>/dev/null || true
    sleep 1
    kill -9 $pids 2>/dev/null || true
  fi
}

stop_port "$PORT"

echo "Starting frontend at http://$HOST:$PORT/"
exec npm --prefix "$WEB_DIR" run dev -- --host "$HOST" --port "$PORT"
