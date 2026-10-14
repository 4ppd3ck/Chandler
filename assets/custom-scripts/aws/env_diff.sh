#!/bin/bash

set -euo pipefail

# Diff the resources of two Kubernetes namespaces, or the buckets / instances
# of two AWS profiles. Answers "why does prod work but stg doesn't?".
#
#   env_diff.sh k8s   <ns-a> <ns-b>
#   env_diff.sh aws   <profile-a> <profile-b> [region]

MODE="${1:-}"
A="${2:-}"
B="${3:-}"
REGION="${4:-us-east-1}"

usage() {
  cat <<EOF
Usage:
  $0 k8s  <ns-a> <ns-b>
  $0 aws  <profile-a> <profile-b> [region]
EOF
}

if [[ -z "$MODE" || -z "$A" || -z "$B" ]]; then usage; exit 1; fi

case "$MODE" in
  k8s|kube|kubernetes)
    if ! command -v kubectl >/dev/null 2>&1; then echo "kubectl missing."; exit 1; fi
    echo "Diffing k8s resources: $A vs $B"
    diff <(kubectl get all,cm,secret,ing,pvc -n "$A" -o custom-columns=KIND:.kind,NAME:.metadata.name --no-headers 2>/dev/null | sort) \
         <(kubectl get all,cm,secret,ing,pvc -n "$B" -o custom-columns=KIND:.kind,NAME:.metadata.name --no-headers 2>/dev/null | sort) \
      || true
    ;;
  aws)
    if ! command -v aws >/dev/null 2>&1; then echo "aws cli missing."; exit 1; fi
    echo "Diffing S3 buckets: profile [$A] vs [$B]"
    diff <(AWS_PROFILE="$A" aws s3 ls --region "$REGION" | awk '{print $3}' | sort) \
         <(AWS_PROFILE="$B" aws s3 ls --region "$REGION" | awk '{print $3}' | sort) \
      || true
    echo ""
    echo "Diffing EC2 instances: profile [$A] vs [$B]"
    diff <(AWS_PROFILE="$A" aws ec2 describe-instances --region "$REGION" \
            --query "Reservations[].Instances[].Tags[?Key=='Name'].Value|[0]" --output text | sort) \
         <(AWS_PROFILE="$B" aws ec2 describe-instances --region "$REGION" \
            --query "Reservations[].Instances[].Tags[?Key=='Name'].Value|[0]" --output text | sort) \
      || true
    ;;
  *) usage; exit 1 ;;
esac
