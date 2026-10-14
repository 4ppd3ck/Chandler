#!/bin/bash

set -euo pipefail

# Pod resource hogs: kubectl top pods sorted by CPU or memory, biggest first.
# Catches the noisy neighbours that plain `kubectl top` (alphabetical) hides.
#
# Usage:
#   k_top.sh                     interactive: fzf-pick namespace, sort by CPU
#   k_top.sh mem                 sort by memory instead
#   k_top.sh -n <ns> [cpu|mem]   skip the picker, use this namespace
#   k_top.sh -A [cpu|mem]        all namespaces
#   k_top.sh ... <count>         show only the top N (default 20)
#
# Requires metrics-server on the cluster (the script tells you if missing).

if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl is not installed. Exiting..."; exit 1
fi

SORT_KEY="cpu"
COUNT=20
NS=""

while [[ $# -ge 1 ]]; do
  case "$1" in
    cpu|mem)  SORT_KEY="$1" ;;
    -n)       NS="$2"; shift ;;
    -A|--all) NS="-A" ;;
    [0-9]*)   COUNT="$1" ;;
    *)
      echo "Usage: $0 [-n <ns>|-A] [cpu|mem] [count]"
      exit 1
      ;;
  esac
  shift
done

if [[ -z "$NS" ]]; then
  TUI_SELECT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"
  NS_RAW=$( { echo "ALL-NAMESPACES"; kubectl get ns -o name 2>/dev/null | sed 's|namespace/||'; } )
  rc=0
  picked=$(printf '%s\n' "$NS_RAW" | bash "$TUI_SELECT" --title "k_top | pick a namespace" --prompt "ns> ") || rc=$?
  case "$rc" in
    0) [[ "$picked" == "ALL-NAMESPACES" ]] && NS="-A" || NS="$picked" ;;
    1) exit 0 ;;
    *)
      if [[ -f "$TUI_SELECT" ]]; then
        echo "Interactive picker needs a terminal. Use: $0 -n <ns> | -A" >&2
        exit 1
      fi
      printf '%s\n' "$NS_RAW" | nl -ba >/dev/tty
      printf '#: ' >/dev/tty
      IFS= read -r pick </dev/tty || exit 0
      picked=$(printf '%s\n' "$NS_RAW" | sed -n "${pick}p")
      [[ -z "$picked" ]] && exit 0
      [[ "$picked" == "ALL-NAMESPACES" ]] && NS="-A" || NS="$picked"
      ;;
  esac
fi

NS_FLAG=()
ALL=0
if [[ "$NS" == "-A" ]]; then
  NS_FLAG=(-A); ALL=1
else
  NS_FLAG=(-n "$NS")
fi

RAW=$(kubectl top pods ${NS_FLAG[@]+"${NS_FLAG[@]}"} --no-headers 2>&1) || {
  echo "kubectl top failed:"
  echo "$RAW"
  echo ""
  echo "Hint: this needs metrics-server installed on the cluster."
  exit 1
}

normalize_cpu() {
  local v="$1"
  if [[ "$v" == *m ]]; then echo "${v%m}"
  elif [[ "$v" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then awk "BEGIN{printf \"%d\", $v*1000}"
  else echo 0; fi
}
normalize_mem() {
  local v="$1"
  if [[ "$v" == *Gi ]]; then awk "BEGIN{printf \"%d\", ${v%Gi}*1024}"
  elif [[ "$v" == *Ki ]]; then awk "BEGIN{printf \"%d\", ${v%Ki}/1024}"
  elif [[ "$v" == *Mi ]]; then echo "${v%Mi}"
  elif [[ "$v" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then awk "BEGIN{printf \"%d\", $v/1048576}"
  else echo 0; fi
}

{
  while read -r line; do
    [[ -z "$line" ]] && continue
    if [[ "$ALL" == 1 ]]; then
      read -r ns name cpu mem extra <<<"$line"
    else
      ns="(ctx)"; read -r name cpu mem extra <<<"$line"
    fi
    if [[ "$SORT_KEY" == "mem" ]]; then
      val=$(normalize_mem "$mem")
    else
      val=$(normalize_cpu "$cpu")
    fi
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$val" "$ns" "$name" "$cpu" "$mem" "$extra"
  done <<<"$RAW"
} | sort -t$'\t' -k1,1nr | head -n "$COUNT" | {
  title="TOP $COUNT PODS BY $SORT_KEY"
  [[ "$ALL" == 1 ]] && title="$title (all namespaces)" || title="$title (ns: $NS)"
  printf '\n== %s ==\n' "$title"
  if [[ "$ALL" == 1 ]]; then
    printf '%-16s %-52s %10s %10s\n' "NAMESPACE" "POD" "CPU" "MEM"
    while IFS=$'\t' read -r val ns name cpu mem extra; do
      printf '%-16s %-52s %10s %10s\n' "$ns" "$name" "$cpu" "$mem"
    done
  else
    printf '%-60s %10s %10s\n' "POD" "CPU" "MEM"
    while IFS=$'\t' read -r val ns name cpu mem extra; do
      printf '%-60s %10s %10s\n' "$name" "$cpu" "$mem"
    done
  fi
}
