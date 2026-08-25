#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ROADMAP=${1:-"$ROOT/Baron-Nexus Implement Roadmap.md"}
REPORT=${2:-"$ROOT/docs/implementation/FINAL_ACCEPTANCE_REPORT.md"}

if [ ! -f "$ROADMAP" ]; then
  printf '%s\n' "Release gate cannot read roadmap: $ROADMAP" >&2
  exit 20
fi
if [ ! -f "$REPORT" ]; then
  printf '%s\n' "Release gate cannot read acceptance report: $REPORT" >&2
  exit 20
fi

unchecked=""
for phase in 19 22 27; do
  phase_items=$(awk -v phase="$phase" '
    $0 ~ "^## P" phase " " {inside=1; next}
    inside && $0 ~ "^## " {inside=0}
    inside && $0 ~ "^- \\[ \\] \\*\\*P" phase "-" {print}
  ' "$ROADMAP")
  if [ -n "$phase_items" ]; then
    unchecked=$unchecked$(printf 'P%s:\n%s\n' "$phase" "$phase_items")
  fi
done

if [ -n "$unchecked" ]; then
  if ! grep -q '^FINAL STATUS: BLOCKED$' "$REPORT"; then
    printf '%s\n' "Release gate is unsafe: unchecked acceptance exists but the report is not explicitly BLOCKED." >&2
    exit 20
  fi
  printf '%s\n' "Baron Nexus release gate: BLOCKED"
  printf '%s\n' "$unchecked"
  printf '%s\n' "See the acceptance report for the exact missing dependency/evidence."
  exit 21
fi

if grep -q '^FINAL STATUS: BLOCKED$' "$REPORT"; then
  printf '%s\n' "Release gate is unsafe: all acceptance checkboxes are checked but the report is still BLOCKED." >&2
  exit 20
fi

printf '%s\n' "Baron Nexus release gate: PASS"
