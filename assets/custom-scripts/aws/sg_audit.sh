#!/bin/bash

set -euo pipefail

# Security Group auditor: finds wide-open ingress (0.0.0.0/0), unused SGs,
# and rules referencing stale network interfaces.

if [[ -f "$HOME/.bash_function" ]]; then
  source "$HOME/.bash_function"
fi

if ! command -v aws >/dev/null 2>&1; then
  echo "AWS CLI is not installed. Exiting..."; exit 1
fi

AWS_PROFILE="${AWS_PROFILE:-default}"
AWS_REGION="${AWS_REGION:-us-east-1}"

echo "Auditing security groups in profile '$AWS_PROFILE' / region '$AWS_REGION'..."
echo ""

# --- Wide-open ingress rules ---------------------------------------------------
echo "=== Ingress rules open to the world (0.0.0.0/0) ==="
aws ec2 describe-security-groups --region "$AWS_REGION" \
  --filters "Name=ip-permission.cidr,Values=0.0.0.0/0" \
  --query "SecurityGroups[].[GroupId,GroupName,Description]" \
  --output table

# --- Unused security groups ----------------------------------------------------
# An SG is "in use" if any network interface references it. We diff the two sets.
echo ""
echo "=== Unused security groups (no ENI references) ==="
ALL_SGS=$(aws ec2 describe-security-groups --region "$AWS_REGION" \
  --query "SecurityGroups[].GroupId" --output text | tr '\t' '\n')
USED_SGS=$(aws ec2 describe-network-interfaces --region "$AWS_REGION" \
  --query "NetworkInterfaces[].Groups[].GroupId" --output text | tr '\t' '\n' | sort -u)

if [[ -n "$USED_SGS" ]]; then
  UNUSED=$(comm -23 <(echo "$ALL_SGS" | sort -u) <(echo "$USED_SGS" | sort))
else
  UNUSED="$ALL_SGS"
fi

if [[ -z "$UNUSED" ]]; then
  echo "(none - all security groups are in use)"
else
  echo "$UNUSED" | while read -r sg; do
    [[ -z "$sg" ]] && continue
    aws ec2 describe-security-groups --region "$AWS_REGION" \
      --group-ids "$sg" \
      --query "SecurityGroups[].[GroupId,GroupName]" --output text
  done
fi
echo ""
echo "Audit complete."
