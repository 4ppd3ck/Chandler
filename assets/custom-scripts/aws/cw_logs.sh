#!/bin/bash

set -euo pipefail

# CloudWatch Logs helper: tail live, or search a time window, with optional
# pattern filter. fzf-picks the log group from a per-profile cache.

if [[ -f "$HOME/.bash_function" ]]; then
  source "$HOME/.bash_function"
fi

if ! command -v aws >/dev/null 2>&1; then
  echo "AWS CLI is not installed. Exiting..."; exit 1
fi
TUI_SELECT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"

AWS_PROFILE="${AWS_PROFILE:-default}"
AWS_REGION="${AWS_REGION:-us-east-1}"
CACHE_DIR="$HOME/.local/bin/cw-logs-cache/$AWS_PROFILE"
mkdir -p "$CACHE_DIR"

refresh_groups() {
  local cache="$CACHE_DIR/groups.txt"
  local token=""
  : > "$cache"
  while true; do
    if [[ -n "$token" ]]; then
      out=$(aws logs describe-log-groups \
        --region "$AWS_REGION" \
        --starting-token "$token" \
        --query "logGroups[].logGroupName" --output text)
    else
      out=$(aws logs describe-log-groups \
        --region "$AWS_REGION" \
        --query "logGroups[].logGroupName" --output text)
    fi
    echo "$out" | tr '\t' '\n' >> "$cache"
    token=$(aws logs describe-log-groups --region "$AWS_REGION" \
      ${token:+--starting-token "$token"} \
      --query "NextToken" --output text 2>/dev/null || true)
    [[ -z "$token" || "$token" == "None" ]] && break
  done
  sort -u "$cache" -o "$cache"
}

CACHE="$CACHE_DIR/groups.txt"
if [[ ! -s "$CACHE" ]]; then
  echo "First run: caching log groups for profile '$AWS_PROFILE'..."
  refresh_groups
fi

rc=0
GROUP=$(bash "$TUI_SELECT" --title "cw_logs | pick a log group" --prompt "group> " <"$CACHE") || rc=$?
case "$rc" in
  0) [[ -z "$GROUP" ]] && { echo "No group selected. Exiting..."; exit 0; } ;;
  1) echo "No group selected. Exiting..."; exit 0 ;;
  *) echo "Interactive picker needs a terminal."; exit 1 ;;
esac

echo "Mode: 1) live tail (follow)  2) search a time window"
read -r -p "Choice [1]: " MODE
MODE="${MODE:-1}"

if [[ "$MODE" == "2" ]]; then
  read -r -p "Time window (e.g. 30m, 2h, 1d) [30m]: " SINCE
  SINCE="${SINCE:-30m}"
  read -r -p "Filter pattern (optional, press Enter to skip): " PATTERN
  echo "Tailing $GROUP (since $SINCE)..."
  if [[ -n "$PATTERN" ]]; then
    aws logs tail "$GROUP" --since "$SINCE" --region "$AWS_REGION" \
      --filter-pattern "$PATTERN" --color on
  else
    aws logs tail "$GROUP" --since "$SINCE" --region "$AWS_REGION" --color on
  fi
else
  echo "Live tailing $GROUP (Ctrl+C to stop)..."
  aws logs tail "$GROUP" --follow --since 5m --region "$AWS_REGION" --color on
fi
