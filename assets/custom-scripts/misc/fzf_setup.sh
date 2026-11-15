#!/bin/bash

# fzf keybinding setup for interactive bash and zsh sessions.
#
# This script is designed to be BOTH:
#   - sourced (`. ~/.local/share/chandler/scripts/misc/fzf_setup.sh`)  -> activates the
#     bindings silently in the current shell (used by the opt-in installer),
#   - run directly (`fzf_setup`)                            -> activates and
#     prints a short confirmation message.
#
# Binds (when sourced/run in an interactive bash or zsh shell with fzf installed):
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

# ---- zsh: the same four keys, zsh-native plumbing ----
# zsh has no `bind -x` and no READLINE_LINE/READLINE_POINT — the editable
# line is BUFFER/CURSOR and key handling goes through zle widgets. Ctrl+R,
# Ctrl+T and Alt+C come from fzf's own zsh integration (`fzf --zsh`,
# fzf >= 0.48 — the installer always downloads a current fzf); Alt+S keeps
# the combined aliases/functions/history/executables picker as a custom
# widget, with zsh-native listing (alias -L, the functions table, the
# $path array — $path is zsh's array twin of PATH).
if [[ -n "${ZSH_VERSION:-}" ]]; then
  __fzf_zsh_search_command() {
    {
      alias -L 2>/dev/null | sed 's/^alias \([^=]*\)=.*/\1/'
      print -r -l -- ${(k)functions} 2>/dev/null
      for d in $path; do [[ -d "$d" ]] && ls "$d" 2>/dev/null; done
      [[ -f "${HISTFILE:-}" ]] && tail -n 500 "${HISTFILE}" 2>/dev/null
    } | awk '!seen[$0]++' \
      | fzf --height 40% --reverse --exact --no-sort \
            --prompt="cmd> " \
            --header "Alt+S mode | type to filter | Enter to insert" \
      || true
  }

  __fzf_search_widget_zsh() {
    local selected
    selected=$(__fzf_zsh_search_command)
    if [[ -n "$selected" ]]; then
      BUFFER="${selected}${BUFFER}"
      CURSOR=${#BUFFER}
    fi
    zle reset-prompt
  }

  if fzf --zsh >/dev/null 2>&1 </dev/null; then
    source <(fzf --zsh)
  fi
  zle -N __fzf_search_widget_zsh
  bindkey '\es' __fzf_search_widget_zsh 2>/dev/null || true
fi

# Sourced or executed? bash: BASH_SOURCE[0] differs from $0 when sourced.
# zsh: $0 IS the sourced file's path, but ZSH_ARGZERO keeps the shell's own
# argv[0], so the two differ exactly when the file was executed. (Inside a
# function zsh's $0 is the defining file's path — this helper's — which is
# what the comparison wants.)
if [[ -n "${ZSH_VERSION:-}" ]]; then
  __fzf_was_executed() { [[ "${ZSH_ARGZERO:-}" == "${0}" ]]; }
else
  __fzf_was_executed() { [[ "${BASH_SOURCE[0]:-${0}}" == "${0}" ]]; }
fi

if __fzf_was_executed; then
  echo "fzf keybindings active. Alt+S=commands, Ctrl+R=history, Ctrl+T=files, Alt+C=cd."
fi
