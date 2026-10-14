#!/bin/bash

set -euo pipefail

# Decode a JWT (base64url header.payload[.signature]) into pretty JSON.
#   echo "<jwt>" | decode_jwt.sh
#   decode_jwt.sh "<jwt>"

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is not installed."; exit 1
fi

# Read JWT from arg or stdin.
if [[ -n "${1:-}" ]]; then JWT="$1"; else JWT=$(cat); fi
JWT="${JWT#Bearer }"           # tolerate "Bearer <token>"
JWT="$(echo -n "$JWT" | tr -d '[:space:]')"
[[ -z "$JWT" ]] && { echo "No JWT provided."; exit 1; }

b64url_to_b64() {
  local s="$1"
  s="${s//-/+}"; s="${s//_/\/}"
  case ${#s} in
    2) s+="==" ;;
    3) s+="=" ;;
  esac
  printf '%s' "$s"
}

IFS='.' read -r header payload sig <<<"$JWT"
if [[ -z "$header" || -z "$payload" ]]; then
  echo "Not a valid JWT (expected 3 dot-separated parts)."; exit 1
fi

echo "=== Header ==="
echo -n "$(b64url_to_b64 "$header")" | base64 -d 2>/dev/null | jq .
echo "=== Payload ==="
echo -n "$(b64url_to_b64 "$payload")" | base64 -d 2>/dev/null | jq .
if [[ -n "$sig" ]]; then
  echo "=== Signature (base64url, truncated) ==="
  echo "$sig" | head -c 32; echo "..."
fi
