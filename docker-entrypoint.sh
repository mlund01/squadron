#!/bin/sh
set -e

# Verify required volume mount exists by checking /proc/self/mountinfo.
# This works for both bind mounts and named volumes, unlike sentinel
# files (which Docker copies into new named volumes on first mount).

is_mounted() {
  awk -v target="$1" '$5 == target { found=1; exit } END { exit !found }' /proc/self/mountinfo
}

if ! is_mounted /config; then
  echo "Error: /config is not mounted. Mount your config directory:" >&2
  echo "  docker run -v /path/to/config:/config ..." >&2
  echo "" >&2
  echo "State (vault, plugins, db) lives in /config/.squadron/" >&2
  exit 1
fi

exec squadron "$@"
