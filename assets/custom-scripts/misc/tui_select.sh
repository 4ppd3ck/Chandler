#!/bin/bash

# tui_select.sh — CleanMyMac-style interactive list picker. No fzf needed.
#
# Reads candidate lines on stdin, shows them in an arrow-key menu (with
# type-to-filter), and prints the chosen line on stdout. Keys are read from
# /dev/tty and the menu is drawn there too, so the stdout pipe stays clean:
#
#   PICKED=$(printf '%s\n' "${ITEMS[@]}" | tui_select.sh --title "pick one")
#
# Keys:
#   ↑/↓ or j/k   move           g/Home  first item
#   type         filter list    G/End   last item
#   Enter        select         Esc     cancel (exit 1)
#
# Exit codes: 0 selected, 1 cancelled, 2 no candidates / no terminal —
# callers use 2 to fall back to a numbered prompt.
#
# Deliberately NO `set -e`: an interactive key loop must survive odd exit
# statuses from arithmetic/tests mid-loop.

TITLE="Select"
PROMPT="> "
HEIGHT=12
MULTI=0

while [[ $# -ge 1 ]]; do
  case "$1" in
    --title)  TITLE="$2"; shift 2 ;;
    --prompt) PROMPT="$2"; shift 2 ;;
    --height) HEIGHT="$2"; shift 2 ;;
    --multi)  MULTI=1; shift ;;
    *) shift ;;
  esac
done

# --- terminal: everything interactive happens on /dev/tty (fd 3) ---------
# The open itself is the test: /dev/tty can exist and pass -r/-w checks yet
# fail to open when the process has no controlling terminal.
if ! { exec 3<>/dev/tty; } 2>/dev/null; then
  exit 2
fi

# --- colors (CleanMyMac style, matching the bash original) --------------
if [[ -z "${NO_COLOR:-}" ]]; then
  C_RESET=$'\e[0m'; C_BOLD=$'\e[1m'; C_DIM=$'\e[2m'
  C_MAGENTA=$'\e[35m'; C_BMAGENTA=$'\e[1;35m'; C_GRAY=$'\e[90m'
  C_BGREEN=$'\e[1;32m'
else
  C_RESET=''; C_BOLD=''; C_DIM=''; C_MAGENTA=''; C_BMAGENTA=''; C_GRAY=''
  C_BGREEN=''
fi

COLS=$(tput cols 2>/dev/null || echo 80)
(( COLS < 40 )) && COLS=40
MAXW=$(( COLS - 8 ))
(( MULTI == 1 )) && MAXW=$(( MAXW - 2 ))

