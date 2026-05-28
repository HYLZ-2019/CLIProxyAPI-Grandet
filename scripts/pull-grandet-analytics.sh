#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/pull-grandet-analytics.sh [remote] [remote_dir] [local_root]

Defaults:
  remote      ubuntu@proxyapi
  remote_dir  /home/ubuntu/CLIProxyAPI-Grandet
  local_root  running local backend config directory, git repository root, or current directory

Example:
  ./scripts/pull-grandet-analytics.sh
  ./scripts/pull-grandet-analytics.sh ubuntu@proxyapi /home/ubuntu/CLIProxyAPI-Grandet

This pulls the production Analytics SQLite DB into:
  <local_root>/data/analytics.db

It also pulls debug logs when present into:
  <local_root>/data/quota-response-debug.jsonl
  <local_root>/data/client-key-attribution-debug.jsonl

Before overwriting local files, it backs up existing local analytics.db / -wal / -shm and debug logs to:
  <local_root>/data/backups/<timestamp>/

It does not modify the remote server.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

detect_local_root() {
  local config_path=""
  config_path=$(ps -eo args | awk '
    /CLIProxyAPI|go run \.\/cmd\/server/ {
      for (i = 1; i <= NF; i++) {
        if ($i == "-config" && (i + 1) <= NF) {
          print $(i + 1)
          exit
        }
      }
    }
  ' || true)
  if [[ -n "$config_path" && -f "$config_path" ]]; then
    dirname "$config_path"
    return
  fi
  git rev-parse --show-toplevel 2>/dev/null || pwd
}

REMOTE=${1:-ubuntu@proxyapi}
REMOTE_DIR=${2:-/home/ubuntu/CLIProxyAPI-Grandet}
if [[ $# -ge 3 ]]; then
  LOCAL_ROOT=$3
else
  LOCAL_ROOT=$(detect_local_root)
fi

LOCAL_DATA_DIR="$LOCAL_ROOT/data"
LOCAL_DB="$LOCAL_DATA_DIR/analytics.db"
LOCAL_QUOTA_DEBUG_LOG="$LOCAL_DATA_DIR/quota-response-debug.jsonl"
LOCAL_CLIENT_KEY_DEBUG_LOG="$LOCAL_DATA_DIR/client-key-attribution-debug.jsonl"
STAMP=$(date +%Y%m%d-%H%M%S)
LOCAL_BACKUP_DIR="$LOCAL_DATA_DIR/backups/$STAMP"
REMOTE_TMP="/tmp/grandet-analytics-$STAMP-$$"
LOCAL_TMP=$(mktemp -d)

cleanup() {
  rm -rf "$LOCAL_TMP"
  ssh "$REMOTE" "rm -rf '$REMOTE_TMP'" >/dev/null 2>&1 || true
}
trap cleanup EXIT

mkdir -p "$LOCAL_DATA_DIR"

if [[ -e "$LOCAL_DB" || -e "$LOCAL_DB-wal" || -e "$LOCAL_DB-shm" || -e "$LOCAL_QUOTA_DEBUG_LOG" || -e "$LOCAL_CLIENT_KEY_DEBUG_LOG" ]]; then
  mkdir -p "$LOCAL_BACKUP_DIR"
  for suffix in "" "-wal" "-shm"; do
    if [[ -e "$LOCAL_DB$suffix" ]]; then
      cp -a "$LOCAL_DB$suffix" "$LOCAL_BACKUP_DIR/"
    fi
  done
  for debug_log in "$LOCAL_QUOTA_DEBUG_LOG" "$LOCAL_CLIENT_KEY_DEBUG_LOG"; do
    if [[ -e "$debug_log" ]]; then
      cp -a "$debug_log" "$LOCAL_BACKUP_DIR/"
    fi
  done
  echo "Backed up local analytics files to $LOCAL_BACKUP_DIR"
fi

echo "Creating remote analytics DB snapshot on $REMOTE:$REMOTE_DIR ..."
ssh "$REMOTE" "set -euo pipefail
  mkdir -p '$REMOTE_TMP'
  DB=\"$REMOTE_DIR/data/analytics.db\"
  if [ ! -f \"\$DB\" ]; then
    echo \"Remote analytics DB not found: \$DB\" >&2
    exit 1
  fi
  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 \"\$DB\" \".backup '$REMOTE_TMP/analytics.db'\"
  else
    cp -a \"\$DB\" '$REMOTE_TMP/analytics.db'
    [ -f \"\$DB-wal\" ] && cp -a \"\$DB-wal\" '$REMOTE_TMP/analytics.db-wal' || true
    [ -f \"\$DB-shm\" ] && cp -a \"\$DB-shm\" '$REMOTE_TMP/analytics.db-shm' || true
  fi
  QUOTA_LOG=\"$REMOTE_DIR/data/quota-response-debug.jsonl\"
  CLIENT_KEY_LOG=\"$REMOTE_DIR/data/client-key-attribution-debug.jsonl\"
  [ -f \"\$QUOTA_LOG\" ] && cp -a \"\$QUOTA_LOG\" '$REMOTE_TMP/quota-response-debug.jsonl' || true
  [ -f \"\$CLIENT_KEY_LOG\" ] && cp -a \"\$CLIENT_KEY_LOG\" '$REMOTE_TMP/client-key-attribution-debug.jsonl' || true
"

echo "Pulling snapshot with scp ..."
scp "$REMOTE:$REMOTE_TMP/analytics.db" "$LOCAL_DB"
scp "$REMOTE:$REMOTE_TMP/analytics.db-wal" "$LOCAL_DB-wal" 2>/dev/null || rm -f "$LOCAL_DB-wal"
scp "$REMOTE:$REMOTE_TMP/analytics.db-shm" "$LOCAL_DB-shm" 2>/dev/null || rm -f "$LOCAL_DB-shm"
scp "$REMOTE:$REMOTE_TMP/quota-response-debug.jsonl" "$LOCAL_QUOTA_DEBUG_LOG" 2>/dev/null || rm -f "$LOCAL_QUOTA_DEBUG_LOG"
scp "$REMOTE:$REMOTE_TMP/client-key-attribution-debug.jsonl" "$LOCAL_CLIENT_KEY_DEBUG_LOG" 2>/dev/null || rm -f "$LOCAL_CLIENT_KEY_DEBUG_LOG"

if command -v sqlite3 >/dev/null 2>&1; then
  sqlite3 "$LOCAL_DB" 'PRAGMA quick_check;' >/dev/null
  echo "Local SQLite quick_check passed."
else
  echo "sqlite3 not found locally; skipped quick_check."
fi

echo "Pulled analytics data to $LOCAL_DB"
if [[ -e "$LOCAL_QUOTA_DEBUG_LOG" ]]; then
  echo "Pulled quota response debug log to $LOCAL_QUOTA_DEBUG_LOG"
else
  echo "No remote quota response debug log found."
fi
if [[ -e "$LOCAL_CLIENT_KEY_DEBUG_LOG" ]]; then
  echo "Pulled client key attribution debug log to $LOCAL_CLIENT_KEY_DEBUG_LOG"
else
  echo "No remote client key attribution debug log found."
fi
echo "If a local backend was already running, restart it so SQLite reopens this DB."
