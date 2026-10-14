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

read -r -p "Enter the path to the file or folder to upload: " LOCAL_PATH

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
