#!/bin/bash

set -euo pipefail

kcontext_folder="$HOME/k8sconfig"
SCRIPT_TMPDIR="$HOME/chandler/tmp"
LOCAL_BIN="$HOME/.local/bin"
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  aarch64|arm64) ARCH="arm64" ;;
  x86_64)        ARCH="x86_64" ;;
  i686|i386)     ARCH="386" ;;
esac

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

mkdir -p "$kcontext_folder" "$SCRIPT_TMPDIR"

if ! command_exists kconf; then
  echo "kconf is not installed. Downloading..."
  DOWNLOAD_URL=$(curl -fsSL \
    "https://api.github.com/repos/particledecay/kconf/releases?per_page=5" \
    | jq -r --arg os "$OS" --arg arch "$ARCH" '
        .[].assets[]
        | select(.name | test("^kconf-" + $os + "-" + $arch + ".*\\.tar\\.gz$"))
        | .browser_download_url' \
    | head -n 1)
  if [[ -z "$DOWNLOAD_URL" ]]; then
    echo "No kconf release asset found for ${OS}/${ARCH}."
    exit 1
  fi
  echo "Downloading: $DOWNLOAD_URL"
  curl --fail -L -o "$SCRIPT_TMPDIR/kconf.tar.gz" "$DOWNLOAD_URL"
  EXTRACT_DIR=$(mktemp -d)
  tar -xf "$SCRIPT_TMPDIR/kconf.tar.gz" -C "$EXTRACT_DIR"
  mv "$EXTRACT_DIR/kconf" "$LOCAL_BIN/"
  chmod +x "$LOCAL_BIN/kconf"
  rm -rf "$EXTRACT_DIR"
else
  echo "kconf is already installed."
fi

remove_all_contexts() {
  echo "Removing all existing contexts..."
  kconf list 2>/dev/null | while read -r raw; do
    local context
    context="${raw#\*}"
    context="${context#"${context%%[![:space:]]*}"}"
    context="${context%"${context##*[![:space:]]}"}"
    [[ -z "$context" ]] && continue
    if kconf rm "$context" >/dev/null 2>&1; then
      echo "Removed context: $context"
    else
      echo "Failed to remove context: $context"
    fi
  done
}

remove_all_contexts

shopt -s nullglob
added=0
failed=0
for kcontext_file in "$kcontext_folder"/*.yaml; do
  if [[ -f "$kcontext_file" ]]; then
    filename=$(basename "$kcontext_file" .yaml)
    if kconf add "$kcontext_file" --context-name="$filename" >/dev/null 2>&1; then
      echo "Added $kcontext_file with context name $filename"
      ((added++))
    else
      echo "Failed to add $kcontext_file"
      ((failed++))
    fi
  fi
done
shopt -u nullglob

rm -rf "${SCRIPT_TMPDIR:?}/"*
echo "Done. Contexts added: $added, failed: $failed."
