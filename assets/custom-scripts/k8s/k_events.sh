#!/bin/bash

set -euo pipefail

# Warning-events feed: what's actually going wrong on the cluster, newest
# first. Default shows WARNING events only; -a includes Normal; -w streams
# live. Great during incidents or "the cluster feels slow" moments.
#
# Usage:
#   k_events.sh                 last 30 warning events, all namespaces
#   k_events.sh -n <ns>         limit to one namespace
#   k_events.sh -a              include Normal events too
#   k_events.sh -w              live stream of warning events (Ctrl+C to stop)
#   k_events.sh -w -a           live stream, everything
#   k_events.sh 100             show last 100 instead of 30

if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl is not installed. Exiting..."; exit 1
fi

NS=""
ALL=0
WATCH=0
COUNT=30

while [[ $# -ge 1 ]]; do
  case "$1" in
    -n)        NS="$2"; shift ;;
    -A)        NS="" ;;
    -a|--all)  ALL=1 ;;
    -w|--watch) WATCH=1 ;;
    [0-9]*)    COUNT="$1" ;;
    *) echo "Usage: $0 [-n <ns>] [-a] [-w] [count]"; exit 1 ;;
  esac
  shift
done

NS_FLAG=()
[[ -n "$NS" ]] && NS_FLAG=(-n "$NS")

FIELDS=(--field-selector type=Warning)
[[ "$ALL" == 1 ]] && FIELDS=()

COLUMNS='LAST SEEN:.lastTimestamp,COUNT:.count,TYPE:.type,REASON:.reason,KIND:.involvedObject.kind,NAME:.involvedObject.name,MESSAGE:.message'

if [[ "$WATCH" == 1 ]]; then
  echo "Streaming events$( [[ "$ALL" == 0 ]] && echo ' (Warning only)' )... Ctrl+C to stop."
  exec kubectl get events ${NS_FLAG[@]+"${NS_FLAG[@]}"} ${FIELDS[@]+"${FIELDS[@]}"} --watch \
    -o custom-columns="$COLUMNS"
fi

kubectl get events ${NS_FLAG[@]+"${NS_FLAG[@]}"} ${FIELDS[@]+"${FIELDS[@]}"} \
  --sort-by=.lastTimestamp \
  -o custom-columns="$COLUMNS" 2>/dev/null | tail -n "$((COUNT + 1))"
