#!/bin/bash

set -euo pipefail

# Delete local git branches that have been merged into the current main
# branch. Shows the list first, then asks for confirmation.
#
#   git_cleanup.sh                  interactive confirm
#   git_cleanup.sh --force          no confirm

if ! command -v git >/dev/null 2>&1; then
  echo "git is not installed."; exit 1
fi

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "Not inside a git repository."; exit 1
fi

FORCE=0
[[ "${1:-}" == "--force" ]] && FORCE=1

CURRENT=$(git rev-parse --abbrev-ref HEAD)
echo "Current branch: $CURRENT"
echo "Merged local branches (safe to delete):"

MERGED=$(git branch --merged "$CURRENT" \
  | grep -v "^\*" | grep -v "$CURRENT" | sed 's/^ *//' || true)

if [[ -z "$MERGED" ]]; then
  echo "  (none)"; exit 0
fi

echo "$MERGED" | sed 's/^/  /'

if [[ $FORCE -ne 1 ]]; then
  echo ""
  read -r -p "Delete these branches? [y/N]: " CONF
  case "$CONF" in
    y|Y|yes|YES) ;;
    *) echo "Aborted."; exit 0 ;;
  esac
fi

echo "$MERGED" | while IFS= read -r b; do
  [[ -z "$b" ]] && continue
  git branch -d "$b" >/dev/null && echo "Deleted: $b"
done
echo "Done."
