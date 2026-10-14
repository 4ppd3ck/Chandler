#!/bin/bash

set -euo pipefail

HOST="${1:-}"
[[ -z "$HOST" ]] && { echo "Usage: $0 <hostname> [resolver ...]"; exit 1; }

# Default resolver set: system, Google, Cloudflare, AWS Route53.
RESOLVERS=("system" "8.8.8.8" "1.1.1.1" "205.251.198.30")
if [[ $# -gt 1 ]]; then RESOLVERS=("${@:2}"); fi

printf "%-20s %s\n" "RESOLVER" "ADDRESSES"
for r in "${RESOLVERS[@]}"; do
  if [[ "$r" == "system" ]]; then
    out=$(getent hosts "$HOST" 2>/dev/null | awk '{print $1}' | tr '\n' ' ')
  else
    out=$(dig +short +time=2 +tries=1 @"$r" "$HOST" 2>/dev/null | tr '\n' ' ')
  fi
  printf "%-20s %s\n" "$r" "${out:-<no answer>}"
done
