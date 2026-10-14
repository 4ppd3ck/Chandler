#!/bin/bash

set -uo pipefail

# ArgoCD application manager.
#
# A small dispatcher over the `argocd` CLI, with fzf where it helps. Mirrors
# the spirit of the `k` / `aw` / `tf` shell functions: short verbs that wrap
# the common case, passthrough for everything else.
#
# Usage:
#   argocd_manager.sh                     # show cheat sheet
#   argocd_manager.sh list                # list apps (table)
#   argocd_manager.sh app                 # fzf-pick an app, then act on it
#   argocd_manager.sh sync <app>          # sync an app
#   argocd_manager.sh diff <app>          # diff live vs desired
#   argocd_manager.sh status <app>        # show app status
#   argocd_manager.sh history <app>       # deployment history
#   argocd_manager.sh rollback <app> <id> # rollback to a history id
#   argocd_manager.sh logs <app>          # tail app logs (workload pods)
#   argocd_manager.sh refresh <app>       # hard-refresh app state
#
# Requires the `argocd` CLI and a logged-in context (`argocd login`).

if ! command -v argocd >/dev/null 2>&1; then
  echo "argocd CLI is not installed."
  echo "Install it with:  curl -sSL -o ~/.local/bin/argocd \\"
  echo "  https://github.com/argoproj/argo-cd/releases/latest/download/argocd-linux-amd64"
  echo "  && chmod +x ~/.local/bin/argocd"
  echo "Then: argocd login <server>"
  exit 1
fi

usage() {
  sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
}

pick_app() {
  local prompt="${1:-Select app: }"
  local tui
  tui="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"
  local rc=0 choice
  choice=$(argocd app list -o name 2>/dev/null \
    | bash "$tui" --title "argocd | pick an app" --prompt "$prompt") || rc=$?
  case "$rc" in
    0) [[ -z "$choice" ]] && { echo "No app selected."; exit 0; }
       echo "$choice" ;;
    1) echo "No app selected."; exit 0 ;;
    *) echo "Interactive picker needs a terminal; pass the app name explicitly." >&2; exit 1 ;;
  esac
}

if [[ $# -eq 0 ]]; then usage; exit 0; fi

cmd="$1"; shift || true
case "$cmd" in
  -h|--help|help) usage; exit 0 ;;

  ls|list)
    argocd app list "$@"
    ;;

  app|apps)
    APP=$(pick_app "Select app: ")
    echo ""
    echo "App: $APP"
    echo "  1) status      2) sync         3) diff        4) history"
    echo "  5) rollback    6) logs         7) refresh     8) manifest"
    read -r -p "Action [1]: " A
    A="${A:-1}"
    case "$A" in
      1) argocd app get "$APP" ;;
      2)
        read -r -p "Sync $APP now? [y/N]: " CONF
        [[ "$CONF" == "y" || "$CONF" == "Y" ]] && argocd app sync "$APP" ;;
      3) argocd app diff "$APP" ;;
      4) argocd app history "$APP" ;;
      5)
        argocd app history "$APP"
        read -r -p "Revision id to rollback to: " REV
        [[ -n "$REV" ]] && argocd app rollback "$APP" "$REV" ;;
      6) argocd app logs "$APP" ;;
      7) argocd app refresh "$APP" ;;
      8) argocd app manifest "$APP" ;;
      *) echo "Invalid action."; exit 1 ;;
    esac
    ;;

  get|status)
    APP="${1:-}"; [[ -z "$APP" ]] && APP=$(pick_app "App to show: ")
    argocd app get "$APP"
    ;;

  sync)
    APP="${1:-}"; [[ -z "$APP" ]] && APP=$(pick_app "App to sync: ")
    read -r -p "Sync $APP now? [y/N]: " CONF
    [[ "$CONF" == "y" || "$CONF" == "Y" ]] && argocd app sync "$APP"
    ;;

  diff)
    APP="${1:-}"; [[ -z "$APP" ]] && APP=$(pick_app "App to diff: ")
    argocd app diff "$APP"
    ;;

  history)
    APP="${1:-}"; [[ -z "$APP" ]] && APP=$(pick_app "App history: ")
    argocd app history "$APP"
    ;;

  rollback)
    APP="${1:-}"; REV="${2:-}"
    [[ -z "$APP" ]] && APP=$(pick_app "App to rollback: ")
    if [[ -z "$REV" ]]; then
      argocd app history "$APP"
      read -r -p "Revision id to rollback to: " REV
    fi
    [[ -n "$REV" ]] && argocd app rollback "$APP" "$REV"
    ;;

  logs)
    APP="${1:-}"; [[ -z "$APP" ]] && APP=$(pick_app "App logs: ")
    argocd app logs "$APP" "${@:2}"
    ;;

  refresh)
    APP="${1:-}"; [[ -z "$APP" ]] && APP=$(pick_app "App to refresh: ")
    argocd app refresh "$APP"
    ;;

  manifest)
    APP="${1:-}"; [[ -z "$APP" ]] && APP=$(pick_app "App manifest: ")
    argocd app manifest "$APP"
    ;;

  login)
    argocd login "$@"
    ;;

  *)
    argocd "$cmd" "$@"
    ;;
esac
