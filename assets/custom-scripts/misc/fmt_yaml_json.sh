#!/bin/bash

set -euo pipefail

# Universal pretty-printer and converter for JSON / YAML.
# Auto-detects input, validates syntax, and can convert between formats.
#
#   cat foo.json | fmt              auto-detect + pretty-print
#   cat foo.yaml | fmt json         convert YAML -> JSON
#   cat foo.json | fmt yaml         convert JSON -> YAML
#   echo '{"a":1}' | fmt
#
# Requires python3 (ships with both yaml & json support via stdlib + PyYAML
# for YAML). Falls back to jq if python3 is unavailable.

OUT="${1:-auto}"

have_python() { command -v python3 >/dev/null 2>&1; }
have_jq()     { command -v jq >/dev/null 2>&1; }
have_yq()     { command -v yq >/dev/null 2>&1; }

INPUT=$(cat)

if [[ -z "$INPUT" ]]; then
  echo "No input received on stdin."; exit 1
fi

is_json() {
  if have_jq; then echo "$INPUT" | jq -e . >/dev/null 2>&1
  elif have_python; then echo "$INPUT" | python3 -c 'import sys,json; json.load(sys.stdin)' >/dev/null 2>&1
  else return 1; fi
}

if [[ "$OUT" == "auto" ]]; then
  if is_json; then OUT="json"; else OUT="yaml"; fi
fi

case "$OUT" in
  json)
    if have_python && python3 -c 'import yaml' 2>/dev/null; then
      echo "$INPUT" | python3 -c 'import sys,json,yaml; print(json.dumps(yaml.safe_load(sys.stdin), indent=2))'
    elif have_yq; then
      echo "$INPUT" | yq -o=json
    elif have_jq; then
      echo "$INPUT" | jq .
    else
      echo "Need python3(with yaml), yq, or jq to emit JSON."; exit 1
    fi
    ;;
  yaml|yml)
    if have_python && python3 -c 'import yaml' 2>/dev/null; then
      echo "$INPUT" | python3 -c 'import sys,json,yaml; yaml.safe_dump(json.load(sys.stdin), sys.stdout, default_flow_style=False, sort_keys=False)'
    elif have_yq; then
      echo "$INPUT" | yq -o=yaml
    else
      echo "Need python3(with PyYAML) or yq to emit YAML."; exit 1
    fi
    ;;
  *)
    echo "Usage: fmt [auto|json|yaml]"; exit 1
    ;;
esac
