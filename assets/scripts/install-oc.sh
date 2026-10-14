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

MIRROR="https://mirror.openshift.com/pub/openshift-v4/clients/oc"


case "$OS" in
  linux)
    case "$ARCH" in
      x86_64)              OC_URL="$MIRROR/latest/linux/oc.tar.gz" ;;
      aarch64|arm64)       OC_URL="$MIRROR/latest/linux-arm64/oc.tar.gz" ;;
      *) echo "Unsupported architecture for oc: $ARCH"; exit 1 ;;
    esac
    ;;
  darwin)
    OC_URL="$MIRROR/latest/macosx/oc.tar.gz"
    ;;
  windows)
    OC_URL="$MIRROR/latest/windows/oc.zip"
    ;;
  *)
    echo "Unsupported OS: this installer handles Windows/macOS/Linux (got '$OS')."
    exit 1
    ;;
esac

if ! curl -fsIL -o /dev/null --max-time 20 "$OC_URL"; then
  echo "No oc tarball found at: $OC_URL"
  echo "ARM note: the mirror's 'latest' stream has no ARM build. Download a"
  echo "versioned tarball instead, e.g.:"
  echo "  https://mirror.openshift.com/pub/openshift-v4/clients/oc/<version>/linux/arm64/oc.tar.gz"
  exit 1
fi

echo "Downloading oc client from: $OC_URL"

BIN_EXT=""
if [[ "$OS" == "windows" ]]; then
  BIN_EXT=".exe"
  curl --fail -L -o "$SCRIPT_TMPDIR/oc.zip" "$OC_URL"

  echo "Extracting oc..."
  EXTRACT_DIR=$(mktemp -d)
  if command -v unzip >/dev/null 2>&1; then
    unzip -q "$SCRIPT_TMPDIR/oc.zip" -d "$EXTRACT_DIR"
  else
    "${SYSTEMROOT:-C:\\Windows}\\System32\\tar.exe" -xf "$SCRIPT_TMPDIR/oc.zip" -C "$EXTRACT_DIR"
  fi

  cp "$EXTRACT_DIR/oc$BIN_EXT" "$LOCAL_BIN/oc$BIN_EXT"
  chmod +x "$LOCAL_BIN/oc$BIN_EXT"

  rm -rf "$EXTRACT_DIR"
  rm -rf "${SCRIPT_TMPDIR:?}/"*

  echo "oc is now installed..."
  "$LOCAL_BIN/oc" version --client 2>/dev/null || true
  exit 0
fi

curl --fail -L -o "$SCRIPT_TMPDIR/oc.tar.gz" "$OC_URL"

echo "Extracting oc..."
EXTRACT_DIR=$(mktemp -d)
tar -xzf "$SCRIPT_TMPDIR/oc.tar.gz" -C "$EXTRACT_DIR"

cp "$EXTRACT_DIR/oc" "$LOCAL_BIN/oc"
chmod +x "$LOCAL_BIN/oc"

rm -rf "$EXTRACT_DIR"
rm -rf "${SCRIPT_TMPDIR:?}/"*

echo "oc is now installed..."
"$LOCAL_BIN/oc" version --client 2>/dev/null || true
