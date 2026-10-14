#!/bin/bash

set -euo pipefail

LOCAL_BIN="$HOME/.local/bin"
KUBECTL_VERSION_DOWNLOAD="$HOME/.local/bin/kubectl-versions"
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

mkdir -p "$SCRIPT_TMPDIR" "$SCRIPT_DIR" "$KUBECTL_VERSION_DOWNLOAD"

# .exe suffix for the windows binaries; empty on the unixes.
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

get_latest_version() {
  VERSION=$(curl -fsSL https://api.github.com/repos/kubernetes/kubernetes/releases/latest | jq -r .tag_name | sed 's/^v//')
}

get_last_5_versions() {
  VERSIONS=$(curl -fsSL https://api.github.com/repos/kubernetes/kubernetes/releases \
    | jq -r 'map(select(.prerelease == false))
         | map(.tag_name | ltrimstr("v"))
         | sort_by(split(".") | map(tonumber? // 0))
         | reverse
         | .[0:5]
         | .[]' \
    | sed 's/^v//')

  echo "The last 5 stable kubectl versions are:"
  echo "$VERSIONS"
}
get_last_5_versions

echo "Enter the kubectl version you want to download (press Enter for the latest version):"
read -r USER_VERSION

if [[ -z "$USER_VERSION" ]]; then
  echo "No version specified, fetching the latest stable version..."
  get_latest_version
else
  VERSION="$USER_VERSION"
  echo "Downloading kubectl version $VERSION..."
fi

if [[ "$OS" == "windows" ]]; then
  DOWNLOAD_URL="https://dl.k8s.io/release/v$VERSION/bin/windows/$ARCH/kubectl.exe"
else
  DOWNLOAD_URL="https://dl.k8s.io/release/v$VERSION/bin/$OS/$ARCH/kubectl"
fi
echo "Downloading kubectl from: $DOWNLOAD_URL"

curl --fail -L -o "$KUBECTL_VERSION_DOWNLOAD/kubectl-$VERSION$BIN_EXT" "$DOWNLOAD_URL"
chmod +x "$KUBECTL_VERSION_DOWNLOAD/kubectl-$VERSION$BIN_EXT"

if [[ "$OS" == "windows" ]]; then
  cp -f "$KUBECTL_VERSION_DOWNLOAD/kubectl-$VERSION$BIN_EXT" "$LOCAL_BIN/kubectl.exe"
else
  ln -sf "$KUBECTL_VERSION_DOWNLOAD/kubectl-$VERSION" "$LOCAL_BIN/kubectl"
fi
echo "kubectl version $VERSION has been downloaded to: $KUBECTL_VERSION_DOWNLOAD"

if ! command -v kubectx >/dev/null 2>&1 || ! command -v kubens >/dev/null 2>&1; then
  if [[ "$OS" == "darwin" ]]; then
    if command -v brew >/dev/null 2>&1; then
      echo "Installing kubectx + kubens via Homebrew..."
      brew install kubectx
    else
      echo "Skipping kubectx/kubens: on macOS they need Homebrew (brew install kubectx)."
    fi
  else
    if ! command -v kubens >/dev/null 2>&1; then
      echo "Downloading kubens binary..."
      curl -fL "https://github.com/ahmetb/kubectx/releases/download/v0.9.5/kubens" -o "$SCRIPT_TMPDIR/kubens"
      chmod +x "$SCRIPT_TMPDIR/kubens"
      mv "$SCRIPT_TMPDIR/kubens" "$LOCAL_BIN/"
    fi
    if ! command -v kubectx >/dev/null 2>&1; then
      echo "Downloading kubectx binary..."
      curl -fL "https://github.com/ahmetb/kubectx/releases/download/v0.9.5/kubectx" -o "$SCRIPT_TMPDIR/kubectx"
      chmod +x "$SCRIPT_TMPDIR/kubectx"
      mv "$SCRIPT_TMPDIR/kubectx" "$LOCAL_BIN/"
    fi
  fi
fi

rm -rf "${SCRIPT_TMPDIR:?}/"*
echo "Kubectl, Kubectx, Kubens are now installed..."
