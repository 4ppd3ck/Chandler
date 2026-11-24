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

die() { echo "ERROR: $*" >&2; exit 1; }

# ---- Supported date formats ----------------------------------------------
#   1) YYYYMMDD   e.g. 20260709      (8 digits)  canonical
#   2) DDMMYYYY   e.g. 09072026      (8 digits)  -> normalize to YYYYMMDD
#   3) YYYYMM     e.g. 202607        (6 digits)  -> padded to YYYYMM00 (sort key)
#   4) MMYYYY     e.g. 072026        (6 digits)  -> normalize to YYYYMM00
#
# Canonicalization makes string compare == chronological compare, which the
# awk matcher relies on for inclusive ranges. The user declares the format up
# front because the raw digits are ambiguous (20260709 could be YYYYMMDD or
# DDMMYYYY).
DATE_FORMAT="${S3_DATE_FORMAT:-}"   # may be pre-set to 1/2/3/4 to skip the menu


fmt_len() { case "$1" in 1|2) echo 8 ;; 3|4) echo 6 ;; *) echo 0 ;; esac; }

# Human label + sample for a format id.
fmt_label() {
  case "$1" in
    1) echo "YYYYMMDD (e.g. 20260709)" ;;
    2) echo "DDMMYYYY (e.g. 09072026)" ;;
    3) echo "YYYYMM   (e.g. 202607)" ;;
    4) echo "MMYYYY   (e.g. 072026)" ;;
  esac
}

# Validate a raw user date string for the active format, and reject impossible
# calendar values when GNU date is available. Returns canonical form on stdout.
#   $1 = raw input ; echoes canonical key (YYYYMMDD or YYYYMM00) on success
canonicalize() {
  local raw="$1"
  local len y m d
  len=$(fmt_len "$DATE_FORMAT")
  [[ ${#raw} -eq "$len" && "$raw" =~ ^[0-9]+$ ]] || return 1
  case "$DATE_FORMAT" in
    1) y=${raw:0:4}; m=${raw:4:2}; d=${raw:6:2} ;;
    2) d=${raw:0:2}; m=${raw:2:2}; y=${raw:4:4} ;;
    3) y=${raw:0:4}; m=${raw:4:2}; d=00 ;;
    4) m=${raw:0:2}; y=${raw:2:4}; d=00 ;;
    *) return 1 ;;
  esac
  # Reject impossible month (year+month formats skip the day check).
  [[ "$m" =~ ^(0[1-9]|1[0-2])$ ]] || return 1
  if [[ "$d" != "00" ]]; then
    local year_num month_num day_num max_day
    year_num=$((10#$y))
    month_num=$((10#$m))
    day_num=$((10#$d))
    (( year_num > 0 )) || return 1
    case "$month_num" in
      1|3|5|7|8|10|12) max_day=31 ;;
      4|6|9|11) max_day=30 ;;
      2)
        max_day=28
        if (( year_num % 400 == 0 || (year_num % 4 == 0 && year_num % 100 != 0) )); then
          max_day=29
        fi
        ;;
    esac
    (( day_num >= 1 && day_num <= max_day )) || return 1
  fi
  echo "${y}${m}${d}"     # canonical: YYYYMMDD or YYYYMM00
}

# Read a date from the user in the active format, retrying until valid/empty.
#   $1 = prompt, $2 = output var name (canonical value assigned)
read_date() {
  local prompt="$1" var="$2" value canon
  while true; do
    read -r -p "$prompt" value
    if [[ -z "$value" ]]; then
      echo "No date entered. Exiting..."
      exit 0
    fi
    if canon=$(canonicalize "$value"); then
      printf -v "$var" '%s' "$canon"
      return 0
    fi
    echo "Invalid date '$value'. Expected $(fmt_label "$DATE_FORMAT"). Try again."
  done
}

# Filter object keys on stdin -> stdout for keys containing a digit run whose
# canonical form matches the active format.
#   $1 mode (exact|range), $2 target-or-low, $3 high (range only)
# Reads $DATE_FORMAT to know the expected digit-run length, then normalizes
# every matching run to YYYYMMDD (or YYYYMM00) before comparing.
filter_keys() {
  local mode="$1" target="$2" high="${3:-}"
  local len
  len=$(fmt_len "$DATE_FORMAT")
  awk -v MODE="$mode" -v FMT="$DATE_FORMAT" -v LEN="$len" \
      -v TARGET="$target" -v LO="$target" -v HI="$high" '
    function canon(run,    y,m,d) {
      # Normalize a LEN-digit run into YYYYMMDD (day 00 for month-only).
      if      (FMT == 1) { y=substr(run,1,4); m=substr(run,5,2); d=substr(run,7,2) }
      else if (FMT == 2) { d=substr(run,1,2); m=substr(run,3,2); y=substr(run,5,4) }
      else if (FMT == 3) { y=substr(run,1,4); m=substr(run,5,2); d="00" }
      else if (FMT == 4) { m=substr(run,1,2); y=substr(run,3,4); d="00" }
      else               { return "" }
      return y m d
    }
    {
      s = $0
      while (match(s, /[0-9]+/)) {
        run = substr(s, RSTART, RLENGTH)
        if (length(run) == LEN+0) {
          c = canon(run)
          if (MODE == "exact") { if (c == TARGET) { print $0; next } }
          else                 { if (c >= LO && c <= HI) { print $0; next } }
        }
        s = substr(s, RSTART + RLENGTH)
      }
    }
  '
}

mkdir -p "$LOG_DIR"

echo "Fetching S3 buckets list..."
BUCKETS=$(aws s3 ls | awk '{print $3}')
[[ -z "$BUCKETS" ]] && die "No buckets found."

rc=0
SOURCE_BUCKET=$(printf '%s\n' "$BUCKETS" | bash "$TUI_SELECT" --title "s3_date_download | pick a bucket" --prompt "bucket> ") || rc=$?
case "$rc" in
  0) [[ -z "$SOURCE_BUCKET" ]] && die "No source bucket selected." ;;
  1) die "No source bucket selected." ;;
  *) die "Interactive picker needs a terminal." ;;
