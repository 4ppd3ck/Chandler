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

# A version may be passed as the first argument — `chandler tool terraform --
# 1.9.0` forwards it — so the install runs unattended; without one, ask
# like the bash original did.
USER_VERSION="${1:-}"
if [[ -z "$USER_VERSION" ]]; then
  echo "Enter the terraform version you want to download (press Enter for the latest version):"
  read -r USER_VERSION
fi

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

# HashiCorp publishes SHA256SUMS for every release — fetch this
# platform's line and verify the zip before it is extracted.
SUMS_URL="https://releases.hashicorp.com/terraform/$VERSION/terraform_${VERSION}_SHA256SUMS"
ZIP_NAME="terraform_${VERSION}_${OS}_${ARCH}.zip"
EXPECTED=$(curl -fsSL "$SUMS_URL" 2>/dev/null | awk -v name="$ZIP_NAME" '$2 == name { print $1 }')
if [[ -z "$EXPECTED" ]]; then
  echo "ERROR: could not fetch the official terraform checksum for $ZIP_NAME from $SUMS_URL — refusing to install an unverified archive." >&2
  exit 1
fi
verify_sha256 "$SCRIPT_TMPDIR/terraform_$VERSION.zip" "$EXPECTED" "terraform $VERSION ($OS/$ARCH)"

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
