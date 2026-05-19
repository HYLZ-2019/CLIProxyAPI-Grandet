#!/usr/bin/env bash
set -euo pipefail

DEFAULT_REMOTE="ubuntu@proxyapi"
DEFAULT_INSTALL_DIR="/home/ubuntu/CLIProxyAPI-Grandet"
DEFAULT_SERVICE_NAME="cliproxy-grandet"
PACKAGE_NAME="CLIProxyAPI-Grandet-linux-arm64"
NODE20="$HOME/.nvm/versions/node/v20.20.2/bin"

usage() {
  cat <<EOF
Usage:
  ./scripts/install-grandet-update.sh [remote] [install_dir] [service_name]

Defaults:
  remote       $DEFAULT_REMOTE
  install_dir  $DEFAULT_INSTALL_DIR
  service_name $DEFAULT_SERVICE_NAME

What local mode does:
  1. Build web/dist/index.html locally.
  2. Cross-compile ./cmd/server to linux/arm64 locally.
  3. Package CLIProxyAPI + static/management.html.
  4. scp the package and this script to the remote machine.
  5. Run the remote install mode with sudo, replacing only:
     - CLIProxyAPI
     - static/management.html

Remote runtime data preserved:
  - config.yaml
  - auths/
  - logs/
  - data/
  - any other runtime files not named above

Examples:
  ./scripts/install-grandet-update.sh
  ./scripts/install-grandet-update.sh ubuntu@proxyapi /home/ubuntu/CLIProxyAPI-Grandet cliproxy-grandet

Internal remote install mode:
  sudo ./install-grandet-update.sh --remote-install /tmp/CLIProxyAPI-Grandet-linux-arm64.tar.gz [install_dir] [service_name]
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

remote_install() {
  if [[ $# -lt 1 || $# -gt 3 ]]; then
    usage >&2
    exit 2
  fi

  local package_path=$1
  local install_dir=${2:-$DEFAULT_INSTALL_DIR}
  local service_name=${3:-$DEFAULT_SERVICE_NAME}

  if [[ $EUID -ne 0 ]]; then
    echo "Remote install mode must run with sudo." >&2
    exit 1
  fi
  if [[ ! -f "$package_path" ]]; then
    echo "Package not found: $package_path" >&2
    exit 1
  fi
  if [[ ! -d "$install_dir" ]]; then
    echo "Install directory not found: $install_dir" >&2
    exit 1
  fi

  require_cmd tar
  require_cmd find
  require_cmd install
  require_cmd systemctl

  local tmp_dir
  tmp_dir=$(mktemp -d)
  local backup_dir="$install_dir/backups/$(date +%Y%m%d-%H%M%S)"
  cleanup_remote() {
    rm -rf "$tmp_dir"
  }
  trap cleanup_remote EXIT

  tar -xzf "$package_path" -C "$tmp_dir"

  local new_binary
  new_binary=$(find "$tmp_dir" -type f -name CLIProxyAPI -perm /111 | head -n 1 || true)
  if [[ -z "$new_binary" ]]; then
    new_binary=$(find "$tmp_dir" -type f -name CLIProxyAPI | head -n 1 || true)
  fi
  if [[ -z "$new_binary" ]]; then
    echo "Package does not contain a CLIProxyAPI binary." >&2
    exit 1
  fi

  if command -v file >/dev/null 2>&1 && ! file "$new_binary" | grep -qi 'aarch64\|ARM aarch64\|ARM64'; then
    echo "Warning: binary does not look like linux arm64/aarch64:" >&2
    file "$new_binary" >&2 || true
  fi

  local new_panel
  new_panel=$(find "$tmp_dir" -type f -path '*/static/management.html' | head -n 1 || true)

  mkdir -p "$backup_dir"
  if [[ -f "$install_dir/CLIProxyAPI" ]]; then
    cp -a "$install_dir/CLIProxyAPI" "$backup_dir/CLIProxyAPI"
  fi
  if [[ -f "$install_dir/static/management.html" ]]; then
    mkdir -p "$backup_dir/static"
    cp -a "$install_dir/static/management.html" "$backup_dir/static/management.html"
  fi

  systemctl stop "$service_name" || true

  install -m 0755 "$new_binary" "$install_dir/CLIProxyAPI"
  if [[ -n "$new_panel" ]]; then
    mkdir -p "$install_dir/static"
    install -m 0644 "$new_panel" "$install_dir/static/management.html"
  fi

  chown --reference="$install_dir" "$install_dir/CLIProxyAPI" 2>/dev/null || true
  if [[ -f "$install_dir/static/management.html" ]]; then
    chown --reference="$install_dir" "$install_dir/static/management.html" 2>/dev/null || true
  fi

  systemctl daemon-reload
  systemctl start "$service_name"
  systemctl --no-pager --lines=30 status "$service_name"

  echo
  echo "Updated $service_name in $install_dir"
  echo "Backup saved at $backup_dir"
  echo "Preserved runtime data: config.yaml, auths/, logs/, data/"
}

local_build_and_deploy() {
  if [[ $# -gt 3 ]]; then
    usage >&2
    exit 2
  fi

  if [[ $EUID -eq 0 ]]; then
    echo "Do not run local build/deploy mode with sudo. The script uses sudo only on the remote machine." >&2
    exit 1
  fi

  local remote=${1:-$DEFAULT_REMOTE}
  local install_dir=${2:-$DEFAULT_INSTALL_DIR}
  local service_name=${3:-$DEFAULT_SERVICE_NAME}

  if [[ -d "$NODE20" ]]; then
    export PATH="$NODE20:$PATH"
  fi

  require_cmd npm
  require_cmd scp
  require_cmd ssh
  require_cmd tar
  require_cmd sha256sum

  if ! command -v go >/dev/null 2>&1 && [[ -x /tmp/cliproxy-go/go/bin/go ]]; then
    export PATH="/tmp/cliproxy-go/go/bin:$PATH"
  fi
  require_cmd go

  local repo_root
  repo_root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
  local stamp
  stamp=$(date +%Y%m%d-%H%M%S)
  local build_dir="/tmp/$PACKAGE_NAME-$stamp"
  local package_path="/tmp/$PACKAGE_NAME-$stamp.tar.gz"
  local checksum_path="$package_path.sha256"
  local remote_package="/tmp/$PACKAGE_NAME-$stamp.tar.gz"
  local remote_script="/tmp/install-grandet-update-$stamp.sh"

  rm -rf "$build_dir" "$package_path" "$checksum_path"
  mkdir -p "$build_dir/static"

  echo "Building management panel ..."
  (cd "$repo_root" && npm --prefix web run build)

  echo "Building linux/arm64 backend ..."
  (cd "$repo_root" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o "$build_dir/CLIProxyAPI" ./cmd/server)

  cp "$repo_root/web/dist/index.html" "$build_dir/static/management.html"
  if [[ -f "$repo_root/config.example.yaml" ]]; then
    cp "$repo_root/config.example.yaml" "$build_dir/config.example.yaml"
  fi

  echo "Packaging $package_path ..."
  tar -C /tmp -czf "$package_path" "$(basename "$build_dir")"
  sha256sum "$package_path" > "$checksum_path"
  cat "$checksum_path"

  echo "Uploading package and installer to $remote ..."
  scp "$package_path" "$remote:$remote_package"
  scp "$0" "$remote:$remote_script"

  echo "Running remote installer on $remote ..."
  ssh -t "$remote" "chmod +x '$remote_script' && sudo '$remote_script' --remote-install '$remote_package' '$install_dir' '$service_name'"

  echo
  echo "Done. Local package kept at: $package_path"
  echo "Checksum: $checksum_path"
}

if [[ "${1:-}" == "--remote-install" ]]; then
  shift
  remote_install "$@"
else
  local_build_and_deploy "$@"
fi
