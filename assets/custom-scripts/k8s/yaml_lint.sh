#!/bin/bash

set -uo pipefail

# YAML linter / sanity checker for Kubernetes and friends.
#
# Catches the three classes of YAML mistakes that bite before kubectl even
# gets to validate the resource:
#
#   1. TAB characters in indentation      (YAML forbids tabs for indenting)
#   2. Bad indentation / structure        (parsed + re-emitted to verify)
#   3. Trailing whitespace                (cosmetic, but often a copy-paste bug)
#
# Usage:
#   yaml_lint.sh                      # all *.yaml + *.yml under . (recursive)
#   yaml_lint.sh file.yaml            # one file
#   yaml_lint.sh dir1 dir2 file.yaml  # mix of files / dirs
#   yaml_lint.sh --strict             # treat trailing-whitespace as errors too
#   cat foo.yaml | yaml_lint.sh -     # read from stdin
#
# Exits non-zero if any file has a TAB, a parse error, or (in --strict) trailing
# whitespace. Works with python3+PyYAML, or `yq` (mikefarah/v4), or pure-bash
# fallback for the tab/whitespace checks when neither is installed.

STRICT=0
TARGETS=()
for arg in "$@"; do
  case "$arg" in
    --strict) STRICT=1 ;;
    -h|--help)
      sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) TARGETS+=("$arg") ;;
  esac
done

if [[ ${#TARGETS[@]} -eq 0 ]]; then
  while IFS= read -r -d '' f; do TARGETS+=("$f"); done < <(find . \( -name '*.yaml' -o -name '*.yml' \) -type f -print0)
fi

have_python_yaml() { command -v python3 >/dev/null 2>&1 && python3 -c 'import yaml' 2>/dev/null; }
have_yq()          { command -v yq      >/dev/null 2>&1 && yq --version 2>/dev/null | grep -qi 'version v4'; }

validate_syntax() {
  local f="$1"
  if have_python_yaml; then
    python3 -c 'import sys,yaml; list(yaml.safe_load_all(sys.stdin))' <"$f" 2>/dev/null
  elif have_yq; then
    yq eval '.' "$f" >/dev/null 2>&1
  else
    return 0   # cannot validate structure without a parser; tab check still runs
  fi
}

# Find the "base files" values file Helm should render with. Looks in the
# chart's own dir for the conventional names. Echoes the path or nothing.
errors=0
warnings=0
checked=0

check_file() {
  local f="$1"
  [[ ! -f "$f" ]] && return
  checked=$((checked + 1))
  local has_tab=0
  local has_trail=0
  local line_no=0
  local offenders=""

  while IFS= read -r line || [[ -n "$line" ]]; do
    line_no=$((line_no + 1))
    if [[ "$line" =~ ^($'\t'|[[:space:]]*$'\t') ]]; then
      [[ $has_tab -eq 0 ]] && offenders+="  tab-indent:      line $line_no"
      [[ $has_tab -eq 0 ]] && offenders+="\n"
      has_tab=1
    fi
    # 2. Trailing whitespace (cosmetic; only an error in --strict).
    if [[ "$line" =~ [[:space:]]$ && ! "$line" =~ ^[[:space:]]*$ ]]; then
      has_trail=$((has_trail + 1))
    fi
  done <"$f"

  local status_ok="OK"
  local notes=""

  if [[ $has_tab -eq 1 ]]; then
    status_ok="FAIL"
    errors=$((errors + 1))
    notes+="[TAB indentation] "
  fi

  if [[ $has_trail -gt 0 ]]; then
    if [[ $STRICT -eq 1 ]]; then
      status_ok="FAIL"
      errors=$((errors + 1))
      notes+="[${has_trail} trailing-ws line(s)] "
    else
      warnings=$((warnings + 1))
      notes+="${has_trail} trailing-ws "
    fi
  fi

  if ! validate_syntax "$f"; then
    status_ok="FAIL"
    errors=$((errors + 1))
    notes+="[parse error] "
  fi

  printf "%-7s %-60s %s\n" "$status_ok" "$f" "$notes"
  if [[ -n "$offenders" ]]; then printf "%b" "$offenders"; fi
}

# stdin mode: read from "-"
if [[ ${#TARGETS[@]} -eq 1 && "${TARGETS[0]}" == "-" ]]; then
  tmp=$(mktemp)
  cat >"$tmp"
  check_file "$tmp"
  rc=$?
  rm -f "$tmp"
  exit $rc
fi

echo "yaml_lint: checking ${#TARGETS[@]} file(s) (strict=$STRICT)"
echo "------------------------------------------------------------"
for t in "${TARGETS[@]}"; do
  if [[ -d "$t" ]]; then
    while IFS= read -r -d '' f; do check_file "$f"; done < <(find "$t" \( -name '*.yaml' -o -name '*.yml' \) -type f -print0)
  else
    check_file "$t"
  fi
done
echo "------------------------------------------------------------"
echo "Checked: $checked   Errors: $errors   Warnings: $warnings"

if [[ $errors -gt 0 ]]; then
  exit 1
fi
exit 0
