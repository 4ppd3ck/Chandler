#!/bin/bash

set -euo pipefail

# Restart a deployment the safe way: fzf-pick it (across all namespaces),
# confirm, rollout restart, then watch the rollout status until it settles.
#
# Usage:
#   k_restart.sh                    interactive picker (fzf)
#   k_restart.sh <deploy> [ns]      non-interactive restart
#   k_restart.sh --list             just list deployments, no restart

if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl is not installed. Exiting..."; exit 1
fi

DEPLOY="${1:-}"
NS="${2:-}"

if [[ -n "$DEPLOY" && "$DEPLOY" != "--list" ]]; then
  NS_FLAG=()
  [[ -n "$NS" ]] && NS_FLAG=(-n "$NS")
  echo "Restarting deployment $DEPLOY..."
  kubectl rollout restart ${NS_FLAG[@]+"${NS_FLAG[@]}"} deployment "$DEPLOY"
  kubectl rollout status ${NS_FLAG[@]+"${NS_FLAG[@]}"} deployment "$DEPLOY"
  echo "Done."
  exit 0
fi

LIST=$(kubectl get deployments -A -o jsonpath \
  '{range .items[*]}{.metadata.namespace}{"\t"}{.metadata.name}{"\t"}{.status.readyReplicas}/{.status.replicas}{"\n"}{end}' \
  2>/dev/null || true)

if [[ -z "$LIST" ]]; then
  echo "No deployments found (or no cluster context)."
  exit 1
fi

if [[ "$DEPLOY" == "--list" ]]; then
  echo -e "NAMESPACE\tNAME\tREADY"
  echo "$LIST"
  exit 0
fi

TUI_SELECT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"
rc=0
PICKED=$(printf '%s\n' "$LIST" \
  | bash "$TUI_SELECT" --title "k_restart | pick a deployment" --prompt "restart> ") || rc=$?

case "$rc" in
  0) ;;
  1) exit 0 ;;
  *)
    if [[ -f "$TUI_SELECT" ]]; then
      echo "Interactive picker needs a terminal. Non-interactive: $0 <deploy> [namespace]" >&2
      exit 1
    fi
    printf '%s\n' "$LIST" | nl -ba >/dev/tty
    printf '#: ' >/dev/tty
    IFS= read -r pick </dev/tty || exit 0
    PICKED=$(printf '%s\n' "$LIST" | sed -n "${pick}p")
    ;;
esac

[[ -z "$PICKED" ]] && exit 0
NS=$(echo "$PICKED"  | cut -f1)
NAME=$(echo "$PICKED" | cut -f2)
READY=$(echo "$PICKED" | cut -f3)

echo ""
echo "Deployment : $NAME"
echo "Namespace  : $NS"
echo "Replicas   : $READY"
echo ""
read -rp "Restart this deployment? [y/N] " CONFIRM
[[ "$CONFIRM" == "y" || "$CONFIRM" == "Y" ]] || { echo "Aborted."; exit 0; }

echo ""
echo ">>> kubectl rollout restart deployment/$NAME -n $NS"
kubectl rollout restart -n "$NS" deployment "$NAME"
echo ">>> watching rollout status (Ctrl+C to stop watching; restart continues)..."
kubectl rollout status -n "$NS" deployment "$NAME" || true
echo "Done."
