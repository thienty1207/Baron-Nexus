#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ROADMAP=${1:-"$ROOT/Baron-Nexus Implement Roadmap.md"}
REPORT=${2:-"$ROOT/docs/implementation/FINAL_ACCEPTANCE_REPORT.md"}
GO_BIN=${GO_BIN:-go}

if [ ! -f "$ROADMAP" ]; then
  printf '%s\n' "Release gate cannot read roadmap: $ROADMAP" >&2
  exit 20
fi
if [ ! -f "$REPORT" ]; then
  printf '%s\n' "Release gate cannot read acceptance report: $REPORT" >&2
  exit 20
fi

unchecked=""
append_unchecked() {
  if [ -n "$unchecked" ]; then
    unchecked="$unchecked
"
  fi
  unchecked="${unchecked}$1"
}
catalog="$ROOT/configs/managed-runtime-catalog.json"
if [ ! -f "$catalog" ] || [ -L "$catalog" ]; then
  append_unchecked "$(printf 'P15-CATALOG:\n- [ ] A validated managed-runtime-catalog.json is required for the full bundle.')"
elif ! "$GO_BIN" run "$ROOT/scripts/validate-managed-runtime-catalog.go" "$catalog" >/dev/null 2>&1; then
  append_unchecked "$(printf 'P15-CATALOG:\n- [ ] managed-runtime-catalog.json must pass the bundle validator.')"
fi
if [ ! -f "$ROOT/acceptance/legacy-upgrade-fixture.json" ] || [ ! -f "$ROOT/scripts/check-legacy-compatibility.sh" ]; then
  append_unchecked "$(printf 'P14-LEGACY:\n- [ ] Legacy v0.1.21 compatibility harness and fixture are required.')"
fi
for phase in 19 22 27; do
  phase_items=$(awk -v phase="$phase" '
    $0 ~ "^## P" phase " " {inside=1; next}
    inside && $0 ~ "^## " {inside=0}
    inside && $0 ~ "^- \\[ \\] \\*\\*P" phase "-" {print}
  ' "$ROADMAP")
  if [ -n "$phase_items" ]; then
    append_unchecked "$(printf 'P%s:\n%s' "$phase" "$phase_items")"
  fi
done

if [ -n "$unchecked" ]; then
  if ! grep -q '^FINAL STATUS: BLOCKED[[:space:]]*$' "$REPORT"; then
    printf '%s\n' "Release gate is unsafe: unchecked acceptance exists but the report is not explicitly BLOCKED." >&2
    exit 20
  fi
  printf '%s\n' "Baron Nexus release gate: BLOCKED"
  printf '%s\n' "$unchecked"
  printf '%s\n' "See the acceptance report for the exact missing dependency/evidence."
  exit 21
fi

if grep -q '^FINAL STATUS: BLOCKED[[:space:]]*$' "$REPORT"; then
  printf '%s\n' "Release gate is unsafe: all acceptance checkboxes are checked but the report is still BLOCKED." >&2
  exit 20
fi

printf '%s\n' "Baron Nexus release gate: PASS"
