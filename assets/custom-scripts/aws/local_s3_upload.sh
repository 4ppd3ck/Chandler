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

echo "Fetching S3 buckets list..."
BUCKETS=$(aws s3 ls | awk '{print $3}')

if [[ -z "$BUCKETS" ]]; then
  echo "No buckets found."
  exit 1
fi

rc=0
DEST_BUCKET=$(printf '%s\n' "$BUCKETS" | bash "$TUI_SELECT" --title "local_s3_upload | pick destination bucket" --prompt "bucket> ") || rc=$?
case "$rc" in
  0) [[ -z "$DEST_BUCKET" ]] && { echo "No destination bucket selected. Exiting..."; exit 1; } ;;
  1) echo "No destination bucket selected. Exiting..."; exit 1 ;;
  *) echo "Interactive picker needs a terminal."; exit 1 ;;
esac

# Make a bare Escape clear and submit the line; this prompt has no file view
# to return to, so an empty result cancels the upload.
bind '"\e": "\C-u\C-m"' 2>/dev/null || true
if ! read -e -r -p "Enter the path to the file or folder to upload: " LOCAL_PATH; then
  echo "Upload cancelled. Exiting..."
  exit 0
fi
if [[ -z "$LOCAL_PATH" ]]; then
  echo "Upload cancelled. Exiting..."
  exit 0
fi

if [[ ! -e "$LOCAL_PATH" ]]; then
  echo "The specified file or folder does not exist. Exiting..."
  exit 1
fi

read -r -p "Enter the destination prefix in the bucket (or leave blank for root): " DEST_PREFIX

# Strip leading/trailing slashes so we never produce double slashes.
DEST_PREFIX="${DEST_PREFIX#/}"
DEST_PREFIX="${DEST_PREFIX%/}"

if [[ -d "$LOCAL_PATH" ]]; then
  if [[ -n "$DEST_PREFIX" ]]; then
    S3_URI="s3://$DEST_BUCKET/$DEST_PREFIX/"
  else
    S3_URI="s3://$DEST_BUCKET/"
  fi
  echo "Uploading folder $LOCAL_PATH to $S3_URI"
  aws s3 cp "$LOCAL_PATH" "$S3_URI" --recursive
elif [[ -f "$LOCAL_PATH" ]]; then
  FILE_NAME=$(basename "$LOCAL_PATH")
  if [[ -n "$DEST_PREFIX" ]]; then
    S3_URI="s3://$DEST_BUCKET/$DEST_PREFIX/$FILE_NAME"
  else
    S3_URI="s3://$DEST_BUCKET/$FILE_NAME"
  fi
  echo "Uploading file $LOCAL_PATH to $S3_URI"
  aws s3 cp "$LOCAL_PATH" "$S3_URI"
else
  echo "Invalid path provided. Exiting..."
  exit 1
fi

echo "Upload complete!"
