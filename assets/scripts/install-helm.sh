#!/bin/bash

set -euo pipefail

LOCAL_BIN="$HOME/.local/bin"
# SCRIPT_DIR/SCRIPT_TMPDIR come from the Go menu environment when set (the
# materialized embedded assets); these defaults self-locate the checkout —
# the script's own dir and, two levels up, the repo tmp/ — so direct
# standalone runs from assets/scripts/ work too.
SCRIPT_DIR="${SCRIPT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd -P)}"
SCRIPT_TMPDIR="${SCRIPT_TMPDIR:-$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/../.." && pwd -P)/tmp}"
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  mingw*|msys*|cygwin*) OS="windows" ;;
esac
ARCH=$(uname -m)

mkdir -p "$SCRIPT_TMPDIR" "$SCRIPT_DIR"

# .exe suffix for the windows binaries; empty on the unixes.
BIN_EXT=""
if [[ "$OS" == "windows" ]]; then
  BIN_EXT=".exe"
fi

get_latest_version() {
  VERSION=$(curl -fsSL https://api.github.com/repos/helm/helm/releases/latest | jq -r .tag_name | sed 's/^v//')
}

get_last_5_versions() {
  VERSIONS=$(curl -fsSL https://api.github.com/repos/helm/helm/releases \
    | jq -r 'map(select(.prerelease == false))
         | map(.tag_name | ltrimstr("v"))
         | sort_by(split(".") | map(tonumber? // 0))
         | reverse
         | .[0:5]
         | .[]' \
    | sed 's/^v//')

  echo "The last 5 stable helm versions are:"
  echo "$VERSIONS"
}
get_last_5_versions

echo "Enter the helm version you want to download (press Enter for the latest version):"
read -r USER_VERSION

if [[ -z "$USER_VERSION" ]]; then
  echo "No version specified, fetching the latest stable version..."
  get_latest_version
else
  VERSION="$USER_VERSION"
  echo "Downloading helm version $VERSION..."
fi

if [[ "$ARCH" == "x86_64" ]]; then
  ARCH="amd64"
elif [[ "$ARCH" == "aarch64" || "$ARCH" == "arm64" ]]; then
  ARCH="arm64"
else
  echo "Unsupported architecture: $ARCH"
  exit 1
fi

DOWNLOAD_URL="https://get.helm.sh/helm-v$VERSION-$OS-$ARCH.tar.gz"
echo "Downloading helm from: $DOWNLOAD_URL"

curl --fail -L -o "$SCRIPT_TMPDIR/helm.tar.gz" "$DOWNLOAD_URL"

# Extract Helm. Extract to a /tmp dir (mktemp -d) rather than $SCRIPT_TMPDIR:
# on some filesystems (9p/virtiofs/shared-folder mounts in VMs) tar's file
# creation fails with "Function not implemented"; /tmp (ext4/tmpfs) works.
echo "Extracting helm..."
EXTRACT_DIR="$(mktemp -d)"
tar -xzf "$SCRIPT_TMPDIR/helm.tar.gz" -C "$EXTRACT_DIR"
chmod +x "$EXTRACT_DIR/$OS-$ARCH/helm$BIN_EXT"

echo "Helm version $VERSION has been downloaded"

cp "$EXTRACT_DIR/$OS-$ARCH/helm$BIN_EXT" "$LOCAL_BIN/"

rm -rf "$EXTRACT_DIR" "${SCRIPT_TMPDIR:?}/"*
echo "Helm is now installed..."
