#!/bin/bash

set -euo pipefail

if ! aws_err="$(aws s3 ls 2>&1)"; then
  # The old silent guard (>/dev/null 2>&1) hid WHY auth failed — no creds,
  # wrong endpoint, a CLI too old for AWS_ENDPOINT_URL, LocalStack down.
  # Show the real error, then the plain-language line.
  printf '%s
' "$aws_err" | head -3 >&2
  echo "AWS CLI is not configured properly or credentials are missing."
  exit 1
fi

TUI_SELECT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"

# AWS auth: a named profile (AWS_PROFILE) or plain env credentials —
# LocalStack, CI, anything without `aws configure`. Nothing is forced onto
# the aws calls (no --profile), so env credentials just work; the display
# names what is actually in use, and AWS_PROF_DIR keeps env-credential
# cache data apart from any real profile's.
AWS_PROF="${AWS_PROFILE:-env credentials}"
AWS_PROF_DIR="${AWS_PROFILE:-env}"

# Lower-cased copy for the environment hints below. ${var,,} is bash 4+ and
# macOS ships /bin/bash 3.2, which these scripts run under via their shebang;
# the lowering goes through tr and works on every bash.
AWS_PROFILE_LC="$(printf '%s' "$AWS_PROF" | tr '[:upper:]' '[:lower:]')"

# Environment colour hint for the current profile: red = prod-like,
# green = non-prod-like (tested first: "nonprod" contains "prod"),
# yellow = anything else.
# A custom endpoint (LocalStack, MinIO, …) is part of "which AWS is this"
# and belongs on the hint line.
ENDPOINT_NOTE=""
if [[ -n "${AWS_ENDPOINT_URL:-}" ]]; then
  ENDPOINT_NOTE=" (endpoint: ${AWS_ENDPOINT_URL})"
fi
if [[ -z "${NO_COLOR:-}" ]]; then
  COL_RESET=$'\e[0m'
  if [[ "${AWS_PROFILE_LC}" =~ (nonprod|non-prd|nonprd|dev|stg|intg) ]]; then
    PROF_COL=$'\e[1;32m'
  elif [[ "${AWS_PROFILE_LC}" =~ (prod|prd) ]]; then
    PROF_COL=$'\e[1;31m'
  else
    PROF_COL=$'\e[1;33m'
  fi
  echo "[Current Profile: ${PROF_COL}${AWS_PROF}${COL_RESET}]${ENDPOINT_NOTE}"
else
  echo "[Current Profile: $AWS_PROF${ENDPOINT_NOTE}]"
fi

update_object_list() {
  local bucket="$1"
  local outfile="$2"

  echo "Fetching updated object list for bucket: $bucket in the background..."
  local temp_file
  temp_file=$(mktemp)

  # An empty bucket makes the CLI print a literal "None" for this query;
  # awk (not grep) drops it so the pipeline's status stays 0 under pipefail.
  aws s3api list-objects-v2 \
    --bucket "$bucket" \
    --query "Contents[].Key" \
    --output text | tr '\t' '\n' | awk '$0 != "None"' >"$temp_file"

  awk -F'/' '{if (NF > 1) {print $1"/"}}' "$temp_file" | sort -u >>"$temp_file"

  sort -u "$temp_file" >"$outfile"

  rm -f "$temp_file"
  echo "Object list updated: $outfile"
}

S3_CACHE_ROOT="${XDG_CACHE_HOME:-$HOME/.cache}/chandler/s3-bucket-objects"
LEGACY_S3_CACHE_ROOT="$HOME/.local/bin/s3-bucket-objects"
migrate_legacy_s3_cache() {
  [[ -d "$LEGACY_S3_CACHE_ROOT" ]] || return 0
  mkdir -p "$S3_CACHE_ROOT"
  local old_profile old_file profile_name new_profile
  for old_profile in "$LEGACY_S3_CACHE_ROOT"/*; do
    [[ -d "$old_profile" ]] || continue
    profile_name="${old_profile##*/}"
    new_profile="$S3_CACHE_ROOT/$profile_name"
    mkdir -p "$new_profile"
    for old_file in "$old_profile"/*; do
      [[ -f "$old_file" ]] || continue
      mv -f "$old_file" "$new_profile/"
    done
    rmdir "$old_profile" 2>/dev/null || true
  done
  rmdir "$LEGACY_S3_CACHE_ROOT" 2>/dev/null || true
}
migrate_legacy_s3_cache
LOG_DIR="$S3_CACHE_ROOT/$AWS_PROF_DIR"
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

while true; do
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

  # Make a bare Escape clear and submit the line, returning to the picker.
  # The mapping is local to this script process; Readline still handles Tab
  # completion and the usual editing keys at the destination prompt.
  bind '"\e": "\C-u\C-m"' 2>/dev/null || true
  if ! read -e -r -p "Enter destination directory to download files or folders: " LOCAL_DEST; then
    echo "Destination prompt cancelled. Exiting..."
    exit 1
  fi
  [[ -n "$LOCAL_DEST" ]] && break
  echo "Returning to object picker..."
done

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
