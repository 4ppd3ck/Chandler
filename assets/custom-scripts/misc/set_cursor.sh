#!/bin/bash

# Set the terminal cursor to a blinking red vertical bar.
#
# Source-safe: works whether sourced into an interactive shell or run
# directly. Opt-in - nothing in the base dotfiles sources this automatically;
# use the `install-fzf-setup.sh` installer (menu option 8) to enable it
# permanently, or run `set_cursor` any time to apply it in the current shell.
#
#   set_cursor.sh            red blinking vertical bar cursor
#   set_cursor.sh --reset    restore the default cursor
#
# Cursor color uses the xterm OSC 12 sequence (\e]12;...).
# Cursor shape uses the DEC private mode sequence (\e[5 q).
# Only applies to xterm-compatible terminals; silently skips others.

__set_cursor_red() {
  case "${TERM:-}" in
    xterm*|screen*|tmux*|vt100*|linux)
      printf '\e]12;#ff0000\a'   # cursor color: red
      printf '\e[5 q'             # cursor shape: blinking vertical bar
      ;;
    *)
      # Non-interactive or unsupported terminal.
      if [[ "${BASH_SOURCE[0]:-${0}}" == "${0}" ]]; then
        echo "Unsupported terminal: ${TERM:-unset} (skipping cursor change)."
        echo "Supported: xterm, screen, tmux, vt100, linux."
      fi
      return 1 2>/dev/null || exit 1
      ;;
  esac
}

__set_cursor_default() {
  printf '\e]12;white\a'   # cursor color: white
  printf '\e[1 q'           # cursor shape: steady block (default)
}

# Only parse args / print messages when run directly (not when sourced).
if [[ "${BASH_SOURCE[0]:-${0}}" == "${0}" ]]; then
  case "${1:-}" in
    --reset|-r)
      __set_cursor_default
      echo "Cursor reset to default."
      exit 0
      ;;
    --help|-h)
      echo "Usage:"
      echo "  set_cursor.sh            red blinking vertical bar cursor"
      echo "  set_cursor.sh --reset    restore the default cursor"
      exit 0
      ;;
    "")
      __set_cursor_red && echo "Cursor set to blinking red vertical bar."
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      echo "Usage: set_cursor.sh [--reset|--help]"
      exit 1
      ;;
  esac
else
  # Sourced: apply silently. Default to red unless RESET_CURSOR is set.
  if [[ "${RESET_CURSOR:-0}" == "1" ]]; then
    __set_cursor_default
  else
    __set_cursor_red
  fi
fi
