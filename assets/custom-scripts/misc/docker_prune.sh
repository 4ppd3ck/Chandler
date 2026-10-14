#!/bin/bash

set -euo pipefail

# Safe docker cleanup with a size preview before deletion.
#   docker_prune.sh            preview only
#   docker_prune.sh --go       actually delete (images, containers, networks, volumes)

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is not installed or not running."; exit 1
fi

GO=0
[[ "${1:-}" == "--go" ]] && GO=1

echo "=== Reclaimable space ==="
echo "Images:     $(docker images -f 'dangling=true' -q | wc -l) dangling image(s)"
echo "Containers: $(docker ps -aq -f 'status=exited' | wc -l) stopped container(s)"
echo "Volumes:    $(docker volume ls -f 'dangling=true' -q | wc -l) dangling volume(s)"
echo "Build cache: see 'docker builder prune'"

if [[ $GO -ne 1 ]]; then
  echo ""
  echo "This is a DRY RUN. Re-run with --go to actually delete."
  exit 0
fi

read -r -p "This will delete the above. Type 'yes' to proceed: " CONF
[[ "$CONF" != "yes" ]] && { echo "Aborted."; exit 0; }

echo "Removing stopped containers..."
docker container prune -f >/dev/null
echo "Removing dangling images..."
docker image prune -f >/dev/null
echo "Removing unused networks..."
docker network prune -f >/dev/null
echo "Removing dangling volumes..."
docker volume prune -f >/dev/null
echo "Done."
