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

# A version may be passed as the first argument — `chandler tool helm --
# 3.16.0` forwards it — so the install runs unattended; without one, ask
# like the bash original did.
USER_VERSION="${1:-}"
if [[ -z "$USER_VERSION" ]]; then
  echo "Enter the helm version you want to download (press Enter for the latest version):"
  read -r USER_VERSION
fi

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

DOWNLOAD_URL="https://get.helm.sh/helm-v$VERSION-$OS-$ARCH.tar.gz"
echo "Downloading helm from: $DOWNLOAD_URL"

curl --fail -L -o "$SCRIPT_TMPDIR/helm.tar.gz" "$DOWNLOAD_URL"

# get.helm.sh publishes a .sha256sum file beside every artifact —
# fetch it and verify the archive before it is extracted.
SUMS_URL="https://get.helm.sh/helm-v$VERSION-$OS-$ARCH.tar.gz.sha256sum"
EXPECTED=$(curl -fsSL "$SUMS_URL" 2>/dev/null | awk '{print $1}')
if [[ -z "$EXPECTED" ]]; then
  echo "ERROR: could not fetch the official helm checksum from $SUMS_URL — refusing to install an unverified archive." >&2
  exit 1
fi
verify_sha256 "$SCRIPT_TMPDIR/helm.tar.gz" "$EXPECTED" "helm $VERSION ($OS/$ARCH)"

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
