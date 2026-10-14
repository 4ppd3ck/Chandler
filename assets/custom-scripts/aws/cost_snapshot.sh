#!/bin/bash

set -euo pipefail

# Quick AWS cost snapshot using the Cost Explorer API: today vs yesterday,
# top 5 services, top 5 resources. Avoids clicking into the billing console.

if [[ -f "$HOME/.bash_function" ]]; then
  source "$HOME/.bash_function"
fi

if ! command -v aws >/dev/null 2>&1; then
  echo "AWS CLI is not installed. Exiting..."; exit 1
fi

AWS_PROFILE="${AWS_PROFILE:-default}"
AWS_REGION="${AWS_REGION:-us-east-1}"

TODAY=$(date -u +%Y-%m-%d)
YESTERDAY=$(date -u -d "yesterday" +%Y-%m-%d)
MTD_START=$(date -u -d "$(date +%Y-%m-01)" +%Y-%m-%d)

echo "AWS Cost Snapshot (profile '$AWS_PROFILE') — as of $TODAY UTC"
echo "============================================================="

# --- Today vs yesterday --------------------------------------------------------
today=$(aws ce get-cost-and-usage --region "$AWS_REGION" \
  --time-period "Start=$TODAY,End=$(date -u -d 'tomorrow' +%Y-%m-%d)" \
  --granularity DAILY --metrics "UnblendedCost" \
  --query "ResultsByTime[0].Total.UnblendedCost.Amount" --output text 2>/dev/null || echo "0")
yesterday=$(aws ce get-cost-and-usage --region "$AWS_REGION" \
  --time-period "Start=$YESTERDAY,End=$TODAY" \
  --granularity DAILY --metrics "UnblendedCost" \
  --query "ResultsByTime[0].Total.UnblendedCost.Amount" --output text 2>/dev/null || echo "0")
mtd=$(aws ce get-cost-and-usage --region "$AWS_REGION" \
  --time-period "Start=$MTD_START,End=$(date -u -d 'tomorrow' +%Y-%m-%d)" \
  --granularity MONTHLY --metrics "UnblendedCost" \
  --query "ResultsByTime[0].Total.UnblendedCost.Amount" --output text 2>/dev/null || echo "0")

printf "Today      : \$%.2f\n" "$today"
printf "Yesterday  : \$%.2f\n" "$yesterday"
printf "Month-to-date: \$%.2f\n" "$mtd"
echo ""

# --- Top 5 services (MTD) ------------------------------------------------------
echo "Top 5 services this month:"
aws ce get-cost-and-usage --region "$AWS_REGION" \
  --time-period "Start=$MTD_START,End=$(date -u -d 'tomorrow' +%Y-%m-%d)" \
  --granularity MONTHLY --metrics "UnblendedCost" --group-by Type=SERVICE \
  --query "sort_by(ResultsByTime[0].Groups, &Metrics.UnblendedCost.Amount)[-5:].{Service:Keys[0],Cost:Metrics.UnblendedCost.Amount}" \
  --output table 2>/dev/null \
  || echo "(Cost Explorer not enabled or no data yet)"
echo ""

# --- Top 5 resources (MTD, needs CE cost allocation tags) ----------------------
echo "Top 5 resources this month:"
aws ce get-cost-and-usage --region "$AWS_REGION" \
  --time-period "Start=$MTD_START,End=$(date -u -d 'tomorrow' +%Y-%m-%d)" \
  --granularity MONTHLY --metrics "UnblendedCost" \
  --query "sort_by(ResultsByTime[0].Groups, &Metrics.UnblendedCost.Amount)[-5:].{Resource:Keys[0],Cost:Metrics.UnblendedCost.Amount}" \
  --output table 2>/dev/null \
  || echo "(Cost Explorer not enabled or no data yet)"
