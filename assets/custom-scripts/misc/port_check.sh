#!/bin/bash

set -euo pipefail

if [[ $# -ge 1 ]]; then
  LIST="$1"
  if [[ ! -f "$LIST" ]]; then
    echo "File not found: $LIST"; exit 1
  fi
  INPUT=$(cat "$LIST")
else
  INPUT=$(cat)
fi

[[ -z "$INPUT" ]] && { echo "No input. Provide host:port lines."; exit 1; }

printf "%-40s %-8s %s\n" "TARGET" "PORT" "RESULT"
while IFS= read -r line; do
  [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
  host="${line%:*}"; port="${line##*:}"
  if timeout 3 bash -c ">/dev/tcp/$host/$port" 2>/dev/null; then
    printf "%-40s %-8s %s\n" "$host" "$port" "OK"
  else
    printf "%-40s %-8s %s\n" "$host" "$port" "FAIL"
  fi
done <<<"$INPUT"