esac

OBJECTS_FILE="$LOG_DIR/${SOURCE_BUCKET}.txt"

echo "Fetching object list for bucket: $SOURCE_BUCKET ..."
TEMP_FILE=$(mktemp)
# An empty bucket makes the CLI print a literal "None" for this query;
# awk (not grep) drops it so the pipeline's status stays 0 under pipefail.
aws s3api list-objects-v2 \
  --bucket "$SOURCE_BUCKET" \
  --query "Contents[].Key" \
  --output text | tr '\t' '\n' | awk '$0 != "None"' >"$TEMP_FILE"
sort -u "$TEMP_FILE" >"$OBJECTS_FILE"
rm -f "$TEMP_FILE"

if [[ ! -s "$OBJECTS_FILE" ]]; then
  echo "Bucket '$SOURCE_BUCKET' is empty."
  exit 0
fi

# ---- Date-format selection -----------------------------------------------
# Set S3_DATE_FORMAT=1|2|3|4 in the environment to pre-select and skip the
# prompt (handy when all your files use one convention).
if [[ -z "$DATE_FORMAT" ]]; then
  echo ""
  echo "Select the date format used in the filenames:"
  echo "  1 - YYYYMMDD   (e.g. 20260709)"
  echo "  2 - DDMMYYYY   (e.g. 09072026)"
  echo "  3 - YYYYMM     (e.g. 202607)"
  echo "  4 - MMYYYY     (e.g. 072026)"
  echo ""
  read -r -p "Enter option [1]: " DATE_FORMAT
  DATE_FORMAT="${DATE_FORMAT:-1}"
fi
case "$DATE_FORMAT" in
  1|2|3|4) : ;;
  *) die "Invalid date-format option '$DATE_FORMAT'." ;;
esac
FMT_LABEL=$(fmt_label "$DATE_FORMAT")
echo "Using format: $FMT_LABEL"

# ---- Mode selection ----
echo ""
echo "Select download mode:"
echo "  1 - Exact date   (e.g. matches keys containing that one date)"
echo "  2 - Date range   (inclusive of both endpoints)"
echo ""
read -r -p "Enter option: " MODE_OPTION

case "$MODE_OPTION" in
  1)
    read_date "Enter the exact date ($(fmt_label "$DATE_FORMAT")): " TARGET_DATE
    MATCHES=$(filter_keys "exact" "$TARGET_DATE" <"$OBJECTS_FILE")
    DESC="exact $FMT_LABEL $TARGET_DATE"
    ;;
  2)
    read_date "Enter start date ($(fmt_label "$DATE_FORMAT")): " START_DATE
    read_date "Enter end date   ($(fmt_label "$DATE_FORMAT")): " END_DATE
    if [[ "$START_DATE" > "$END_DATE" ]]; then
      die "Start ($START_DATE) is after end ($END_DATE)."
    fi
    MATCHES=$(filter_keys "range" "$START_DATE" "$END_DATE" <"$OBJECTS_FILE")
    DESC="range $START_DATE .. $END_DATE ($FMT_LABEL)"
    ;;
  *)
    die "Invalid option '$MODE_OPTION'."
    ;;
esac

if [[ -z "$MATCHES" ]]; then
  echo "No files found for $DESC."
  exit 0
fi

MATCH_COUNT=$(echo "$MATCHES" | wc -l)
echo ""
echo "Found $MATCH_COUNT object(s) for $DESC."

while true; do
  # Present matches for multi-select (ESC/cancel to abort without downloading).
  rc=0
  SELECTED=$(printf '%s\n' "$MATCHES" | bash "$TUI_SELECT" --multi \
    --title "s3_date_download | pick files/folders" --prompt "dl> ") || rc=$?
  case "$rc" in
    0) ;;
    1) echo "No selection made. Exiting..."; exit 0 ;;
    *) die "Interactive picker needs a terminal." ;;
  esac
  [[ -z "$SELECTED" ]] && { echo "No selection made. Exiting..."; exit 0; }

  # Make a bare Escape clear and submit the line, returning to the picker.
  bind '"\e": "\C-u\C-m"' 2>/dev/null || true
  if ! read -e -r -p "Enter destination directory: " LOCAL_DEST; then
    echo "Destination prompt cancelled. Exiting..."
    exit 0
  fi
  [[ -n "$LOCAL_DEST" ]] && break
  echo "Returning to file picker..."
done

if [[ -z "$LOCAL_DEST" || "$LOCAL_DEST" == "/" || "$LOCAL_DEST" == "~" ]]; then
  die "Invalid destination directory: '$LOCAL_DEST'."
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
