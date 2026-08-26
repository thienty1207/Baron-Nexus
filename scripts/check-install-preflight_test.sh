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
for phrase in \
  'nodejs' \
  'pnpm@latest' \
  'uv-${UV_ARCH}-unknown-linux-gnu.tar.gz' \
  'sha256sum' \
  'docker-ce' \
  'docker-compose-plugin' \
  'sudo_retry'; do
  if ! rg -q --fixed-strings "$phrase" install.sh; then
    printf '%s\n' "install.sh is missing automatic host-bootstrap contract: $phrase" >&2
    exit 20
  fi
done
if ! rg -q --fixed-strings 'baron deepseek api_key' install.sh; then
  printf '%s\n' 'install.sh must explain the canonical DeepSeek API-key rotation command when Baron is already installed.' >&2
  exit 20
fi
printf '%s\n' 'Install sudo preflight contract passed.'
