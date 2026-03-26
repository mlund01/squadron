#!/bin/bash
# Copies local squadron vault to the Docker container's squadron-data volume.
# Usage: scripts/sync-vars.sh

set -e

VAULT_FILE="$HOME/.squadron/vars.vault"
CONTAINER="squadron-squadron-1"

if [ ! -f "$VAULT_FILE" ]; then
  echo "No vault file found at $VAULT_FILE — run 'squadron init' first"
  exit 1
fi

# Check if container is running
if ! docker ps --format '{{.Names}}' | grep -q "^${CONTAINER}$"; then
  echo "Container $CONTAINER is not running. Start it with: docker compose up"
  exit 1
fi

docker cp "$VAULT_FILE" "${CONTAINER}:/data/squadron/vars.vault"
echo "Synced encrypted vault to container"
