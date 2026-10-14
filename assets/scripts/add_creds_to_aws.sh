#!/bin/bash

set -euo pipefail

awsconfig_folder="$HOME/awsconfig"
awscredentials_file="$HOME/.aws/credentials.conf"

mkdir -p "$awsconfig_folder"

shopt -s nullglob
credential_files=("$awsconfig_folder"/*.txt)
shopt -u nullglob

if [[ ${#credential_files[@]} -eq 0 ]]; then
  echo "No credential .txt files found in $awsconfig_folder. Leaving $awscredentials_file untouched."
  exit 1
fi

mkdir -p "$(dirname "$awscredentials_file")"

: > "$awscredentials_file"

for file in "${credential_files[@]}"; do
  filename=$(basename "$file" .txt)
  {
    echo "[$filename]"
    tail -n +2 "$file"
  } >> "$awscredentials_file"
done

chmod 600 "$awscredentials_file"
echo "Credentials written to $awscredentials_file ($((${#credential_files[@]})) profile(s))."
