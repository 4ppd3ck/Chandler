#!/bin/bash

set -uo pipefail

# Helm chart lint + render helper.
#
# Two jobs in one script:
#
#   1. LINT   - run `helm lint` on a chart and surface the result.
#   2. RENDER - `helm template` the chart using a base values file so you can
#               eyeball or diff the manifests without installing anything.
#
# The "base files" idea: many teams keep a non-secret baseline values file in
# the chart under a folder like `base-files/`, `base/`, or just `values.yaml`.
# This script auto-detects those conventions, OR you can pass an explicit file.
#
# Usage:
#   helm_lint.sh                          # fzf-pick a chart dir under . then lint
#   helm_lint.sh ./my-chart               # lint a specific chart
#   helm_lint.sh ./my-chart --render      # lint + render with base values
#   helm_lint.sh ./my-chart --render -f my-overrides.yaml
#   helm_lint.sh ./my-chart --values base-files/prod.yaml
#
# Render output goes to stdout (pipe to a file with `> out.yaml`).
# Lint output goes to stderr so a `> out.yaml` redirect stays clean.

if ! command -v helm >/dev/null 2>&1; then
  echo "helm is not installed. Exiting..." >&2
  exit 1
fi

CHART=""
DO_RENDER=0
EXTRA_VALUES=()
SHOW_HELP=0
POSITIONAL=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) SHOW_HELP=1; shift ;;
    --render|-r) DO_RENDER=1; shift ;;
    -f|--values) EXTRA_VALUES+=("$2"); shift 2 ;;
    --values=*) EXTRA_VALUES+=("${1#*=}"); shift ;;
    -f*) EXTRA_VALUES+=("${1#-f}"); shift ;;
    *) POSITIONAL+=("$1"); shift ;;
  esac
done
set -- "${POSITIONAL[@]}"

if [[ $SHOW_HELP -eq 1 ]]; then
  sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
fi

if [[ $# -ge 1 ]]; then
  CHART="$1"
else
  tui="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"
  echo "Searching for charts (Chart.yaml) under $(pwd)..."
  chart_list=$(find . -maxdepth 4 -type f -name Chart.yaml -print0 \
    | xargs -0 -n1 dirname 2>/dev/null || true)
  if [[ -z "$chart_list" ]]; then
    echo "No Chart.yaml found under $(pwd)."
    echo "Usage: $0 <chart-dir> [--render [-f values.yaml ...]]"
    exit 1
  fi
  rc=0
  chart_file=$(printf '%s\n' "$chart_list" \
    | bash "$tui" --title "helm_lint | pick a chart" --prompt "chart> ") || rc=$?
  case "$rc" in
    0) [[ -z "$chart_file" ]] && { echo "No chart selected."; exit 0; }
       CHART="$chart_file" ;;
    1) echo "No chart selected."; exit 0 ;;
    *) echo "Interactive picker needs a terminal."
       echo "Usage: $0 <chart-dir> [--render [-f values.yaml ...]]"
       exit 1 ;;
  esac
fi

if [[ ! -d "$CHART" ]]; then
  echo "Chart directory not found: $CHART" >&2
  exit 1
fi
if [[ ! -f "$CHART/Chart.yaml" ]]; then
  echo "Not a Helm chart (no Chart.yaml): $CHART" >&2
  exit 1
fi

BASE_VALUES=()
if [[ ${#EXTRA_VALUES[@]} -eq 0 ]]; then
  env_name="${HELM_ENV:-base}"
  for cand in \
      "$CHART/base-files/${env_name}.yaml" \
      "$CHART/base-files/base.yaml" \
      "$CHART/base-files/default.yaml" \
      "$CHART/base/base.yaml" \
      "$CHART/base.yaml"; do
    if [[ -f "$cand" ]]; then
      BASE_VALUES+=("$cand")
      echo "(auto-detected base values: $cand)" >&2
      break
    fi
  done
fi
ALL_VALUES=("${EXTRA_VALUES[@]}" "${BASE_VALUES[@]}")

values_args=()
for vf in "${ALL_VALUES[@]}"; do
  if [[ ! -f "$vf" ]]; then
    echo "Values file not found, skipping: $vf" >&2
    continue
  fi
  values_args+=(-f "$vf")
done

# ---- 1. LINT -----------------------------------------------------------------
echo "== helm lint $CHART ${values_args[*]}" >&2
if helm lint "$CHART" "${values_args[@]}" >&2; then
  echo "[lint] OK" >&2
  lint_rc=0
else
  echo "[lint] FAILED" >&2
  lint_rc=1
fi

# ---- 2. RENDER (optional) ----------------------------------------------------
if [[ $DO_RENDER -eq 1 ]]; then
  echo "== helm template $CHART ${values_args[*]}" >&2
  release="${HELM_RELEASE:-release}"
  if ! helm template "$release" "$CHART" "${values_args[@]}"; then
    echo "[render] FAILED" >&2
    exit 1
  fi
  echo "[render] OK -> stdout" >&2
fi

exit $lint_rc
