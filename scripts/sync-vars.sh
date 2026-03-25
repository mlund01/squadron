#!/bin/bash
# Copies local squadron vars to the Docker container's squadron-data volume.
# Usage: scripts/sync-vars.sh

set -e

VARS_FILE="$HOME/.squadron/vars.txt"
CONTAINER="squadron-squadron-1"

if [ ! -f "$VARS_FILE" ]; then
  echo "No local vars file found at $VARS_FILE"
  exit 1
fi

# Check if container is running
if ! docker ps --format '{{.Names}}' | grep -q "^${CONTAINER}$"; then
  echo "Container $CONTAINER is not running. Start it with: docker compose up"
  exit 1
fi

docker cp "$VARS_FILE" "${CONTAINER}:/data/squadron/vars.txt"
echo "Synced $(wc -l < "$VARS_FILE" | tr -d ' ') variables to container"
