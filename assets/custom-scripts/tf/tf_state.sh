#!/bin/bash

set -euo pipefail

# Terraform state explorer: list, find, show, or unlock resources in the
# current state file. Intended to be run from a directory containing a
# terraform project (where `terraform init` has been run).
#
#   tf_state.sh                  interactive fzf browser
#   tf_state.sh list             plain list of resources in state
#   tf_state.sh find <name>      grep the state for a resource address
#   tf_state.sh show <addr>      show details of one resource
#   tf_state.sh unlock <addr>    force-unlock the state (with confirmation)
#   tf_state.sh count            summary counts by resource type

if ! command -v terraform >/dev/null 2>&1; then
  echo "terraform is not installed. Exiting..."; exit 1
fi

action="${1:-browse}"
addr="${2:-}"

run_list() {
  terraform state list 2>/dev/null \
    || { echo "No state found here. Run 'terraform init' first."; exit 1; }
}

case "$action" in
  list)
    run_list
    ;;
  find)
    [[ -z "$addr" ]] && { echo "Usage: $0 find <pattern>"; exit 1; }
    run_list | grep -i "$addr" || echo "(no matches)"
    ;;
  show)
    [[ -z "$addr" ]] && { echo "Usage: $0 show <address>"; exit 1; }
    terraform state show "$addr"
    ;;
  unlock)
    [[ -z "$addr" ]] && { echo "Usage: $0 unlock <address>"; exit 1; }
    echo "About to force-unlock. This is dangerous if a lock is genuine."
    read -r -p "Type the lock ID or 'abort': " LOCK
    case "$LOCK" in
      abort|"") echo "Aborted." ;;
      *) terraform force-unlock "$LOCK" ;;
    esac
    ;;
  count)
    run_list | awk -F'.' '{print $1}' | sort | uniq -c | sort -rn
    ;;
  browse|"")
    tui="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"
    rc=0
    picked=$(run_list | bash "$tui" --title "tf_state | pick a resource" --prompt "res> ") || rc=$?
    case "$rc" in
      0) [[ -z "$picked" ]] && { echo "Nothing selected."; exit 0; } ;;
      1) echo "Nothing selected."; exit 0 ;;
      *) echo "Interactive picker needs a terminal — use: $0 list|find|show|count"; exit 1 ;;
    esac
    terraform state show "$picked"
    ;;
  *)
    echo "Usage: $0 [list|find|show|unlock|count|browse]"
    exit 1
    ;;
esac
