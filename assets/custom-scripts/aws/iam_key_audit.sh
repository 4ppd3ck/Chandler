#!/bin/bash

set -euo pipefail

# IAM access-key auditor: reports every access key with its age and last-used
# info, flagging keys older than 90 days. Helps catch forgotten long-lived
# credentials before they become a compliance finding.

if [[ -f "$HOME/.bash_function" ]]; then
  source "$HOME/.bash_function"
fi

if ! command -v aws >/dev/null 2>&1; then
  echo "AWS CLI is not installed. Exiting..."; exit 1
fi

AWS_PROFILE="${AWS_PROFILE:-default}"
THRESHOLD_DAYS="${1:-90}"
WARN_EPOCH=$(( $(date +%s) - THRESHOLD_DAYS * 86400 ))

echo "IAM access-key audit (profile '$AWS_PROFILE', flagging > ${THRESHOLD_DAYS}d old)"
echo ""

printf "%-22s %-22s %-10s %-12s %-16s %-12s %s\n" \
  "USER" "KEY_ID" "STATUS" "CREATED" "LAST_USED" "LAST_REGION" "AGE(d)"

USERS=$(aws iam list-users --query "Users[].UserName" --output text | tr '\t' '\n')
for user in $USERS; do
  keys=$(aws iam list-access-keys --user-name "$user" \
    --query "AccessKeyMetadata[].[AccessKeyId,Status,CreateDate]" \
    --output text 2>/dev/null || true)
  [[ -z "$keys" ]] && continue
  while IFS=$'\t' read -r key_id status created; do
    [[ -z "$key_id" ]] && continue
    created_epoch=$(date -d "${created%.*}" +%s 2>/dev/null || echo 0)
    age_days=$(( ( $(date +%s) - created_epoch ) / 86400 ))
    # LastUsed info (separate API call per key)
    read -r last_used last_region < <(
      aws iam get-access-key-last-used --access-key-id "$key_id" \
        --query "AccessKeyLastUsed.[LastUsedDate,Region]" --output text 2>/dev/null \
        || echo -e "never\t-"
    )
    flag=""
    if (( created_epoch > 0 && created_epoch < WARN_EPOCH )); then
      flag=" <<<< OLDER THAN ${THRESHOLD_DAYS}d"
    fi
    printf "%-22s %-22s %-10s %-12s %-16s %-12s %s%s\n" \
      "$user" "$key_id" "$status" "${created%%T*}" \
      "${last_used%%T*}" "$last_region" "$age_days" "$flag"
  done <<<"$keys"
done
echo ""
echo "Audit complete."
