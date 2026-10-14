#!/bin/bash

set -uo pipefail

# kustomization.yaml validator.
#
# Verifies that a Kustomize bundle is internally consistent:
#   - kustomization.yaml (or kustomization.yml) exists in the target dir
#   - all `resources:` / `components:` it references actually exist on disk
#   - the standard fields it should have are present (apiVersion, kind)
#   - optionally: `kustomize build` succeeds (catches overlay errors, patches
#     that don't apply, missing namespaces, etc.) — needs the `kustomize` binary
#     or `kubectl kustomize`.
#
# Usage:
#   kustomize_check.sh                 # find every kustomization.* under .
#   kustomize_check.sh path/           # check the bundle rooted at path/
#   kustomize_check.sh path/ --build   # also run `kustomize build`
#   kustomize_check.sh --build         # find + build every bundle under .
#
# Exit code is non-zero if any bundle fails validation.

BUILD=0
TARGETS=()
for arg in "$@"; do
  case "$arg" in
    --build|-b) BUILD=1 ;;
    -h|--help)
      sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) TARGETS+=("$arg") ;;
  esac
done

find_kustomization() {
  local d="$1"
  [[ ! -d "$d" ]] && d="$(dirname "$d")"
  local tried="$d"
  while [[ -n "$tried" && "$tried" != "/" ]]; do
    for name in kustomization.yaml kustomization.yml Kustomization; do
      if [[ -f "$tried/$name" ]]; then echo "$tried/$name"; return 0; fi
    done
    tried="$(dirname "$tried")"
  done
  return 1
}

have_kustomize() { command -v kustomize >/dev/null 2>&1; }
have_kubectl()   { command -v kubectl   >/dev/null 2>&1; }
have_python_yaml() { command -v python3 >/dev/null 2>&1 && python3 -c 'import yaml' 2>/dev/null; }

read_list_field() {
  local file="$1" key="$2"
  awk -v k="$key" '
    /^[[:space:]]*#/ { next }
    $1 == k ":" {
      in_list = 1
      next
    }
    in_list {
      if (/^[[:space:]]*-[[:space:]]+/) {
        sub(/^[[:space:]]*-[[:space:]]+/, "")
        sub(/[[:space:]]*#.*$/, "")
        if ($0 != "") print $0
        next
      }
      # A non-dash line at the original indent ends the list.
      if (/^[^[:space:]]/) { in_list = 0 }
    }
  ' "$file"
}

errors=0
bundles=0

check_bundle() {
  local kfile="$1"
  local dir
  dir="$(dirname "$kfile")"
  bundles=$((bundles + 1))
  local bundle_err=0
  local notes=""

  # 1. Required top-level fields.
  for field in apiVersion kind; do
    if ! grep -Eq "^[[:space:]]*${field}[[:space:]]*:" "$kfile"; then
      notes+="[missing: ${field}] "
      bundle_err=1
    fi
  done

  # 2. kind should be Kustomization.
  local kind
  kind=$(awk -F':' '/^[[:space:]]*kind[[:space:]]*:/ {gsub(/[[:space:]]/,"",$2); print $2; exit}' "$kfile")
  if [[ -n "$kind" && "$kind" != "Kustomization" ]]; then
    notes+="[kind=$kind, expected Kustomization] "
    bundle_err=1
  fi

  # 3. Every referenced resource/component must exist.
  local missing=0
  while IFS= read -r ref; do
    [[ -z "$ref" ]] && continue
    if [[ ! -e "$dir/$ref" ]]; then
      notes+="[missing ref: $ref] "
      missing=1
    fi
  done < <(read_list_field "$kfile" "resources")
  while IFS= read -r ref; do
    [[ -z "$ref" ]] && continue
    ref="${ref#/}"
    if [[ ! -e "$dir/$ref" ]]; then
      notes+="[missing component: $ref] "
      missing=1
    fi
  done < <(read_list_field "$kfile" "components")
  [[ $missing -eq 1 ]] && bundle_err=1

  # 4. Structural parse of the kustomization itself.
  if have_python_yaml; then
    if ! python3 -c 'import sys,yaml; list(yaml.safe_load_all(sys.stdin))' <"$kfile" >/dev/null 2>&1; then
      notes+="[kustomization.yaml parse error] "
      bundle_err=1
    fi
  fi

  # 5. Optional: actually build it.
  if [[ $BUILD -eq 1 ]]; then
    if have_kustomize; then
      if ! kustomize build "$dir" >/dev/null 2>"$dir/.kustomize_check.err" 2>&1; then
        notes+="[kustomize build FAILED: $(head -1 "$dir/.kustomize_check.err" 2>/dev/null)] "
        bundle_err=1
        rm -f "$dir/.kustomize_check.err"
      fi
    elif have_kubectl; then
      if ! kubectl kustomize "$dir" >/dev/null 2>&1; then
        notes+="[kubectl kustomize FAILED] "
        bundle_err=1
      fi
    else
      notes+="[--build skipped: neither kustomize nor kubectl installed] "
    fi
  fi

  if [[ $bundle_err -eq 1 ]]; then
    errors=$((errors + 1))
    printf "%-7s %s\n" "FAIL" "$kfile"
    [[ -n "$notes" ]] && echo "         $notes"
  else
    printf "%-7s %s\n" "OK" "$kfile"
  fi
}

# Build the list of kustomization files to check.
KFILES=()
if [[ ${#TARGETS[@]} -eq 0 ]]; then
  while IFS= read -r -d '' f; do KFILES+=("$f"); done \
    < <(find . \( -name 'kustomization.yaml' -o -name 'kustomization.yml' -o -name 'Kustomization' \) -type f -print0)
else
  for t in "${TARGETS[@]}"; do
    if [[ "$t" == "--build" || "$t" == "-b" ]]; then continue; fi
    if kf=$(find_kustomization "$t"); then
      KFILES+=("$kf")
    else
      echo "FAIL   $t (no kustomization.{yaml,yml} found here or above)"
      errors=$((errors + 1))
    fi
  done
fi

if [[ ${#KFILES[@]} -eq 0 ]]; then
  echo "No kustomization.{yaml,yml} files found."
  exit 0
fi

echo "kustomize_check: ${#KFILES[@]} bundle(s) (build=$BUILD)"
echo "------------------------------------------------------------"
for kf in "${KFILES[@]}"; do check_bundle "$kf"; done
echo "------------------------------------------------------------"
echo "Bundles: $bundles   Failed: $errors"

[[ $errors -gt 0 ]] && exit 1
exit 0
