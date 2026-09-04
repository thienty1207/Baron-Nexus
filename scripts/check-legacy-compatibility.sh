#!/usr/bin/env sh
set -eu

REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
GO_BIN=${GO_BIN:-go}
PROJECT=${1:-.}
BEFORE=${2:-"$PROJECT/.baron/compatibility-before.json"}
AFTER=${3:-}
GLOBAL=${4:-}
SQLITE=${5:-}

if [ ! -d "$PROJECT" ]; then
	printf '%s\n' "Legacy compatibility project root is not a directory: $PROJECT" >&2
	exit 2
fi
if [ ! -f "$BEFORE" ]; then
	printf '%s\n' "Legacy compatibility baseline is missing: $BEFORE" >&2
	printf '%s\n' 'Capture a baseline before running baron update; this gate never invents one.' >&2
	exit 2
fi

cd "$REPO"
export BARON_COMPATIBILITY_ROOT="$PROJECT"
export BARON_COMPATIBILITY_GLOBAL="$GLOBAL"
export BARON_COMPATIBILITY_SQLITE="$SQLITE"
export BARON_COMPATIBILITY_BEFORE="$BEFORE"
export BARON_COMPATIBILITY_AFTER="$AFTER"
"$GO_BIN" test ./internal/app -run '^TestLegacyCompatibilityGateFromEnvironment$' -count=1
printf '%s\n' 'Legacy compatibility gate passed without mutating the project or credentials.'
