#!/bin/bash

set -euo pipefail

# Lists and refreshes the cached object list for a chosen S3 bucket.
# Output is written to ~/.local/bin/s3-bucket-objects/<profile>/<bucket>.txt
# and can be browsed with fzf or consumed by other scripts.

if ! aws s3 ls >/dev/null 2>&1; then
  echo "AWS CLI is not configured properly or credentials are missing."
  exit 1
fi

TUI_SELECT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"

AWS_PROFILE="${AWS_PROFILE:-default}"

# Environment colour hint for the current profile: red = prod-like,
# green = non-prod-like (tested first: "nonprod" contains "prod"),
# yellow = anything else.
if [[ -z "${NO_COLOR:-}" ]]; then
  if [[ "${AWS_PROFILE,,}" =~ (nonprod|non-prd|nonprd|dev|stg|intg) ]]; then
    PROF_COL=$'\e[1;32m'
  elif [[ "${AWS_PROFILE,,}" =~ (prod|prd) ]]; then
    PROF_COL=$'\e[1;31m'
  else
    PROF_COL=$'\e[1;33m'
  fi
  echo "[Current Profile: ${PROF_COL}${AWS_PROFILE}\e[0m]"
else
  echo "[Current Profile: $AWS_PROFILE]"
fi

update_object_list() {
  local bucket="$1"
  local outfile="$2"

  echo "Fetching updated object list for bucket: $bucket..."
  local temp_file
  temp_file=$(mktemp)

  aws s3api list-objects-v2 \
    --bucket "$bucket" \
    --query "Contents[].Key" \
    --output text | tr '\t' '\n' >"$temp_file"

  # Add common prefixes (folders) explicitly to the list
  awk -F'/' '{if (NF > 1) {print $1"/"}}' "$temp_file" | sort -u >>"$temp_file"

  sort -u "$temp_file" >"$outfile"

  rm -f "$temp_file"
  echo "Object list updated: $outfile"
}

LOG_DIR="$HOME/.local/bin/s3-bucket-objects/$AWS_PROFILE"
mkdir -p "$LOG_DIR"

echo "Fetching S3 buckets list..."
BUCKETS=$(aws s3 ls | awk '{print $3}')

if [[ -z "$BUCKETS" ]]; then
  echo "No buckets found."
  exit 1
fi

rc=0
SOURCE_BUCKET=$(printf '%s\n' "$BUCKETS" | bash "$TUI_SELECT" --title "s3_local_objects | pick a bucket" --prompt "bucket> ") || rc=$?
case "$rc" in
  0) [[ -z "$SOURCE_BUCKET" ]] && { echo "No source bucket selected. Exiting..."; exit 1; } ;;
  1) echo "No source bucket selected. Exiting..."; exit 1 ;;
  *) echo "Interactive picker needs a terminal."; exit 1 ;;
esac

if [[ -z "$SOURCE_BUCKET" ]]; then
  echo "No source bucket selected. Exiting..."
  exit 1
fi

OBJECTS_FILE="$LOG_DIR/${SOURCE_BUCKET}.txt"

update_object_list "$SOURCE_BUCKET" "$OBJECTS_FILE"

if [[ ! -s "$OBJECTS_FILE" ]]; then
  echo "Bucket is empty or object list is empty."
  exit 0
fi

OBJ_COUNT=$(wc -l <"$OBJECTS_FILE")
echo "Cached $OBJ_COUNT objects for bucket '$SOURCE_BUCKET' -> $OBJECTS_FILE"

echo "Opening object browser (esc to exit)..."
bash "$TUI_SELECT" --title "objects in $SOURCE_BUCKET" --prompt "obj> " \
  <"$OBJECTS_FILE" >/dev/null || true

echo "Done."