# --- candidates -----------------------------------------------------------
mapfile -t ITEMS
N_ITEMS=${#ITEMS[@]}
if (( N_ITEMS == 0 )); then
  printf '\r%sno candidates%s\n' "$C_DIM" "$C_RESET" >&3
  exit 2
fi

FILTER=""
SEL=0
PREV_HEIGHT=0
declare -A CHECKED=()      # multi mode: item text -> 1

# visible[i] = index into ITEMS matching the current filter (case-insensitive)
declare -a VIS=()
recompute_vis() {
  VIS=()
  local i needle="${FILTER,,}"
  if [[ -z "$needle" ]]; then
    VIS=( "${ITEMS[@]}" )
    return 0
  fi
  for (( i = 0; i < N_ITEMS; i++ )); do
    [[ "${ITEMS[$i],,}" == *"$needle"* ]] && VIS+=( "${ITEMS[$i]}" )
  done
  return 0
}

tty_puts() { printf '%b' "$1" >&3; }

erase_block() {
  if (( PREV_HEIGHT > 0 )); then
    tty_puts "\e[${PREV_HEIGHT}A"
    local n
    for (( n = 0; n < PREV_HEIGHT; n++ )); do tty_puts '\r\e[2K\n'; done
    tty_puts "\e[${PREV_HEIGHT}A"
  fi
}

draw() {
  erase_block
  local n first last line
  local n_vis=${#VIS[@]}
  (( SEL >= n_vis )) && SEL=$(( n_vis - 1 ))
  (( SEL < 0 )) && SEL=0

  tty_puts "\r\e[2K${C_BMAGENTA}▶${C_RESET} ${C_BOLD}${TITLE}${C_RESET}\n"
  tty_puts "\r\e[2K${C_GRAY}${PROMPT}${C_RESET}${FILTER}${C_DIM}  (${n_vis}/${N_ITEMS})${C_RESET}\n"
  tty_puts "\r\e[2K${C_MAGENTA}$(printf '━%.0s' $(seq 1 "$COLS"))${C_RESET}\n"

  if (( n_vis == 0 )); then
    tty_puts "\r\e[2K${C_DIM}  (no matches)${C_RESET}\n"
    PREV_HEIGHT=4
  else
    first=$(( SEL - HEIGHT / 2 ))
    (( first < 0 )) && first=0
    last=$(( first + HEIGHT ))
    (( last > n_vis )) && last=$(( n_vis ))
    (( last - first < HEIGHT && first > 0 )) && first=$(( last - HEIGHT ))
    (( first < 0 )) && first=0
    for (( n = first; n < last; n++ )); do
      line="${VIS[$n]}"
      (( ${#line} > MAXW )) && line="${line:0:MAXW}"
      if (( n == SEL )); then
        tty_puts "\r\e[2K${C_BMAGENTA}▶${C_RESET} "
      else
        tty_puts "\r\e[2K  "
      fi
      if (( MULTI == 1 )); then
        if [[ -n "${CHECKED[${VIS[$n]}]:-}" ]]; then
          tty_puts "${C_BGREEN}✓${C_RESET} "
        else
          tty_puts "${C_GRAY}·${C_RESET} "
        fi
      fi
      if (( n == SEL )); then
        tty_puts "${C_BOLD}${line}${C_RESET}\n"
      else
        tty_puts "${line}${C_RESET}\n"
      fi
    done
    PREV_HEIGHT=$(( 3 + last - first ))
  fi

  tty_puts "\r\e[2K${C_MAGENTA}$(printf '━%.0s' $(seq 1 "$COLS"))${C_RESET}\n"
  if (( MULTI == 1 )); then
    tty_puts "\r\e[2K${C_GRAY}↑↓ navigate · type to filter · ␣ check · a all · d none · ⏎ done · esc cancel${C_RESET}\n"
  else
    tty_puts "\r\e[2K${C_GRAY}↑↓ navigate · type to filter · ⏎ select · esc cancel${C_RESET}\n"
  fi
  PREV_HEIGHT=$(( PREV_HEIGHT + 2 ))
}

finish_ok() {
  erase_block
  tty_puts '\e[?25h'
  if (( MULTI == 1 )); then
    local n printed=0
    for (( n = 0; n < ${#VIS[@]}; n++ )); do
      if [[ -n "${CHECKED[${VIS[$n]}]:-}" ]]; then
        printf '%s\n' "${VIS[$n]}"
        printed=1
      fi
    done
    if (( printed == 1 )); then
      exit 0
    fi
    # Nothing checked: accept the highlighted item (fzf-style), so
    # highlight + Enter is a valid single pick in multi mode too.
    printf '%s\n' "${VIS[$SEL]}"
    exit 0
  fi
  printf '%s\n' "${VIS[$SEL]}"
  exit 0
}

finish_cancel() {
  erase_block
  tty_puts '\e[?25h'
  exit 1
}

trap 'tty_puts "\e[?25h"' EXIT

recompute_vis
tty_puts '\e[?25l'          # hide cursor while the picker is up
draw

while true; do
  IFS= read -rsn1 key <&3 || finish_cancel
  case "$key" in
    $'\x03'|$'\x04')  finish_cancel ;;                      # Ctrl+C / Ctrl+D
    $'\x1b')                                          # ESC or arrow sequence
      seq=''
      IFS= read -rsn2 -t 0.05 seq <&3 || seq=''
      case "$seq" in
        '[A') (( SEL > 0 )) && SEL=$(( SEL - 1 )); draw ;;
        '[B') (( SEL < ${#VIS[@]} - 1 )) && SEL=$(( SEL + 1 )); draw ;;
        '[H'|'1~'|'7~') SEL=0; draw ;;
        '[F'|'4~'|'8~') SEL=$(( ${#VIS[@]} - 1 )); draw ;;
        *)    finish_cancel ;;                            # bare ESC = cancel
      esac
      ;;
    $'\x7f'|$'\x08')                                    # Backspace
      FILTER="${FILTER%?}"
      SEL=0
      recompute_vis
      draw
      ;;
    $'\n'|$'\r'|'')
      (( ${#VIS[@]} == 0 )) && { draw; continue; }
      finish_ok
      ;;
    j|k|g|G)                                            # vim keys
      case "$key" in
        j) (( SEL < ${#VIS[@]} - 1 )) && SEL=$(( SEL + 1 )) ;;
        k) (( SEL > 0 )) && SEL=$(( SEL - 1 )) ;;
        g) SEL=0 ;;
        G) SEL=$(( ${#VIS[@]} - 1 )) ;;
      esac
      draw
      ;;
    ' ')
      if (( MULTI == 1 )); then
        if [[ -n "${CHECKED[${VIS[$SEL]}]:-}" ]]; then
          unset "CHECKED[${VIS[$SEL]}]"
        else
          CHECKED[${VIS[$SEL]}]=1
        fi
        draw
      else
        FILTER+="$key"
        SEL=0
        recompute_vis
        draw
      fi
      ;;
    *)
      if [[ $MULTI == 1 && "$key" == "a" ]]; then         # check all visible
        local n
        for (( n = 0; n < ${#VIS[@]}; n++ )); do CHECKED[${VIS[$n]}]=1; done
        draw
        continue
      fi
      if [[ $MULTI == 1 && "$key" == "d" ]]; then         # uncheck all visible
        local n
        for (( n = 0; n < ${#VIS[@]}; n++ )); do unset "CHECKED[${VIS[$n]}]"; done
        draw
        continue
      fi
      # Any other printable char feeds the filter (including 'q').
      if [[ "$key" == [[:print:]] && ${#key} -eq 1 ]]; then
        FILTER+="$key"
        SEL=0
        recompute_vis
        draw
      fi
      ;;
  esac
done
