#!/bin/bash

set -euo pipefail

# One-shot pod doctor: describe + events + current/previous logs + restart
# count and last termination reason. Great for "why is this pod crashing?".
#
# Usage:
#   k_doctor.sh <pod-name> [namespace]
#   k_doctor.sh <pod-name> [namespace] logs      # just the logs
#   k_doctor.sh <pod-name> [namespace] prev      # previous-container logs

if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl is not installed. Exiting..."; exit 1
fi

POD="${1:-}"
NS="${2:-}"
SLICE="${3:-}"

if [[ -z "$POD" ]]; then
  TUI_SELECT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"
  LIST=$(kubectl get pods -A -o jsonpath \
    '{range .items[*]}{.metadata.namespace}{"/"}{.metadata.name}{"\t"}{.status.phase}{"\n"}{end}' \
    2>/dev/null || true)
  if [[ -z "$LIST" ]]; then
    echo "Usage: $0 <pod-name> [namespace] [logs|prev]"
    echo "(no pods found for the interactive pick)"
    exit 1
  fi
  rc=0
  PICKED=$(printf '%s\n' "$LIST" | bash "$TUI_SELECT" --title "k_doctor | pick a pod" --prompt "pod> ") || rc=$?
  case "$rc" in
    0)
      full="${PICKED%%$'\t'*}"
      NS="${full%%/*}"
      POD="${full#*/}"
      ;;
    1) exit 0 ;;
    *)
      [[ -f "$TUI_SELECT" ]] && echo "Interactive picker needs a terminal." >&2
      echo "Usage: $0 <pod-name> [namespace] [logs|prev]"
      exit 1
      ;;
  esac
fi

if [[ -z "$NS" || "$NS" == "logs" || "$NS" == "prev" ]]; then
  SLICE="${NS:-}"
  NS=""
fi

NS_FLAG=()
[[ -n "$NS" ]] && NS_FLAG=(-n "$NS")

echo "==================== DOCTOR: $POD ===================="
if [[ -n "$NS" ]]; then echo "Namespace : $NS"; fi
echo "Started   : $(kubectl get pod ${NS_FLAG[@]+"${NS_FLAG[@]}"} "$POD" \
  -o jsonpath='{.status.startTime}' 2>/dev/null || echo '?')"
echo "Phase     : $(kubectl get pod ${NS_FLAG[@]+"${NS_FLAG[@]}"} "$POD" \
  -o jsonpath='{.status.phase}' 2>/dev/null || echo '?')"
echo "Restarts  : $(kubectl get pod ${NS_FLAG[@]+"${NS_FLAG[@]}"} "$POD" \
  -o jsonpath='{.status.containerStatuses[0].restartCount}' 2>/dev/null || echo '?')"
echo "Last term : $(kubectl get pod ${NS_FLAG[@]+"${NS_FLAG[@]}"} "$POD" \
  -o jsonpath='{.status.containerStatuses[0].lastState.terminated.reason}' 2>/dev/null || echo 'none')"
echo "Exit code : $(kubectl get pod ${NS_FLAG[@]+"${NS_FLAG[@]}"} "$POD" \
  -o jsonpath='{.status.containerStatuses[0].lastState.terminated.exitCode}' 2>/dev/null || echo '-')"
echo "======================================================"

case "${SLICE:-full}" in
  logs)
    echo ">>> current logs:"
    kubectl logs ${NS_FLAG[@]+"${NS_FLAG[@]}"} "$POD" --tail=80 || true
    ;;
  prev)
    echo ">>> previous-container logs (the crash reason):"
    kubectl logs ${NS_FLAG[@]+"${NS_FLAG[@]}"} "$POD" --previous --tail=80 || true
    ;;
  *)
    echo ""
    echo ">>> Events for $POD:"
    kubectl describe pod ${NS_FLAG[@]+"${NS_FLAG[@]}"} "$POD" | sed -n '/^Events:/,$p' | head -40 || true
    echo ""
    echo ">>> Current logs (tail 40):"
    kubectl logs ${NS_FLAG[@]+"${NS_FLAG[@]}"} "$POD" --tail=40 || true
    echo ""
    echo ">>> Previous logs (tail 40):"
    kubectl logs ${NS_FLAG[@]+"${NS_FLAG[@]}"} "$POD" --previous --tail=40 2>/dev/null \
      || echo "(no previous container)"
    ;;
esac
