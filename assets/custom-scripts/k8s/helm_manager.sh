#!/bin/bash

set -euo pipefail

# Helm release manager: arrow-key-pick a release across all namespaces, then
# status / history / rollback / uninstall / get-values.

if ! command -v helm >/dev/null 2>&1; then
  echo "helm is not installed. Exiting..."; exit 1
fi

TUI_SELECT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"

echo "Fetching helm releases across all namespaces..."
REL_RAW=$(helm list -A --output json \
  | jq -r '.[] | "\(.namespace) | \(.name) | \(.status) | \(.chart) | \(.app_version)"')
if [[ -z "$REL_RAW" ]]; then
  echo "No helm releases found."
  exit 0
fi

rc=0
REL=$(printf '%s\n' "$REL_RAW" | bash "$TUI_SELECT" \
        --title "Namespace | Release | Status | Chart | AppVersion" --prompt "release> ") || rc=$?
case "$rc" in
  0) ;;
  1) echo "No release selected."; exit 0 ;;
  *)
    if [[ -f "$TUI_SELECT" ]]; then
      echo "Interactive picker needs a terminal. Non-interactive: helm status <release> -n <ns>" >&2
      exit 1
    fi
    printf '%s\n' "$REL_RAW" | nl -ba >/dev/tty
    printf '#: ' >/dev/tty
    IFS= read -r pick </dev/tty || exit 0
    REL=$(printf '%s\n' "$REL_RAW" | sed -n "${pick}p")
    ;;
esac
[[ -z "$REL" ]] && { echo "No release selected."; exit 0; }

NS=$(echo "$REL" | awk -F'|' '{gsub(/ /,"",$1); print $1}')
NAME=$(echo "$REL" | awk -F'|' '{gsub(/ /,"",$2); print $2}')

echo ""
echo "Release: $NAME  (namespace: $NS)"
echo "  1) status          2) history          3) get values"
echo "  4) rollback        5) uninstall        6) manifest"
read -r -p "Action [1]: " A
A="${A:-1}"

case "$A" in
  1) helm status "$NAME" -n "$NS" ;;
  2) helm history "$NAME" -n "$NS" ;;
  3) helm get values "$NAME" -n "$NS" ;;
  4)
    helm history "$NAME" -n "$NS"
    read -r -p "Revision to rollback to: " REV
    [[ -z "$REV" ]] && { echo "No revision."; exit 0; }
    helm rollback "$NAME" "$REV" -n "$NS"
    ;;
  5)
    read -r -p "Type 'yes' to uninstall $NAME: " CONF
    [[ "$CONF" == "yes" ]] && helm uninstall "$NAME" -n "$NS" || echo "Aborted."
    ;;
  6) helm get manifest "$NAME" -n "$NS" ;;
  *) echo "Invalid action."; exit 1 ;;
esac
