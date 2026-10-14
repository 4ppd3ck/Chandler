#!/bin/bash

set -euo pipefail

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

  echo "Fetching updated object list for bucket: $bucket in the background..."
  local temp_file
  temp_file=$(mktemp)

  aws s3api list-objects-v2 \
    --bucket "$bucket" \
    --query "Contents[].Key" \
    --output text | tr '\t' '\n' >"$temp_file"

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
SOURCE_BUCKET=$(printf '%s\n' "$BUCKETS" | bash "$TUI_SELECT" --title "s3_local_copy | pick a bucket" --prompt "bucket> ") || rc=$?
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

# Kick off the refresh in the background and capture the PID so we can wait on it.
update_object_list "$SOURCE_BUCKET" "$OBJECTS_FILE" &
FETCH_PID=$!

echo "Waiting for object list file to be created or updated..."

# Wait until the background job completes (or errors out). With set -e we cannot
# blindly `wait`; instead poll for the process to finish writing the file.
wait "$FETCH_PID" || {
  echo "Failed to fetch object list. Exiting..."
  exit 1
}

if [[ ! -s "$OBJECTS_FILE" ]]; then
  echo "Object list is empty or not yet fetched. Exiting..."
  exit 1
fi

rc=0
SELECTED=$(bash "$TUI_SELECT" --multi \
  --title "s3_local_copy | pick files/folders" --prompt "dl> " \
  <"$OBJECTS_FILE") || rc=$?
case "$rc" in
  0) ;;
  1) echo "No selection made. Exiting..."; exit 1 ;;
  *) echo "Interactive picker needs a terminal."; exit 1 ;;
esac
[[ -z "$SELECTED" ]] && { echo "No selection made. Exiting..."; exit 1; }

read -r -p "Enter destination directory to download files or folders: " LOCAL_DEST

if [[ -z "$LOCAL_DEST" || "$LOCAL_DEST" == "/" || "$LOCAL_DEST" == "~" ]]; then
  echo "Invalid destination directory. Exiting..."
  exit 1
fi

mkdir -p "$LOCAL_DEST"

ok=0
fail=0
while IFS= read -r ITEM; do
  [[ -z "$ITEM" ]] && continue
  if [[ "$ITEM" == */ ]]; then
    echo "Copying folder $ITEM..."
    if aws s3 cp "s3://$SOURCE_BUCKET/$ITEM" "$LOCAL_DEST/$ITEM" --recursive; then
      ((ok++))
    else
      echo "  Failed: $ITEM"
      ((fail++))
    fi
  else
    echo "Copying file $ITEM..."
    mkdir -p "$(dirname "$LOCAL_DEST/$ITEM")"
    if aws s3 cp "s3://$SOURCE_BUCKET/$ITEM" "$LOCAL_DEST/$ITEM"; then
      ((ok++))
    else
      echo "  Failed: $ITEM"
      ((fail++))
    fi
  fi
done <<<"$SELECTED"

echo ""
echo "Download complete. Success: $ok, Failed: $fail -> $LOCAL_DEST"
