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

# ---- checksum verification --------------------------------------------------
# sha256_of prints a file's SHA256 digest (sha256sum on Linux/Git Bash,
# shasum -a 256 on macOS).
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# verify_sha256 <file> <expected-hex> <what>: refuses (exit 1) on a
# mismatch — nothing unverified moves into LOCAL_BIN.
verify_sha256() {
  local file="$1" expected="$2" what="$3" actual
  actual=$(sha256_of "$file")
  if [[ "$actual" != "$expected" ]]; then
    echo "ERROR: $what failed the checksum check (got $actual, want $expected) — refusing to install." >&2
    exit 1
  fi
  echo "$what: checksum verified."
}

get_k9s_latest_version() {
  K9S_VERSION=$(curl -fsSL https://api.github.com/repos/derailed/k9s/releases/latest | jq -r .tag_name | sed 's/^v//')
}

# A version may be passed as the first argument — `chandler tool k9s --
# 0.32.5` forwards it — so the install runs unattended; without one, ask
# like the bash original did.
USER_VERSION="${1:-}"
if [[ -z "$USER_VERSION" ]]; then
  echo "Enter the k9s version you want to download (press Enter for the latest version):"
  read -r USER_VERSION
fi

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
  # The release assets capitalize the OS: k9s_Freebsd_amd64.tar.gz (not
  # freebsd) — the lowercase uname string would 404.
  freebsd) K9S_OS="Freebsd" ;;
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

# The k9s release publishes checksums.sha256 covering every asset —
# fetch this arch's line and verify the archive before it is extracted.
SUMS_URL="https://github.com/derailed/k9s/releases/download/v$K9S_VERSION/checksums.sha256"
ASSET_NAME="k9s_${K9S_OS}_${ARCH}.tar.gz"
if [[ "$OS" == "windows" ]]; then
  ASSET_NAME="k9s_${K9S_OS}_${ARCH}.zip"
fi
EXPECTED=$(curl -fsSL "$SUMS_URL" 2>/dev/null | awk -v name="$ASSET_NAME" '$2 == name { print $1 }')
if [[ -z "$EXPECTED" ]]; then
  echo "ERROR: could not fetch the official k9s checksum for $ASSET_NAME from $SUMS_URL — refusing to install an unverified archive." >&2
  exit 1
fi
verify_sha256 "$SCRIPT_TMPDIR/$ARCHIVE" "$EXPECTED" "k9s $K9S_VERSION ($OS/$ARCH)"

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
