#!/bin/bash

set -euo pipefail

# Certificate expiry report: ACM certificates + (optionally) live TLS probes
# against ingress endpoints. Sorted by days-to-expiry, color-coded.

if [[ -f "$HOME/.bash_function" ]]; then
  # shellcheck disable=SC1090
  source "$HOME/.bash_function"
fi

if ! command -v aws >/dev/null 2>&1; then
  echo "AWS CLI is not installed. Exiting..."; exit 1
fi

AWS_PROFILE="${AWS_PROFILE:-default}"
AWS_REGION="${AWS_REGION:-us-east-1}"

echo "ACM certificate expiry (profile '$AWS_PROFILE' / region '$AWS_REGION')"
echo "------------------------------------------------------------"

aws acm list-certificates --region "$AWS_REGION" \
  --output text --query "CertificateSummaryList[].CertificateArn" \
| while read -r arn; do
  [[ -z "$arn" ]] && continue
  aws acm describe-certificate --region "$AWS_REGION" --certificate-arn "$arn" \
    --query "Certificate.[DomainName,Status,NotAfter]" --output text \
  | while IFS=$'\t' read -r domain status not_after; do
    # NotAfter is epoch seconds; compute days remaining.
    days=$(( ( $(date -d "@$not_after" +%s 2>/dev/null || echo 0) - $(date +%s) ) / 86400 ))
    flag=""
    if   (( days <  0 )); then flag=" <<<< EXPIRED"
    elif (( days < 14 )); then flag=" <<<< CRITICAL"
    elif (( days < 30 )); then flag=" <<<< WARN"
    fi
    printf "%-40s %-10s %5dd%s\n" "$domain" "$status" "$days" "$flag"
  done
done | sort -k3 -n

echo ""
echo "(Optional) Probe live TLS endpoints? Provide a file of hostnames, or Enter to skip:"
read -r -p "File: " HOSTFILE
if [[ -n "$HOSTFILE" && -f "$HOSTFILE" ]]; then
  echo ""
  echo "Live TLS probes:"
  while IFS= read -r host; do
    [[ -z "$host" ]] && continue
    exp=$(echo | timeout 5 openssl s_client -servername "$host" -connect "$host:443" 2>/dev/null \
      | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)
    if [[ -n "$exp" ]]; then
      days=$(( ( $(date -d "$exp" +%s) - $(date +%s) ) / 86400 ))
      printf "%-40s %5dd\n" "$host" "$days"
    else
      printf "%-40s %s\n" "$host" "(no cert / unreachable)"
    fi
  done <"$HOSTFILE"
fi
