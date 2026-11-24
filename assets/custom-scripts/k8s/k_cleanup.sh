#!/bin/bash

set -euo pipefail

# Cluster janitor: lists (and optionally deletes, with confirmation) the usual
# clutter — Evicted/Failed pods, Completed Jobs, old ReplicaSets from finished
# rollouts, dangling PVCs.

if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl is not installed. Exiting..."; exit 1
fi

DRY="${1:-list}"   # "list" (default) or "delete"

if [[ "$DRY" != "list" && "$DRY" != "delete" ]]; then
  echo "Usage: $0 [list|delete]"
  exit 1
fi

banner() { echo ""; echo "====== $* ======"; }
act() {
  local what="$1"; shift
  if [[ "$DRY" == "delete" ]]; then
    echo "DELETE: $what"
    kubectl delete "$@" --ignore-not-found=true || true
  else
    echo "would delete: $what"
  fi
}

banner "Evicted / Failed pods"
# mapfile is bash 4+; macOS ships /bin/bash 3.2 and this script runs under it
# via its shebang, so the arrays fill with a read loop. The ${arr[@]+...}
# guards keep an empty result from tripping set -u (bash 3.2 expands an empty
# array as unbound).
EVICTED=()
while IFS= read -r p_line || [[ -n "$p_line" ]]; do EVICTED+=( "$p_line" ); done < <(kubectl get pods -A --field-selector=status.phase=Failed \
  -o jsonpath='{range .items[?(@.status.phase=="Failed")]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}' 2>/dev/null || true)
for p in ${EVICTED[@]+"${EVICTED[@]}"}; do
  [[ -z "$p" ]] && continue
  ns="${p%%/*}"; name="${p#*/}"
  act "$p" pod -n "$ns" "$name"
done
[[ ${#EVICTED[@]} -eq 0 ]] && echo "(none)"

banner "Completed Jobs"
JOBS=()
while IFS= read -r j_line || [[ -n "$j_line" ]]; do JOBS+=( "$j_line" ); done < <(kubectl get jobs -A -o jsonpath='{range .items[?(@.status.succeeded)]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}' 2>/dev/null || true)
for j in ${JOBS[@]+"${JOBS[@]}"}; do
  [[ -z "$j" ]] && continue
  ns="${j%%/*}"; name="${j#*/}"
  act "$j" job -n "$ns" "$name"
done
[[ ${#JOBS[@]} -eq 0 ]] && echo "(none)"

banner "Old ReplicaSets (scale 0, from finished rollouts)"
RS=()
while IFS= read -r r_line || [[ -n "$r_line" ]]; do RS+=( "$r_line" ); done < <(kubectl get rs -A -o jsonpath='{range .items[?(@.spec.replicas==0)]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}' 2>/dev/null || true)
for r in ${RS[@]+"${RS[@]}"}; do
  [[ -z "$r" ]] && continue
  ns="${r%%/*}"; name="${r#*/}"
  act "$r" rs -n "$ns" "$name"
done
[[ ${#RS[@]} -eq 0 ]] && echo "(none)"

banner "Done. (mode: $DRY)"
