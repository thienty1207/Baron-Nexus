#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

preflight=$(rg -n 'sudo -v' install.sh | cut -d: -f1 | head -n1 || true)
download=$(rg -n 'curl --fail' install.sh | cut -d: -f1 | head -n1 || true)
if [ -z "$preflight" ] || [ -z "$download" ] || [ "$preflight" -ge "$download" ]; then
  printf '%s\n' 'install.sh must preflight sudo before its first release download.' >&2
  exit 20
fi
if rg -n 'SUDO_PASSWORD|sudo_password|printf.*PASSWORD|echo.*PASSWORD' install.sh >/dev/null; then
  printf '%s\n' 'install.sh must not capture or echo a sudo password.' >&2
  exit 20
fi
printf '%s\n' 'Install sudo preflight contract passed.'
