#!/bin/sh
set -e

# Verify required volume mounts exist by checking /proc/self/mountinfo.
# This works for both bind mounts and named volumes, unlike sentinel
# files (which Docker copies into new named volumes on first mount).

is_mounted() {
  awk -v target="$1" '$5 == target { found=1; exit } END { exit !found }' /proc/self/mountinfo
}

fail=0

if ! is_mounted /config; then
  echo "Error: /config is not mounted. Mount your config directory:" >&2
  echo "  docker run -v /path/to/config:/config ..." >&2
  fail=1
fi

if ! is_mounted /data/squadron; then
  echo "Error: /data/squadron is not mounted. Mount a persistent volume:" >&2
  echo "  docker run -v squadron-data:/data/squadron ..." >&2
  fail=1
fi

if [ "$fail" = "1" ]; then
  exit 1
fi

exec squadron "$@"
