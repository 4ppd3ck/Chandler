#!/bin/bash

set -euo pipefail

# SCRIPT_DIR/SCRIPT_TMPDIR come from the Go menu environment when set (the
# materialized embedded assets); these defaults self-locate the checkout —
# the script's own dir and, two levels up, the repo tmp/ — so direct
# standalone runs from assets/scripts/ work too.
SCRIPT_DIR="${SCRIPT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd -P)}"
SCRIPT_TMPDIR="${SCRIPT_TMPDIR:-$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/../.." && pwd -P)/tmp}"
LOCAL_BIN="$HOME/.local/bin"

mkdir -p "$SCRIPT_TMPDIR" "$SCRIPT_DIR"

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "This installer downloads a Linux AppImage and only runs on Linux — skipped (get Brave from brave.com on other systems)."
  exit 0
fi

ARCH="${1:-x86_64}"
case "$ARCH" in
  arm64) ARCH="aarch64" ;;
esac

# Resolve the newest Brave stable AppImage asset that actually exists on the
# mirror. The mirror publishes many nightly-only releases on top of the
# latest stable one, so walk the release list (newest first) until a stable
# asset for this arch shows up — the URL then comes from the asset itself
# and is guaranteed to exist.
echo "Looking up the newest Brave stable AppImage (arch: $ARCH)..."
DOWNLOAD_URL=""
for page in 1 2 3 4 5 6; do
  releases=$(curl -fsSL "https://api.github.com/repos/srevinsaju/Brave-AppImage/releases?per_page=50&page=$page") || break
  DOWNLOAD_URL=$(printf '%s' "$releases" \
    | jq -r --arg arch "$ARCH" '
        .[].assets[]
        | select(.name | test("^Brave-stable-.*-" + $arch + "\\.AppImage$"))
        | .browser_download_url' \
    | head -n 1)
  [[ -n "$DOWNLOAD_URL" ]] && break
  [[ "$(printf '%s' "$releases" | jq 'length')" -eq 0 ]] && break
done

if [[ -z "$DOWNLOAD_URL" ]]; then
  echo "No Brave stable AppImage found on the mirror (arch: $ARCH)."
  exit 1
fi
echo "Downloading: $DOWNLOAD_URL"

# Check and gracefully stop a running Brave process before replacing the binary.
# Try SIGTERM first (lets Brave save session/tabs), fall back to SIGKILL.
pid=$(pgrep -x brave || true)
if [[ -n "$pid" ]]; then
  echo "Brave is running (pid: $pid). Asking it to quit (SIGTERM)..."
  kill "$pid" 2>/dev/null || true
  for _ in {1..10}; do
    if ! pgrep -x brave >/dev/null 2>&1; then
      break
    fi
    sleep 0.5
  done
  if pgrep -x brave >/dev/null 2>&1; then
    echo "Brave did not exit gracefully. Forcing (SIGKILL)..."
    pkill -9 -x brave || true
  fi
  echo "Brave stopped."
else
  echo "Brave is not running."
fi

# Download Brave Browser as AppImage
wget -O "$SCRIPT_TMPDIR/brave.AppImage" "$DOWNLOAD_URL"
cp -p "$SCRIPT_TMPDIR/brave.AppImage" "$LOCAL_BIN/"
chmod +x "$LOCAL_BIN/brave.AppImage"

rm -rf "${SCRIPT_TMPDIR:?}/"*
echo "Brave Browser is now installed..."
