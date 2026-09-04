#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
GO_BIN=${GO_BIN:-go}
TMP=$(mktemp -d "${TMPDIR:-/tmp}/baron-catalog-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT HUP INT TERM

incomplete="$TMP/incomplete.json"
printf '%s\n' '{"releases":[{"component":"bun","version":"1.0.0","stable":true,"install_method":"archive","assets":[{"url":"https://example.invalid/bun","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","platform":"linux","architecture":"amd64"}]}]}' > "$incomplete"
if "$GO_BIN" run "$ROOT/scripts/validate-managed-runtime-catalog.go" "$incomplete" >/dev/null 2>&1; then
  printf '%s\n' 'incomplete managed runtime catalog was accepted.' >&2
  exit 1
fi

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
    pnpm) install_method=npm; package='pnpm'; entry_point='pnpm' ;;
    dsh) install_method=npm; package='@deepseek-ai/dsh'; entry_point='dsh' ;;
    codex) install_method=npm; package='@openai/codex'; entry_point='codex' ;;
  esac
  package_fields=''
  if [ -n "$package" ]; then
    package_fields=",\"package\":\"$package\",\"entry_point\":\"$entry_point\""
  fi
  printf '%s' "{\"component\":\"$component\",\"version\":\"1.0.0\",\"stable\":true,\"install_method\":\"$install_method\"$package_fields,\"assets\":[{\"url\":\"https://example.invalid/$component-linux\",\"sha256\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"platform\":\"linux\",\"architecture\":\"amd64\"},{\"url\":\"https://example.invalid/$component-windows\",\"sha256\":\"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",\"platform\":\"windows\",\"architecture\":\"amd64\"}]}" >> "$complete"
done
printf '%s\n' ']}' >> "$complete"

output=$("$GO_BIN" run "$ROOT/scripts/validate-managed-runtime-catalog.go" "$complete")
case "$output" in
  *'10 required components'*) ;;
  *)
    printf '%s\n' "$output" >&2
    printf '%s\n' 'complete managed runtime catalog did not report the required component count.' >&2
    exit 1
    ;;
esac
printf '%s\n' 'Managed runtime catalog validator contract passed.'
