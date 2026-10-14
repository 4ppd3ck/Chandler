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
# Git Bash / MSYS2 report MINGW*/MSYS*/CYGWIN* — normalize so the
# per-OS branches below see "windows".
case "$OS" in
  mingw*|msys*|cygwin*) OS="windows" ;;
esac
ARCH=$(uname -m)

mkdir -p "$SCRIPT_TMPDIR" "$SCRIPT_DIR" "$LOCAL_BIN"

# Windows: the CLI ships as an MSI (machine-wide install — expect the UAC
# prompt). The Session Manager plugin's Windows setup is a separate GUI
# installer, so it is pointed at instead of run from here. cygpath -w
# hands msiexec a real Windows path; the doubled slashes keep MSYS from
# mangling the msiexec flags into paths.
if [[ "$OS" == "windows" ]]; then
  echo "Windows detected: installing AWS CLI v2 (MSI)..."
  curl --fail -L -o "$SCRIPT_TMPDIR/AWSCLIV2.msi" \
    "https://awscli.amazonaws.com/AWSCLIV2.msi"
  msiexec //i "$(cygpath -w "$SCRIPT_TMPDIR/AWSCLIV2.msi")" //passive //norestart
  rm -rf "${SCRIPT_TMPDIR:?}/"*
  echo "AWS CLI is now installed — open a new terminal so 'aws' lands on the PATH."
  echo "Session Manager plugin (SSM): install manually from the AWS docs if you use it."
  exit 0
fi

# macOS: CLI via the official universal bundle; SSM plugin via brew cask.
if [[ "$OS" == "darwin" ]]; then
  echo "macOS detected: installing AWS CLI (universal) ..."
  curl --fail -L -o "$SCRIPT_TMPDIR/awscliv2.zip" \
    "https://awscli.amazonaws.com/awscli-exe-macos-universal.zip"
  unzip -q "$SCRIPT_TMPDIR/awscliv2.zip" -d "$SCRIPT_TMPDIR"
  "$SCRIPT_TMPDIR/aws/install" -i "$LOCAL_BIN/aws-cli" -b "$LOCAL_BIN" --update
  if ! command -v session-manager-plugin >/dev/null 2>&1; then
    if command -v brew >/dev/null 2>&1; then
      echo "Installing Session Manager plugin via Homebrew..."
      brew install --cask session-manager-plugin || \
        echo "WARN: cask failed — install the SSM plugin manually from AWS docs."
    else
      echo "Skipping SSM plugin: needs Homebrew (brew install --cask session-manager-plugin)."
    fi
  fi
  rm -rf "${SCRIPT_TMPDIR:?}/"*
  echo "AWS (macOS) is now installed..."
  exit 0
fi

if [[ "$OS" != "linux" ]]; then
  echo "Unsupported OS: this installer only handles Windows/macOS/Linux (got '$OS')."
  exit 1
fi

if [[ "$ARCH" == "x86_64" ]]; then
  ARCH="x86_64"
elif [[ "$ARCH" == "aarch64" || "$ARCH" == "arm64" ]]; then
  ARCH="aarch64"
else
  echo "Unsupported architecture: $ARCH"
  exit 1
fi

# Session Manager plugin (SSM). Use the native .deb on Debian/Ubuntu (no
# rpm2cpio/cpio needed), fall back to the .rpm on RPM-based distros.
if ! command -v session-manager-plugin >/dev/null 2>&1; then
  SSM_BIN_PATH="usr/local/sessionmanagerplugin/bin/session-manager-plugin"
  # Only two extraction toolchains are supported: dpkg-deb (Debian/Ubuntu,
  # always present there) and rpm2cpio+cpio (RPM distros). Anything else
  # (Alpine, musl-based images) gets a clear error instead of a
  # command-not-found abort mid-download.
  if ! command -v dpkg-deb >/dev/null 2>&1 && ! { command -v rpm2cpio >/dev/null 2>&1 && command -v cpio >/dev/null 2>&1; }; then
    echo "Cannot extract the SSM plugin: need dpkg-deb (Debian/Ubuntu) or rpm2cpio+cpio (RPM distros)." >&2
    exit 1
  fi
  if command -v dpkg-deb >/dev/null 2>&1; then
    # Debian / Ubuntu — extract the .deb with dpkg-deb (always available).
    case "$ARCH" in
      x86_64) SSM_PKG="ubuntu_64bit" ;;
      aarch64) SSM_PKG="ubuntu_arm64" ;;
    esac
    curl --fail -L -o "$SCRIPT_TMPDIR/session-manager-plugin.deb" \
      "https://s3.amazonaws.com/session-manager-downloads/plugin/latest/${SSM_PKG}/session-manager-plugin.deb"
    dpkg-deb -x "$SCRIPT_TMPDIR/session-manager-plugin.deb" "$SCRIPT_TMPDIR"
  else
    # RPM-based distro — extract the .rpm with rpm2cpio + cpio.
    case "$ARCH" in
      x86_64) SSM_PKG="linux_64bit" ;;
      aarch64) SSM_PKG="linux_arm64" ;;
    esac
    curl --fail -L -o "$SCRIPT_TMPDIR/session-manager-plugin.rpm" \
      "https://s3.amazonaws.com/session-manager-downloads/plugin/latest/${SSM_PKG}/session-manager-plugin.rpm"
    # cd subshell rather than cpio's -D/--directory: not every cpio build
    # ships that flag, and the extraction target is the same either way.
    ( cd "$SCRIPT_TMPDIR" && rpm2cpio "session-manager-plugin.rpm" | cpio -idm --quiet )
  fi
  mv "$SCRIPT_TMPDIR/$SSM_BIN_PATH" "$LOCAL_BIN/"
fi

# AWS CLI v2. Official installer ships one bundle per arch. unzip is a
# Go-side prerequisite for the interactive flow, but a standalone run on a
# minimal distro may lack it — say so instead of feeding unzip garbage.
if ! command -v unzip >/dev/null 2>&1; then
  echo "unzip is required to extract the AWS CLI bundle — install it (apt/dnf install unzip) and re-run." >&2
  exit 1
fi
curl --fail -L -o "$SCRIPT_TMPDIR/awscliv2.zip" \
  "https://awscli.amazonaws.com/awscli-exe-${OS}-${ARCH}.zip"
unzip -q "$SCRIPT_TMPDIR/awscliv2.zip" -d "$SCRIPT_TMPDIR"
"$SCRIPT_TMPDIR/aws/install" -i "$LOCAL_BIN/aws-cli" -b "$LOCAL_BIN" --update

rm -rf "${SCRIPT_TMPDIR:?}/"*
echo "AWS and SSM are now installed..."
