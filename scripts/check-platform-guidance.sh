#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
README="$ROOT/README.md"
for phrase in \
  'baron tencent-memory init' \
  'Docker Desktop' \
  'WSL2' \
  'Ubuntu' \
  'without silently' \
  'Codex Desktop boundary' \
  'CLI hook contract'; do
  if ! rg -q --fixed-strings "$phrase" "$README"; then
    printf '%s\n' "Platform guidance is missing required phrase: $phrase" >&2
    exit 20
  fi
done

# The Windows installer only copies a verified binary. It must not grow
# Docker/WSL/Tencent installation commands that would contradict the README.
if rg -ni --glob 'install.ps1' '(docker|wsl|tencent)' "$ROOT"; then
  printf '%s\n' 'Windows installer unexpectedly claims to install Docker, WSL, or Tencent.' >&2
  exit 20
fi
if ! rg -q --fixed-strings 'Set-BaronBinaryAcl' "$ROOT/install.ps1" || ! rg -q --fixed-strings 'Set-Acl' "$ROOT/install.ps1"; then
  printf '%s\n' 'Windows installer is missing its explicit ACL adaptation.' >&2
  exit 20
fi

printf '%s\n' 'Linux/Windows platform guidance scan passed.'
