#!/usr/bin/env sh
set -eu

# The core installer only places a released native Go binary. It never
# reimplements Baron behavior or silently replaces a legacy Baron executable.
SOURCE=${BARON_BINARY_SOURCE:-}
DEST=${BARON_INSTALL_PATH:-"$HOME/.local/bin/baron"}
EXPECTED_SHA256=${BARON_BINARY_SHA256:-}

if [ -z "$SOURCE" ]; then
  printf '%s\n' 'Set BARON_BINARY_SOURCE to a verified release binary.' >&2
  exit 2
fi
if [ -e "$DEST" ] && [ "${BARON_ALLOW_REPLACE:-0}" != "1" ]; then
  printf '%s\n' "Refusing to overwrite existing baron command at $DEST; set BARON_ALLOW_REPLACE=1 only for an explicit migration." >&2
  exit 20
fi
if [ -n "$EXPECTED_SHA256" ]; then
  ACTUAL_SHA256=$(sha256sum "$SOURCE" | awk '{print $1}')
  if [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
    printf '%s\n' 'Refusing to install: binary SHA-256 does not match BARON_BINARY_SHA256.' >&2
    exit 20
  fi
fi
mkdir -p "$(dirname -- "$DEST")"
install -m 0755 "$SOURCE" "$DEST"
printf '%s\n' "Installed $DEST"
