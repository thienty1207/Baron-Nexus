#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
old='Baron'" "'Shared Brain'
found=0

while IFS= read -r line; do
  case "$line" in
    *"Baron-Nexus Implement Roadmap.md:"*)
      continue
      ;;
  esac
  printf '%s\n' "Public branding regression: $line" >&2
  found=1
done <<EOF
$(rg -n --hidden --glob '!.git/**' --glob '!tmp/**' --glob '!*.sum' "$old" "$ROOT" || true)
EOF

if [ "$found" -ne 0 ]; then
  exit 20
fi

rg -q --fixed-strings 'Baron Nexus' "$ROOT/README.md"
rg -q --fixed-strings 'Baron Nexus' "$ROOT/internal/cli/cli.go"
printf '%s\n' 'Baron Nexus branding scan passed.'
