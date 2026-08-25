#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
before=$(cksum "$ROOT/Baron-Nexus Implement Roadmap.md" "$ROOT/docs/implementation/FINAL_ACCEPTANCE_REPORT.md")
set +e
output=$("$ROOT/scripts/check-release-gate.sh" 2>&1)
status=$?
set -e
if [ "$status" -ne 21 ]; then
  printf '%s\n' "$output" >&2
  printf '%s\n' "expected the current incomplete roadmap to be blocked with exit 21" >&2
  exit 1
fi
case "$output" in
  *"Baron Nexus release gate: BLOCKED"*P19:*P22:*P27:*) ;;
  *)
    printf '%s\n' "$output" >&2
    printf '%s\n' "blocked gate output did not list all incomplete release phases" >&2
    exit 1
    ;;
esac
after=$(cksum "$ROOT/Baron-Nexus Implement Roadmap.md" "$ROOT/docs/implementation/FINAL_ACCEPTANCE_REPORT.md")
if [ "$before" != "$after" ]; then
  printf '%s\n' "release gate mutated roadmap/report" >&2
  exit 1
fi
printf '%s\n' "Baron Nexus release gate correctly remains BLOCKED until external acceptance is complete."
