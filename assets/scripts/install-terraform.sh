#!/bin/bash

set -euo pipefail

LOCAL_BIN="$HOME/.local/bin"
TERRAFORM_VERSION_DOWNLOAD="$HOME/.local/bin/terraform-versions"
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

mkdir -p "$SCRIPT_TMPDIR" "$SCRIPT_DIR" "$TERRAFORM_VERSION_DOWNLOAD"

BIN_EXT=""
if [[ "$OS" == "windows" ]]; then
  BIN_EXT=".exe"
fi

extract_zip() {
  if command -v unzip >/dev/null 2>&1; then
    unzip -q "$1" -d "$2"
  else
    "${SYSTEMROOT:-C:\\Windows}\\System32\\tar.exe" -xf "$1" -C "$2"
  fi
}

get_latest_version() {
  VERSION=$(curl -fsSL https://api.github.com/repos/hashicorp/terraform/releases/latest | jq -r .tag_name | sed 's/^v//')
}

get_last_5_stable_versions() {
  VERSIONS=$(curl -fsSL https://api.github.com/repos/hashicorp/terraform/releases \
    | jq -r 'map(select(.prerelease == false))
         | map(.tag_name | ltrimstr("v"))
         | sort_by(split(".") | map(tonumber? // 0))
         | reverse
         | .[0:5]
         | .[]' \
    | sed 's/^v//')

  echo "The last 5 stable terraform versions are:"
  echo "$VERSIONS"
}
get_last_5_stable_versions

echo "Enter the terraform version you want to download (press Enter for the latest version):"
read -r USER_VERSION

if [[ -z "$USER_VERSION" ]]; then
  echo "No version specified, fetching the latest stable version..."
  get_latest_version
else
  VERSION="$USER_VERSION"
  echo "Downloading terraform version $VERSION..."
fi

if [[ "$ARCH" == "x86_64" ]]; then
  ARCH="amd64"
elif [[ "$ARCH" == "aarch64" || "$ARCH" == "arm64" ]]; then
  ARCH="arm64"
else
  echo "Unsupported architecture: $ARCH"
  exit 1
fi

DOWNLOAD_URL="https://releases.hashicorp.com/terraform/$VERSION/terraform_${VERSION}_${OS}_${ARCH}.zip"
echo "Downloading terraform from: $DOWNLOAD_URL"

curl --fail -L -o "$SCRIPT_TMPDIR/terraform_$VERSION.zip" "$DOWNLOAD_URL"

echo "Extracting terraform_$VERSION.zip..."
extract_zip "$SCRIPT_TMPDIR/terraform_$VERSION.zip" "$SCRIPT_TMPDIR"

echo "Copying terraform binary to $TERRAFORM_VERSION_DOWNLOAD..."
cp "$SCRIPT_TMPDIR/terraform$BIN_EXT" "$TERRAFORM_VERSION_DOWNLOAD/terraform-$VERSION$BIN_EXT"
chmod +x "$TERRAFORM_VERSION_DOWNLOAD/terraform-$VERSION$BIN_EXT"

if [[ "$OS" == "windows" ]]; then
  cp -f "$TERRAFORM_VERSION_DOWNLOAD/terraform-$VERSION$BIN_EXT" "$LOCAL_BIN/terraform.exe"
else
  ln -sf "$TERRAFORM_VERSION_DOWNLOAD/terraform-$VERSION" "$LOCAL_BIN/terraform"
fi

rm -rf "${SCRIPT_TMPDIR:?}/"*
echo "Terraform is now installed..."
