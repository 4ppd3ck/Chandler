#!/bin/bash

set -euo pipefail

# Browse EC2 instances (Name, state, IP) via fzf and start an SSM session on
# the chosen one. Replaces the need to hardcode per-host aliases.

if [[ -f "$HOME/.bash_function" ]]; then
  source "$HOME/.bash_function"
fi

if ! command -v aws >/dev/null 2>&1; then
  echo "AWS CLI is not installed. Exiting..."; exit 1
fi
TUI_SELECT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../misc/tui_select.sh"

AWS_PROFILE="${AWS_PROFILE:-default}"
AWS_REGION="${AWS_REGION:-us-east-1}"

# Get all running instances, flatten tags to "Name | State | PrivIP | PubIP"
echo "Fetching instances for profile '$AWS_PROFILE' / region '$AWS_REGION'..."
MAP=$(aws ec2 describe-instances --region "$AWS_REGION" \
  --filters "Name=instance-state-name,Values=running" \
  --query "Reservations[].Instances[].[InstanceId,State.Name,PrivateIpAddress,PublicIpAddress,Tags[?Key=='Name'].Value|[0]]" \
  --output text | awk -F'\t' '{
    name = $5; if (name == "") name = "(unnamed)";
    pub  = $4; if (pub  == "") pub  = "-";
    printf "%s | %s | %s | %s | %s\n", name, $2, $3, pub, $1
  }')

if [[ -z "$MAP" ]]; then
  echo "No running instances found."; exit 0
fi

rc=0
SELECTION=$(printf '%s\n' "$MAP" | bash "$TUI_SELECT" \
  --title "Name | State | PrivIP | PubIP | Id" --prompt "instance> ") || rc=$?
case "$rc" in
  0) [[ -z "$SELECTION" ]] && { echo "No selection. Exiting..."; exit 0; } ;;
  1) echo "No selection. Exiting..."; exit 0 ;;
  *) echo "Interactive picker needs a terminal."; exit 1 ;;
esac

INSTANCE_ID=$(echo "$SELECTION" | awk -F'|' '{gsub(/ /,"",$5); print $5}')
echo "Connecting via SSM to $INSTANCE_ID ..."
if ! command -v session-manager-plugin >/dev/null 2>&1; then
  echo "session-manager-plugin not installed. Run install-aws.sh first."
  exit 1
fi

# The SSM document is optional: without one AWS uses the default
# SSM-SessionManagerRunShell. Set SSM_DOCUMENT to target a custom
# document instead (e.g. one enabling session logging or KMS encryption).
doc_args=()
if [[ -n "${SSM_DOCUMENT:-}" ]]; then
  echo "Using SSM Document: $SSM_DOCUMENT"
  doc_args=(--document-name "$SSM_DOCUMENT")
fi

aws ssm start-session --target "$INSTANCE_ID" \
  --region "$AWS_REGION" ${doc_args[@]+"${doc_args[@]}"}
