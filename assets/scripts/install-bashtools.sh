#!/bin/bash

set -euo pipefail

# Self-locate the checkout that holds this installer (assets/scripts/../..),
# like update-scripts.sh does — never assume a fixed home path;
# $HOME/chandler is only the fallback. A checkout can live anywhere. When the Go
# menu runs this installer from the materialized embedded assets, the
# environment it exports wins: ROOT/SCRIPT_DIR/SCRIPT_TMPDIR/BASHFILES_DIR
# name the materialized tree and the resolved deployment root, and
# CHANDLER_SELF names the running binary — the BASH_SOURCE-derived defaults
# below keep direct bash-from-repo usage working.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/../.." && pwd -P)"
LOCAL_BIN="$HOME/.local/bin"
SCRIPT_DIR="${SCRIPT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd -P)}"
SCRIPT_TMPDIR="${SCRIPT_TMPDIR:-$ROOT/tmp}"
BASHFILES_DIR="${BASHFILES_DIR:-$ROOT/assets/bash-files}"
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  mingw*|msys*|cygwin*) OS="windows" ;;
esac
ARCH=$(uname -m)

mkdir -p "$SCRIPT_TMPDIR" "$SCRIPT_DIR"

SCRIPTS_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/chandler/scripts"
mkdir -p "$SCRIPTS_DIR"

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

if [[ "$ARCH" == "x86_64" ]]; then
  ARCH="amd64"
elif [[ "$ARCH" == "aarch64" || "$ARCH" == "arm64" ]]; then
  ARCH="arm64"
else
  echo "Unsupported architecture: $ARCH"
  exit 1
fi

