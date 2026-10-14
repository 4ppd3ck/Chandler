#!/bin/bash

set -euo pipefail

# Re-copy the helper scripts from the deployment repo (custom/, next to
# scripts/) into ~/.local/share/chandler/scripts/ — so edits made in the repo (e.g. a
# fixed add_yaml_to_kconf.sh) take effect for the aliases (k8screds, k_top,
# ssh_key_gen, ...) without re-running the full Bash-tools installer.
#
# Dotfiles (~/.bashrc, aliases, functions) are NOT touched here — refresh
# those with menu option 8 (install-bashtools.sh) if they changed.

# The repo root is the checkout holding this script (assets/scripts/../..),
# so a checkout living anywhere works; $HOME/chandler is the fallback for
# a lone copy of the script.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SRC="$ROOT/custom"
[[ -d "$SRC" ]] || SRC="$HOME/chandler/custom"
DST="$HOME/.local/share/chandler/scripts"

if [[ ! -d "$SRC" ]]; then
  echo "Source folder not found: $SRC"
  echo "This updater expects the repo's custom/ folder (under the repo root, or at ~/chandler)."
  exit 1
fi

mkdir -p "$DST"

echo "Comparing $SRC -> $DST ..."
change_list=$(diff -rq "$SRC" "$DST" 2>/dev/null | grep -vF "Only in $DST" || true)

if [[ -z "$change_list" ]]; then
  echo "Already up to date — nothing to copy."
  exit 0
fi

printf '%s\n' "$change_list" | sed 's/^/  /'

cp -R "$SRC"/. "$DST"/

n=$(printf '%s\n' "$change_list" | wc -l)
echo ""
echo "Updated $n item(s). New/changed scripts take effect immediately"
echo "(new ALIASES need a fresh terminal, or menu option 8 for dotfiles)."
