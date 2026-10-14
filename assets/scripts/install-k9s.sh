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

extract_zip() {
  if command -v unzip >/dev/null 2>&1; then
    unzip -q "$1" -d "$2"
  else
    "${SYSTEMROOT:-C:\\Windows}\\System32\\tar.exe" -xf "$1" -C "$2"
  fi
}

BIN_EXT=""
if [[ "$OS" == "windows" ]]; then
  BIN_EXT=".exe"
fi

if [[ "$ARCH" == "x86_64" ]]; then
  ARCH="amd64"
elif [[ "$ARCH" == "aarch64" || "$ARCH" == "arm64" ]]; then
  ARCH="arm64"
else
  echo "Unsupported architecture: $ARCH"
  exit 1
fi

get_k9s_latest_version() {
  K9S_VERSION=$(curl -fsSL https://api.github.com/repos/derailed/k9s/releases/latest | jq -r .tag_name | sed 's/^v//')
}

echo "Enter the k9s version you want to download (press Enter for the latest version):"
read -r USER_VERSION

if [[ -z "$USER_VERSION" ]]; then
  echo "No version specified, fetching the latest stable version..."
  get_k9s_latest_version
else
  K9S_VERSION="$USER_VERSION"
  echo "Downloading k9s version $K9S_VERSION..."
fi
echo "Latest k9s version: $K9S_VERSION"

case "$OS" in
  linux)   K9S_OS="Linux" ;;
  darwin)  K9S_OS="Darwin" ;;
  windows) K9S_OS="Windows" ;;
  *)       K9S_OS="$OS" ;;
esac
if [[ "$OS" == "windows" ]]; then
  DOWNLOAD_URL="https://github.com/derailed/k9s/releases/download/v$K9S_VERSION/k9s_${K9S_OS}_${ARCH}.zip"
  ARCHIVE="k9s-$K9S_VERSION.zip"
else
  DOWNLOAD_URL="https://github.com/derailed/k9s/releases/download/v$K9S_VERSION/k9s_${K9S_OS}_${ARCH}.tar.gz"
  ARCHIVE="k9s-$K9S_VERSION.tar.gz"
fi
echo "Downloading k9s from: $DOWNLOAD_URL"
curl --fail -L -o "$SCRIPT_TMPDIR/$ARCHIVE" "$DOWNLOAD_URL"

echo "Extracting $ARCHIVE"
EXTRACT_DIR="$(mktemp -d)"
if [[ "$OS" == "windows" ]]; then
  extract_zip "$SCRIPT_TMPDIR/$ARCHIVE" "$EXTRACT_DIR"
else
  tar -xzf "$SCRIPT_TMPDIR/$ARCHIVE" -C "$EXTRACT_DIR"
fi
chmod +x "$EXTRACT_DIR/k9s$BIN_EXT"

mv "$EXTRACT_DIR/k9s$BIN_EXT" "$LOCAL_BIN/"

rm -rf "$EXTRACT_DIR" "${SCRIPT_TMPDIR:?}/"*
echo "K9S is now installed..."