# ---- Install fzf ----
if ! command -v fzf >/dev/null 2>&1; then
  VERSION=$(curl -fsSL https://api.github.com/repos/junegunn/fzf/releases/latest | jq -r .tag_name | sed 's/^v//')
  # windows assets are zip archives, unixes tarballs.
  if [[ "$OS" == "windows" ]]; then
    DOWNLOAD_URL="https://github.com/junegunn/fzf/releases/download/v$VERSION/fzf-${VERSION}-windows_${ARCH}.zip"
    ARCHIVE="fzf-$VERSION.zip"
    ASSET_NAME="fzf-${VERSION}-windows_${ARCH}.zip"
  else
    DOWNLOAD_URL="https://github.com/junegunn/fzf/releases/download/v$VERSION/fzf-${VERSION}-${OS}_${ARCH}.tar.gz"
    ARCHIVE="fzf-$VERSION.tar.gz"
    ASSET_NAME="fzf-${VERSION}-${OS}_${ARCH}.tar.gz"
  fi
  echo "Downloading fzf from: $DOWNLOAD_URL"

  curl --fail -L -o "$SCRIPT_TMPDIR/$ARCHIVE" "$DOWNLOAD_URL"

  # The fzf release publishes fzf_<version>_checksums.txt (underscores)
  # covering every asset — fetch this platform's line and verify the
  # archive before it is extracted.
  SUMS_URL="https://github.com/junegunn/fzf/releases/download/v$VERSION/fzf_${VERSION}_checksums.txt"
  EXPECTED=$(curl -fsSL "$SUMS_URL" 2>/dev/null | awk -v name="$ASSET_NAME" '$2 == name { print $1 }')
  if [[ -z "$EXPECTED" ]]; then
    echo "ERROR: could not fetch the official fzf checksum for $ASSET_NAME from $SUMS_URL — refusing to install an unverified archive." >&2
    exit 1
  fi
  verify_sha256 "$SCRIPT_TMPDIR/$ARCHIVE" "$EXPECTED" "fzf $VERSION ($OS/$ARCH)"

  EXTRACT_DIR="$(mktemp -d)"
  if [[ "$OS" == "windows" ]]; then
    extract_zip "$SCRIPT_TMPDIR/$ARCHIVE" "$EXTRACT_DIR"
  else
    tar -xzf "$SCRIPT_TMPDIR/$ARCHIVE" -C "$EXTRACT_DIR"
  fi
  cp -r "$EXTRACT_DIR/fzf$BIN_EXT" "$LOCAL_BIN/"
  chmod +x "$LOCAL_BIN/fzf$BIN_EXT"
  rm -rf "$EXTRACT_DIR"
fi

backup_file() {
  local target="$1"
  if [[ -f "$target" ]]; then
    local bkp="${target}_bkp_$(date +%Y%m%d%H%M)"
    cp -p "$target" "$bkp"
    echo "Backed up existing $target -> $bkp"
  fi
}


backup_file "$HOME/.bash_function"
cp -r "$BASHFILES_DIR/bash_function" "$HOME/.bash_function"

backup_file "$HOME/.bash_environment"
cp -r "$BASHFILES_DIR/bash_environment" "$HOME/.bash_environment"

backup_file "$HOME/.aws_environment"
cp -r "$BASHFILES_DIR/aws_environment" "$HOME/.aws_environment"

backup_file "$HOME/.commands_environment"
cp -r "$BASHFILES_DIR/commands_environment" "$HOME/.commands_environment"

backup_file "$HOME/.bashrc"
cp -r "$BASHFILES_DIR/bashrc" "$HOME/.bashrc"

# ---- Wire the same helpers into zsh (~/.zshrc) ----
# zsh never reads .bashrc, so without this the aliases, the k/aw/tf
# dispatchers and the SSO helpers exist only in bash. When a zsh config
# exists — or zsh is the login shell and none exists yet — append a
# marker-guarded block that sources the same four dotfiles (their content
# is written to be zsh-compatible: the bash-only bits branch on
# $BASH_VERSION). The user's own .zshrc is never replaced, only appended
# to; the block carries its own `chandler shell tools` markers so it is
# idempotent here and `chandler uninstall` strips it.
ZSHRC="$HOME/.zshrc"
ZSH_MARK_BEGIN="# >>> chandler shell tools >>>"
ZSH_MARK_END="# <<< chandler shell tools <<<"
if [[ -f "$ZSHRC" || "${SHELL##*/}" == "zsh" ]]; then
  if ! grep -qF "$ZSH_MARK_BEGIN" "$ZSHRC" 2>/dev/null; then
    {
      echo ""
      echo "$ZSH_MARK_BEGIN"
      echo "# Added by install-bashtools.sh — sources the chandler shell helpers in zsh."
      echo '[ -f "$HOME/.bash_environment" ]   && source "$HOME/.bash_environment"'
      echo '[ -f "$HOME/.bash_function" ]     && source "$HOME/.bash_function"'
      echo '[ -f "$HOME/.aws_environment" ]   && source "$HOME/.aws_environment"'
      echo '[ -f "$HOME/.commands_environment" ] && source "$HOME/.commands_environment"'
      echo "$ZSH_MARK_END"
    } >> "$ZSHRC"
    echo "Wired chandler shell helpers into $ZSHRC"
  fi
fi

# ---- Create symlink to AWS config (ensure ~/.aws exists) ----
mkdir -p "$HOME/.aws"
if [[ -e "$HOME/.aws/config" && ! -L "$HOME/.aws/config" ]]; then
  backup_file "$HOME/.aws/config"
fi
ln -sf "$SCRIPTS_DIR/config" "$HOME/.aws/config"

# ---- Install the Go entrypoint as a portable copy on the PATH (`chandler`) ----
# A copy, not a symlink: the binary resolves its checkout through the pin
# file, and `chandler update` can refresh the copy in place. The source is
# CHANDLER_SELF when the Go menu set it (the running binary itself, since it
# carries the embedded assets); the fallback keeps direct
# bash-from-repo usage working, where the binary sits at
# ../../GO/chandler (assets/scripts/ → repo root → GO).
# tmp-file + mv -f (rename) swaps the command atomically, so a running
# `chandler` binary is never clobbered mid-execution. Missing binary: note
# it and move on.
GO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/../.." && pwd -P)"
# windows runs it as chandler.exe — the Go side installs the same name.
if [[ "$OS" == "windows" ]]; then
  CHANDLER_NAME="chandler.exe"
  GO_SUF=".exe"
else
  CHANDLER_NAME="chandler"
  GO_SUF=""
fi
# The built binary sits at the checkout root (flat layout) or in the GO
# module dir (nested layout); prefer whichever exists.
GO_FALLBACK="$GO_ROOT/chandler$GO_SUF"
if [[ ! -f "$GO_FALLBACK" ]]; then
  GO_FALLBACK="$GO_ROOT/GO/chandler$GO_SUF"
fi
GO_BIN="${CHANDLER_SELF:-$GO_FALLBACK}"
if [[ -x "$GO_BIN" ]]; then
  cp "$GO_BIN" "$LOCAL_BIN/.chandler.tmp.$$"
  mv -f "$LOCAL_BIN/.chandler.tmp.$$" "$LOCAL_BIN/$CHANDLER_NAME"
  chmod 0755 "$LOCAL_BIN/$CHANDLER_NAME"
  # Pin the checkout this installer ran from — but only when running
  # standalone. Under the Go menu (CHANDLER_SELF set) the binary owns the
  # pin: resolveRoot persists it whenever a marker candidate wins.
  if [[ -z "${CHANDLER_SELF:-}" ]]; then
    ROOT="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/../.." && pwd -P)"
    mkdir -p "$HOME/.config/chandler"
    printf '%s\n' "$ROOT" > "$HOME/.config/chandler/path"
  fi
else
  echo "Go binary not found — 'chandler' not installed (build it: cd GO && go build -o chandler .)"
fi

# ---- Set executable permission on scripts (recursive — scripts now live in
#      aws/ k8s/ tf/ misc/ subfolders under $SCRIPTS_DIR/) ----
find "$SCRIPTS_DIR" -type f -name '*.sh' -exec chmod +x {} +

rm -rf "${SCRIPT_TMPDIR:?}/"*
echo "Installed Bash Tools..."
