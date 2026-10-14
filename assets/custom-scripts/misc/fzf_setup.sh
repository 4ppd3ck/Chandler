#!/bin/bash

# fzf keybinding setup for interactive bash sessions.
#
# This script is designed to be BOTH:
#   - sourced (`. ~/.local/share/chandler/scripts/misc/fzf_setup.sh`)  -> activates the
#     bindings silently in the current shell (used by the opt-in installer),
#   - run directly (`fzf_setup`)                            -> activates and
#     prints a short confirmation message.
#
# Binds (when sourced/run in an interactive bash shell with fzf installed):
#   Alt+S        fuzzy-search aliases + functions + history + PATH executables
#                (TAB is intentionally left free for normal shell
#                autocompletion of files, folders and commands)
#   Ctrl+R       fuzzy-search command history
#   Ctrl+T       fuzzy-pick a file and insert its path at the cursor
#   Alt+C        fuzzy-pick a directory and cd into it
#
# Opt-in. Nothing in the base dotfiles sources this automatically; run the
# `install-fzf-setup.sh` installer (menu option 9) to enable it, or call
# `fzf_setup` any time to activate it in the current shell.


if ! command -v fzf >/dev/null 2>&1; then
  echo "fzf is not installed. Run install-bashtools first."
  return 1 2>/dev/null || exit 1
fi

export FZF_DEFAULT_OPTS="${FZF_DEFAULT_OPTS:-} --height 40% --reverse --exact"

# ---- Alt+S: combined aliases/functions/history/executables picker ----
__fzf_search_command() {
  local aliases functions cmds
  aliases=$(alias 2>/dev/null | sed 's/alias \([^=]*\)=.*/\1/' | sort -u || true)
  functions=$(declare -F 2>/dev/null | awk '{print $3}' | sort -u || true)
  cmds=$(compgen -c 2>/dev/null | sort -u || true)
  {
    echo "$aliases"
    echo "$functions"
    [[ -f "${HISTFILE:-}" ]] && tail -n 500 "${HISTFILE}" 2>/dev/null || true
    echo "$cmds"
  } | awk '!seen[$0]++' \
    | fzf --height 40% --reverse --exact --no-sort \
          --prompt="cmd> " \
          --bind "ctrl-r:reload(compgen -c | sort -u)" \
          --header "Alt+S mode | type to filter | Enter to insert" \
    || true
}

__fzf_search_widget() {
  local selected
  selected=$(__fzf_search_command)
  READLINE_LINE="${selected}${READLINE_LINE:+$READLINE_LINE}"
  READLINE_POINT=${#READLINE_LINE}
}

# ---- Ctrl+R: fuzzy history search ----
__fzf_history_widget() {
  local selected
  selected=$(history -n 1 2>/dev/null \
    | awk '!seen[$0]++' \
    | fzf --height 40% --reverse --exact --no-sort \
          --prompt="hist> " \
          --bind "ctrl-r:reload(history -n 1 | awk '!seen[$0]++')" \
          --header "Ctrl+R | type to search | Enter to insert" \
    || true)
  READLINE_LINE="${selected}${READLINE_LINE:+ $READLINE_LINE}"
  READLINE_POINT=${#READLINE_LINE}
}

# ---- Ctrl+T: fuzzy file picker ----
__fzf_file_widget() {
  local selected
  selected=$(find -L . -maxdepth 5 -type f 2>/dev/null \
    | cut -c3- \
    | fzf --height 40% --reverse --exact \
          --prompt="file> " \
          --header "Ctrl+T | pick a file to insert at cursor" \
    || true)
  READLINE_LINE="${READLINE_LINE:0:$READLINE_POINT}${selected}${READLINE_LINE:$READLINE_POINT}"
  READLINE_POINT=$(( READLINE_POINT + ${#selected} ))
}

# ---- Alt+C: fuzzy cd ----
__fzf_cd_widget() {
  local selected
  selected=$(find -L . -maxdepth 5 -type d 2>/dev/null \
    | cut -c3- \
    | fzf --height 40% --reverse --exact \
          --prompt="cd> " \
          --header "Alt+C | pick a directory to cd into" \
          --no-sort \
    || true)
  [[ -n "$selected" ]] && cd "$selected"
}

# `bind` only exists in interactive bash. `bind -x` registers a macro that
# runs when the key is pressed. The `2>/dev/null || true` guard makes this
# safe even when the script is sourced from a non-interactive context.
# Alt+S is `\es` (ESC + s), matching the Alt+C (`\ec`) convention above.
if [[ -n "${BASH_VERSION:-}" ]]; then
  bind -x '"\es": __fzf_search_widget'   2>/dev/null || true
  bind -x '"\C-r": __fzf_history_widget' 2>/dev/null || true
  bind -x '"\C-t": __fzf_file_widget'    2>/dev/null || true
  bind -x '"\ec": __fzf_cd_widget'       2>/dev/null || true
fi

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  echo "fzf keybindings active. Alt+S=commands, Ctrl+R=history, Ctrl+T=files, Alt+C=cd."
fi
