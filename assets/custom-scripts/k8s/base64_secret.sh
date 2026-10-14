#!/bin/bash

set -euo pipefail

# Base64 + k8s Secret helper.
#
#   b64 enc "value"              base64-encode a string
#   b64 dec "<base64>"           base64-decode a string
#   b64 kget <name> [ns]         decode every key in a k8s Secret
#   b64 kget <name> [ns] <key>   decode a single key from a k8s Secret

usage() {
  cat <<'EOF'
b64 - base64 / k8s secret helper
  b64 enc  "value"
  b64 dec  "<base64>"
  b64 kget <secret-name> [namespace] [key]
EOF
}

if [[ $# -lt 1 ]]; then usage; exit 0; fi

sub="$1"; shift
case "$sub" in
  enc)
    [[ -z "${1:-}" ]] && { echo "Need a value to encode."; exit 1; }
    printf '%s' "$1" | base64
    ;;
  dec)
    [[ -z "${1:-}" ]] && { echo "Need a value to decode."; exit 1; }
    printf '%s' "$1" | base64 -d 2>/dev/null || echo "(invalid base64)" >&2
    echo
    ;;
  kget)
    if ! command -v kubectl >/dev/null 2>&1; then
      echo "kubectl is not installed."; exit 1
    fi
    secret="${1:-}"; ns="${2:-}"; key="${3:-}"
    [[ -z "$secret" ]] && { echo "Need a secret name."; exit 1; }
    ns_args=(); [[ -n "$ns" ]] && ns_args=(-n "$ns")
    if [[ -n "$key" ]]; then
      kubectl get secret "${ns_args[@]}" "$secret" \
        -o jsonpath="{.data.$key}" | base64 -d 2>/dev/null
      echo
    else
      kubectl get secret "${ns_args[@]}" "$secret" \
        -o json | jq -r '.data // {} | to_entries[] | "\(.key) = \(.value|@base64d)"'
    fi
    ;;
  -h|--help|help) usage ;;
  *) echo "Unknown subcommand: $sub"; usage; exit 1 ;;
esac
