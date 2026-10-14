#!/bin/bash

set -euo pipefail


BASHRC="$HOME/.bashrc"
MARK_BEGIN="# >>> chandler fzf/cursor setup >>>"
MARK_END="# <<< chandler fzf/cursor setup <<<"

FZF_SETUP="$HOME/.local/share/chandler/scripts/misc/fzf_setup.sh"
SET_CURSOR="$HOME/.local/share/chandler/scripts/misc/set_cursor.sh"

# ---- Ensure the helper scripts are installed ----
# The repo's custom/ tree, self-located two levels up from this script
# (assets/scripts/ → repo root); the legacy $HOME/chandler copy
# stays the fallback for a lone copy of the script.
CUSTOM_SRC="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/../.." && pwd -P)/custom/misc"
[[ -d "$CUSTOM_SRC" ]] || CUSTOM_SRC="$HOME/chandler/custom/misc"
if [[ ! -f "$FZF_SETUP" && -f "$CUSTOM_SRC/fzf_setup.sh" ]]; then
  mkdir -p "$(dirname "$FZF_SETUP")"
  cp "$CUSTOM_SRC/fzf_setup.sh" "$FZF_SETUP"
  chmod +x "$FZF_SETUP"
  echo "Copied fzf_setup.sh -> $FZF_SETUP"
fi
if [[ ! -f "$SET_CURSOR" && -f "$CUSTOM_SRC/set_cursor.sh" ]]; then
  mkdir -p "$(dirname "$SET_CURSOR")"
  cp "$CUSTOM_SRC/set_cursor.sh" "$SET_CURSOR"
  chmod +x "$SET_CURSOR"
  echo "Copied set_cursor.sh -> $SET_CURSOR"
fi

# ---- Remove any existing managed block from .bashrc ----
strip_block() {
  local tmp
  [[ ! -f "$BASHRC" ]] && return 0
  # Delete the marker-delimited block (inclusive) with awk; the markers
  # match by suffix too, so blocks written by earlier marker names are
  # stripped. (sed address regexes break here: the markers contain '/' — e.g.
  # "fzf/cursor" — which would terminate the address pattern early.)
  tmp=$(mktemp)
  awk -v b="$MARK_BEGIN" -v e="$MARK_END" '
    BEGIN { n = 0 }
    {
      if ($0 == b || $0 ~ /^# >>> .* fzf\/cursor setup >>>$/) {
        skip = 1
        # also drop the blank separator line build_block added before the block
        if (n > 0 && line[n] == "") n--
      } else if ($0 == e || $0 ~ /^# <<< .* fzf\/cursor setup <<<$/) {
        skip = 0
      } else if (!skip) {
        line[++n] = $0
      }
    }
    END { for (i = 1; i <= n; i++) print line[i] }
  ' "$BASHRC" > "$tmp"
  # Only replace if something actually changed.
  if ! cmp -s "$tmp" "$BASHRC"; then
    cp "$tmp" "$BASHRC"
  fi
  rm -f "$tmp"
}

# ---- Build the managed block for a given choice ----
build_block() {
  local choice="$1"
  echo ""
  echo "$MARK_BEGIN"
  echo "# Added by install-fzf-setup.sh. Edit or remove via menu option 9."
  case "$choice" in
    fzf)
      echo "[[ -f \"$FZF_SETUP\" ]] && source \"$FZF_SETUP\""
      ;;
    cursor)
      echo "[[ -f \"$SET_CURSOR\" ]] && source \"$SET_CURSOR\""
      ;;
    both)
      echo "[[ -f \"$FZF_SETUP\" ]]   && source \"$FZF_SETUP\""
      echo "[[ -f \"$SET_CURSOR\" ]] && source \"$SET_CURSOR\""
      ;;
  esac
  echo "$MARK_END"
}

if [[ $# -ge 1 ]]; then
  CHOICE="$1"
else
  echo "=== fzf / cursor setup (opt-in) ==="
  echo "  1  - fzf keybindings only   (Alt+S / Ctrl+R / Ctrl+T / Alt+C)"
  echo "  2  - Cursor styling only    (blinking red vertical bar)"
  echo "  3  - Both"
  echo "  4  - Remove (restore default behaviour)"
  echo ""
  read -rp "Enter option [1-4]: " CHOICE
fi

case "$CHOICE" in
  1) MODE="fzf" ;;
  2) MODE="cursor" ;;
  3) MODE="both" ;;
  4) MODE="remove" ;;
  *)
    echo "Invalid option: '${CHOICE}'."
    exit 1
    ;;
esac

strip_block

if [[ "$MODE" == "remove" ]]; then
  echo "Removed fzf/cursor setup from $BASHRC."
  echo "Reopen your terminals for the change to take effect."
  exit 0
fi

if [[ "$MODE" == "fzf" || "$MODE" == "both" ]]; then
  if ! command -v fzf >/dev/null 2>&1; then
    echo "WARNING: fzf is not installed."
    echo "Run 'install-bashtools' (menu option 8) first, then re-run this."
    exit 1
  fi
fi

touch "$BASHRC"
build_block "$MODE" >> "$BASHRC"

echo "Configured ($MODE) in $BASHRC."
echo "Reopen your terminals for the change to take effect."
