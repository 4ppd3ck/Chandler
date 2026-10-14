#!/bin/bash

set -euo pipefail

# Port-forward manager: fzf-pick a service (or pod), choose the ports, run
# kubectl port-forward in the background — and keep track of every forward
# so you can list and stop them later. No more forgotten port-forwards.
#
# Usage:
#   k_forward.sh                          interactive: pick svc, choose ports
#   k_forward.sh pod                      interactive: pick a pod instead
#   k_forward.sh <name> <local:remote> [ns] [svc|pod]   non-interactive
#   k_forward.sh list                     show active forwards
#   k_forward.sh stop <pid|all>           stop one / all forwards

if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl is not installed. Exiting..."; exit 1
fi

REG_DIR="${TMPDIR:-/tmp}/k_forward"
mkdir -p "$REG_DIR"

CMD="${1:-}"

list_forwards() {
  local found=0
  for pidfile in "$REG_DIR"/*.pid; do
    [[ -e "$pidfile" ]] || continue
    local pid; pid=$(cat "$pidfile" 2>/dev/null) || continue
    if kill -0 "$pid" 2>/dev/null; then
      local meta; meta="${pidfile%.pid}.meta"
      printf 'pid %-8s %s\n' "$pid" "$(cat "$meta" 2>/dev/null || echo '?')"
      found=1
    else
      rm -f "$pidfile" "$meta"
    fi
  done
  [[ "$found" == 0 ]] && echo "(no active forwards)"
}

stop_forward() {
  local target="${1:-}"
  if [[ "$target" == "all" ]]; then
    for pidfile in "$REG_DIR"/*.pid; do
      [[ -e "$pidfile" ]] || continue
      local pid; pid=$(cat "$pidfile" 2>/dev/null) || continue
      kill "$pid" 2>/dev/null && echo "Stopped pid $pid" || echo "pid $pid not running"
      rm -f "$pidfile" "${pidfile%.pid}.meta"
    done
    return 0
  fi
  local pidfile="$REG_DIR/$target.pid"
  if [[ -e "$pidfile" ]]; then
    kill "$target" 2>/dev/null && echo "Stopped pid $target" || echo "pid $target not running"
    rm -f "$pidfile" "${pidfile%.pid}.meta"
  else
    echo "No forward with pid $target. Use: $0 list"
    exit 1
  fi
}

case "$CMD" in
  list) list_forwards; exit 0 ;;
  stop) stop_forward "${2:-all}"; exit 0 ;;
esac

TYPE="svc"
NAME="$CMD"
PORTS="${2:-}"
NS="${3:-}"

if [[ -z "$NAME" || "$NAME" == "pod" ]]; then
  TUI_SELECT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"

  rc=0
  TYPE_PICK=$(printf 'service\npod\n' | bash "$TUI_SELECT" \
                --title "k_forward | forward a service or a pod" --prompt "type> ") || rc=$?
  case "$rc" in
    0) [[ "$TYPE_PICK" == "pod" ]] && TYPE="pod" || TYPE="svc" ;;
    1) exit 0 ;;
    *)
      if [[ -f "$TUI_SELECT" ]]; then
        echo "Interactive picker needs a terminal. Non-interactive: $0 <name> <local:remote> [ns] [svc|pod]" >&2
        exit 1
      fi
      printf '1) service\n2) pod\n' >/dev/tty
      printf '#: ' >/dev/tty
      IFS= read -r pick </dev/tty || exit 0
      [[ "$pick" == "2" ]] && TYPE="pod" || TYPE="svc"
      ;;
  esac

  LIST=$(kubectl get "$TYPE"s -A -o jsonpath \
    '{range .items[*]}{.metadata.namespace}{"\t"}{.metadata.name}{"\n"}{end}' 2>/dev/null || true)
  [[ -z "$LIST" ]] && { echo "No ${TYPE}s found."; exit 1; }

  rc=0
  PICKED=$(printf '%s\n' "$LIST" | bash "$TUI_SELECT" \
             --title "k_forward | pick a $TYPE" --prompt "$TYPE> ") || rc=$?
  case "$rc" in
    0) ;;
    1) exit 0 ;;
    *)
      printf '%s\n' "$LIST" | nl -ba >/dev/tty
      printf '#: ' >/dev/tty
      IFS= read -r pick </dev/tty || exit 0
      PICKED=$(printf '%s\n' "$LIST" | sed -n "${pick}p")
      ;;
  esac
  NS=$(echo "$PICKED" | cut -f1)
  NAME=$(echo "$PICKED" | cut -f2)

  DEFAULT_REMOTE=""
  if [[ "$TYPE" == "svc" ]]; then
    SVC_PORTS=$(kubectl get svc "$NAME" -n "$NS" -o jsonpath \
      '{range .spec.ports[*]}{.port}{"/"}{.targetPort}{" ("}{.name}{") "}{"\n"}{end}' 2>/dev/null || true)
    if [[ -n "$SVC_PORTS" ]]; then
      echo "Service ports:"
      echo "$SVC_PORTS" | sed 's/^/  /'
      DEFAULT_REMOTE=$(echo "$SVC_PORTS" | head -1 | cut -d/ -f1)
    fi
  fi
  read -rp "Remote port [${DEFAULT_REMOTE:-80}]: " REMOTE
  REMOTE="${REMOTE:-$DEFAULT_REMOTE}"
  REMOTE="${REMOTE:-80}"
  read -rp "Local port [${REMOTE}]: " LOCAL
  LOCAL="${LOCAL:-$REMOTE}"
  PORTS="$LOCAL:$REMOTE"
else
  # Non-interactive: <name> <local:remote> [ns] [svc|pod]
  [[ -z "$PORTS" ]] && { echo "Usage: $0 <name> <local:remote> [ns] [svc|pod]"; exit 1; }
  TYPE="${4:-svc}"
  [[ -z "$NS" ]] && NS=$(kubectl config view --minify -o jsonpath='{..namespace}' 2>/dev/null || true)
  [[ -z "$NS" ]] && NS="default"
  LOCAL="${PORTS%%:*}"
  REMOTE="${PORTS##*:}"
fi

LOG="$REG_DIR/fwd_${NAME}_${LOCAL}.log"
echo "Forwarding localhost:${LOCAL} -> ${TYPE}/${NAME}:${REMOTE} (ns: $NS)"

nohup kubectl port-forward -n "$NS" "$TYPE/$NAME" "$LOCAL:$REMOTE" >"$LOG" 2>&1 &
PID=$!
sleep 1

if kill -0 "$PID" 2>/dev/null; then
  echo "$PID" > "$REG_DIR/$PID.pid"
  echo "localhost:${LOCAL} -> ${TYPE}/${NAME}:${REMOTE} (ns: $NS, pid: $PID)" > "$REG_DIR/$PID.meta"
  echo "Active. pid: $PID  (log: $LOG)"
  echo "Stop with: k_forward.sh stop $PID   (or 'stop all')"
else
  echo "Failed to start. Log output:"
  tail -5 "$LOG" || true
  rm -f "$LOG"
  exit 1
fi

echo ""
echo "Active forwards:"
list_forwards
