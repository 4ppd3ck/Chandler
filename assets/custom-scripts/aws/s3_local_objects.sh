#!/bin/bash

set -euo pipefail

# Lists and refreshes the cached object list for a chosen S3 bucket.
# Output is written to ~/.cache/chandler/s3-bucket-objects/<profile>/<bucket>.txt
# and can be browsed with fzf or consumed by other scripts.

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

  echo "Fetching updated object list for bucket: $bucket..."
  local temp_file
  temp_file=$(mktemp)

  # An empty bucket makes the CLI print a literal "None" for this query;
  # awk (not grep) drops it so the pipeline's status stays 0 under pipefail.
  aws s3api list-objects-v2 \
    --bucket "$bucket" \
    --query "Contents[].Key" \
    --output text | tr '\t' '\n' | awk '$0 != "None"' >"$temp_file"

  # Add common prefixes (folders) explicitly to the list
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

echo "Opening object browser (select a folder to open it, ../ to go up, esc to exit)..."
BROWSE_FILE=$(mktemp)
CURRENT_PREFIX=""
while true; do
  # Build a one-level view: direct files and immediate child folders only.
  # The full object list remains cached in OBJECTS_FILE.
  {
    printf '../\n'
    awk -v prefix="$CURRENT_PREFIX" '
      index($0, prefix) == 1 {
        rest = substr($0, length(prefix) + 1)
        if (rest == "") next
        slash = index(rest, "/")
        if (slash > 0) print substr(rest, 1, slash)
        else print rest
      }
    ' "$OBJECTS_FILE" | sort -u
  } >"$BROWSE_FILE"

  if [[ -z "$CURRENT_PREFIX" ]]; then
    BROWSE_TITLE="objects in $SOURCE_BUCKET"
  else
    BROWSE_TITLE="objects in $SOURCE_BUCKET/$CURRENT_PREFIX"
  fi

  rc=0
  SELECTED=$(bash "$TUI_SELECT" --title "$BROWSE_TITLE" --prompt "obj> " \
    <"$BROWSE_FILE") || rc=$?
  case "$rc" in
    0) ;;
    1) break ;;
    *) echo "Interactive picker needs a terminal."; break ;;
  esac

  if [[ "$SELECTED" == "../" ]]; then
    if [[ -n "$CURRENT_PREFIX" ]]; then
      trimmed_prefix="${CURRENT_PREFIX%/}"
      if [[ "$trimmed_prefix" == */* ]]; then
        CURRENT_PREFIX="${trimmed_prefix%/*}/"
      else
        CURRENT_PREFIX=""
      fi
    else
      break
    fi
  elif [[ "$SELECTED" == */ ]]; then
    CURRENT_PREFIX+="$SELECTED"
  else
    echo "Selected object: ${CURRENT_PREFIX}${SELECTED}"
  fi
done
rm -f "$BROWSE_FILE"

echo "Done."
