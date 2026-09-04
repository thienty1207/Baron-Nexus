#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/baron-release-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT HUP INT TERM

set +e
output=$(BARON_VERSION=0.1.22 BARON_RELEASE_DIR="$TMP/release" BARON_MANAGED_RUNTIME_CATALOG="$TMP/missing-catalog.json" BARON_SOURCE_REVISION=test sh "$ROOT/scripts/build-release.sh" 2>&1)
status=$?
set -e
if [ "$status" -eq 0 ]; then
	printf '%s\n' "$output" >&2
	printf '%s\n' 'release build accepted a missing managed runtime catalog.' >&2
	exit 1
fi
case "$output" in
	*'managed runtime catalog'*) ;;
	*)
		printf '%s\n' "$output" >&2
		printf '%s\n' 'release build failure did not identify the managed runtime catalog.' >&2
		exit 1
		;;
esac

complete="$TMP/complete.json"
printf '%s' '{"releases":[' > "$complete"
first=1
for component in uv python strix bun go node npm pnpm dsh codex; do
	if [ "$first" -eq 0 ]; then
		printf '%s' ',' >> "$complete"
	fi
	first=0
	install_method=archive
	package=''
	entry_point=''
	case "$component" in
		strix) install_method=uv-tool; package='strix-agent'; entry_point='strix' ;;
		pnpm) install_method=archive; package='pnpm'; entry_point='pnpm' ;;
		dsh) install_method=pnpm; package='@deepseek-ai/dsh'; entry_point='dsh' ;;
		codex) install_method=pnpm; package='@openai/codex'; entry_point='codex' ;;
	esac
	package_fields=''
	if [ -n "$package" ]; then
		package_fields=",\"package\":\"$package\",\"entry_point\":\"$entry_point\""
	fi
	printf '%s' "{\"component\":\"$component\",\"version\":\"1.0.0\",\"stable\":true,\"install_method\":\"$install_method\"$package_fields,\"assets\":[{\"url\":\"https://example.invalid/$component-linux\",\"sha256\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"platform\":\"linux\",\"architecture\":\"amd64\"},{\"url\":\"https://example.invalid/$component-windows\",\"sha256\":\"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",\"platform\":\"windows\",\"architecture\":\"amd64\"}]}" >> "$complete"
done
printf '%s\n' ']}' >> "$complete"

release="$TMP/release-complete"
BARON_VERSION=0.1.22 BARON_RELEASE_DIR="$release" BARON_SOURCE_REVISION=test BARON_MANAGED_RUNTIME_CATALOG="$complete" sh "$ROOT/scripts/build-release.sh" >/dev/null
for artifact in baron-linux-amd64 baron-windows-amd64.exe managed-runtime-catalog.json release-manifest.json SHA256SUMS; do
	if [ ! -f "$release/$artifact" ]; then
		printf '%s\n' "release artifact is missing: $artifact" >&2
		exit 1
	fi
done
if ! (cd "$release" && sha256sum -c SHA256SUMS >/dev/null); then
	printf '%s\n' 'release artifact checksums did not verify.' >&2
	exit 1
fi
printf '%s\n' 'Release build catalog gate contract passed.'
