#!/bin/bash

set -euo pipefail

# Find the latest AMIs matching a name pattern, optionally across multiple
# regions. Useful when bootstrapping EC2 / Launch Templates.

if [[ -f "$HOME/.bash_function" ]]; then
  source "$HOME/.bash_function"
fi

if ! command -v aws >/dev/null 2>&1; then
  echo "AWS CLI is not installed. Exiting..."; exit 1
fi

AWS_REGION="${AWS_REGION:-us-east-1}"

read -r -p "AMI name pattern (e.g. amazonlinux2-*): " PATTERN
[[ -z "$PATTERN" ]] && { echo "No pattern. Exiting..."; exit 0; }

read -r -p "Regions (comma-separated, Enter for '$AWS_REGION'): " REGIONS_RAW
REGIONS_RAW="${REGIONS_RAW:-$AWS_REGION}"
IFS=',' read -ra REGIONS <<<"$REGIONS_RAW"

echo ""
printf "%-16s %-26s %-22s %s\n" "REGION" "NAME" "AMI-ID" "CREATED"
for r in "${REGIONS[@]}"; do
  r="${r// /}"
  aws ec2 describe-images --region "$r" \
    --owners amazon \
    --filters "Name=name,Values=$PATTERN" \
    --query "sort_by(Images, &CreationDate)[-1].[Name,ImageId,CreationDate]" \
    --output text | while IFS=$'\t' read -r name image_id created; do
      printf "%-16s %-26s %-22s %s\n" "$r" "$name" "$image_id" "${created%%T*}"
    done
done
