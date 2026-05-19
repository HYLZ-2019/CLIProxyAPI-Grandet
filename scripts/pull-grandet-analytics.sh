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

Before overwriting local files, it backs up existing local analytics.db / -wal / -shm to:
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

if [[ -e "$LOCAL_DB" || -e "$LOCAL_DB-wal" || -e "$LOCAL_DB-shm" ]]; then
  mkdir -p "$LOCAL_BACKUP_DIR"
  for suffix in "" "-wal" "-shm"; do
    if [[ -e "$LOCAL_DB$suffix" ]]; then
      cp -a "$LOCAL_DB$suffix" "$LOCAL_BACKUP_DIR/"
    fi
  done
  echo "Backed up local analytics DB files to $LOCAL_BACKUP_DIR"
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
"

echo "Pulling snapshot with scp ..."
scp "$REMOTE:$REMOTE_TMP/analytics.db" "$LOCAL_DB"
scp "$REMOTE:$REMOTE_TMP/analytics.db-wal" "$LOCAL_DB-wal" 2>/dev/null || rm -f "$LOCAL_DB-wal"
scp "$REMOTE:$REMOTE_TMP/analytics.db-shm" "$LOCAL_DB-shm" 2>/dev/null || rm -f "$LOCAL_DB-shm"

if command -v sqlite3 >/dev/null 2>&1; then
  sqlite3 "$LOCAL_DB" 'PRAGMA quick_check;' >/dev/null
  echo "Local SQLite quick_check passed."
else
  echo "sqlite3 not found locally; skipped quick_check."
fi

echo "Pulled analytics data to $LOCAL_DB"
echo "If a local backend was already running, restart it so SQLite reopens this DB."
