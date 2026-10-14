#!/bin/bash
# Render a Helm chart locally with `helm template` using an environment
# values file — the "does my chart actually render for stg?" check, without
# installing anything.
#
#   helm_template_test.sh                              pick chart + values (TUI)
#   helm_template_test.sh .                            chart = here, pick values
#   helm_template_test.sh . -e stg                     = helm template test . -f values-stg.yaml
#   helm_template_test.sh ./my-chart -e prod -r myapp  custom release name
#   helm_template_test.sh . -e stg -f overrides.yaml --set image.tag=dev
#
# Rendered manifests go to stdout (redirect with > out.yaml to save).
# Extra values files: repeat -f. Extra --set: repeat --set.

set -euo pipefail

RELEASE="test"
CHART=""
ENV_NAME=""
EXTRA_ARGS=()

usage() { sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; }

if ! command -v helm >/dev/null 2>&1; then
  echo "helm is not installed. Exiting..." >&2
  exit 1
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage; exit 0 ;;
    -r|--release)   RELEASE="$2"; shift 2 ;;
    -e|--env)       ENV_NAME="$2"; shift 2 ;;
    -f|--values)    EXTRA_ARGS+=(-f "$2"); shift 2 ;;
    --set)          EXTRA_ARGS+=(--set "$2"); shift 2 ;;
    -n|--namespace) EXTRA_ARGS+=(--namespace "$2"); shift 2 ;;
    -*)             echo "Unknown option: $1" >&2; usage; exit 1 ;;
    *)  CHART="${1:-$CHART}"; shift ;;
  esac
done

if [[ -z "$CHART" ]]; then
  tui="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"
  echo "Searching for charts (Chart.yaml) under $(pwd)..."
  chart_list=$(find . -maxdepth 4 -type f -name Chart.yaml -print0 \
    | xargs -0 -n1 dirname 2>/dev/null || true)
  if [[ -z "$chart_list" ]]; then
    echo "No Chart.yaml found under $(pwd)."
    echo "Usage: $0 [chart-dir] [-e <env>] [-f values.yaml ...] [--set k=v ...]"
    exit 1
  fi
  rc=0
  CHART=$(printf '%s\n' "$chart_list" \
    | bash "$tui" --title "helm_template_test | pick a chart" --prompt "chart> ") || rc=$?
  case "$rc" in
    0) [[ -z "$CHART" ]] && { echo "No chart selected."; exit 0; } ;;
    1) echo "No chart selected."; exit 0 ;;
    *) echo "Interactive picker needs a terminal."
       echo "Usage: $0 [chart-dir] [-e <env>] [-f values.yaml ...] [--set k=v ...]"
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

VALUE_FILES=()
while IFS= read -r vf; do
  VALUE_FILES+=("$vf")
done < <(find "$CHART" -maxdepth 2 -type f \( -name 'values*.yaml' -o -name 'values*.yml' \) \
  2>/dev/null | sort || true)

pick_values_file() {
  local env="$1" candidate
  if [[ -n "$env" ]]; then
    for candidate in "${VALUE_FILES[@]:-}"; do
      [[ -z "$candidate" ]] && continue
      if [[ "$(basename "$candidate")" == "values-$env.yaml" || "$(basename "$candidate")" == "values-$env.yml" ]]; then
        printf '%s' "$candidate"
        return 0
      fi
    done
    for candidate in "${VALUE_FILES[@]:-}"; do
      [[ -z "$candidate" ]] && continue
      if [[ "$(basename "$candidate")" == "values.$env.yaml" || "$(basename "$candidate")" == "values.$env.yml" ]]; then
        printf '%s' "$candidate"
        return 0
      fi
    done
    return 1
  fi
  return 2
}

VALUES_FILE=""
if [[ -n "$ENV_NAME" ]]; then
  if ! VALUES_FILE=$(pick_values_file "$ENV_NAME"); then
    echo "No values file for env '$ENV_NAME' in $CHART." >&2
    echo "Available values files:" >&2
    printf '  %s\n' "${VALUE_FILES[@]:-none}" >&2
    exit 1
  fi
else
  if (( ${#VALUE_FILES[@]} == 1 )); then
    VALUES_FILE="${VALUE_FILES[0]}"
  elif (( ${#VALUE_FILES[@]} > 1 )); then
    tui="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"
    rc=0
    VALUES_FILE=$(printf '%s\n' "${VALUE_FILES[@]}" \
      | bash "$tui" --title "helm_template_test | pick a values file" --prompt "values> ") || rc=$?
    case "$rc" in
      0) [[ -z "$VALUES_FILE" ]] && { echo "No values file selected."; exit 0; } ;;
      1) echo "No values file selected."; exit 0 ;;
      *) echo "Interactive picker needs a terminal — pass -e <env> instead."
         echo "Available values files:" >&2
         printf '  %s\n' "${VALUE_FILES[@]}" >&2
         exit 1 ;;
    esac
  else
    VALUES_FILE=""
  fi
fi

# --- render ----------------------------------------------------------------
CMD_ARGS=(template "$RELEASE" "$CHART")
[[ -n "$VALUES_FILE" ]] && CMD_ARGS+=(-f "$VALUES_FILE")
(( ${#EXTRA_ARGS[@]} > 0 )) && CMD_ARGS+=("${EXTRA_ARGS[@]}")

echo ">>> helm ${CMD_ARGS[*]}" >&2
helm "${CMD_ARGS[@]}"
