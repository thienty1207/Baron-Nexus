#!/usr/bin/env sh
set -eu

REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/baron-compatibility-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT HUP INT TERM

project="$TMP/project"
mkdir -p "$project"
before="$project/before.json"
after="$project/after.json"
printf '%s\n' '{"sqlite_schema":"schema-1","sqlite_counts":{"events":1}}' > "$before"
printf '%s\n' '{"sqlite_schema":"schema-1","sqlite_counts":{"events":1}}' > "$after"

output=$(sh "$REPO/scripts/check-legacy-compatibility.sh" "$project" "$before" "$after")
case "$output" in
	*'Legacy compatibility gate passed'*) ;;
	*)
		printf '%s\n' "$output" >&2
		printf '%s\n' 'legacy compatibility script did not report a passing fixture.' >&2
		exit 1
		;;
esac

if ! rg -q --fixed-strings 'BARON_COMPATIBILITY_BEFORE' "$REPO/scripts/check-legacy-compatibility.sh"; then
	printf '%s\n' 'legacy compatibility script does not pass the baseline manifest explicitly.' >&2
	exit 1
fi
if rg -n 'DEEPSEEK_API_KEY|LLM_API_KEY|sk-' "$REPO/scripts/check-legacy-compatibility.sh" "$REPO/acceptance/legacy-upgrade-fixture.json" >/dev/null; then
	printf '%s\n' 'legacy compatibility harness contains a credential-like value.' >&2
	exit 1
fi
printf '%s\n' 'Legacy compatibility harness contract passed.'
