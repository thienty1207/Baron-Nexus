#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
GO_BIN=${GO_BIN:-/usr/local/go/bin/go}
VERSION=${BARON_VERSION:-dev}
OUT=${BARON_RELEASE_DIR:-"$ROOT/dist/$VERSION"}
mkdir -p "$OUT"

if [ -d "$ROOT/target" ]; then
  printf '%s\n' 'Refusing release: Rust target/ output is present in the repository.' >&2
  exit 20
fi

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$GO_BIN" build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/baron-linux-amd64" "$ROOT/cmd/baron"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 "$GO_BIN" build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/baron-windows-amd64.exe" "$ROOT/cmd/baron"
"$GO_BIN" version > "$OUT/GO_TOOLCHAIN.txt"
"$GO_BIN" list -m all > "$OUT/SBOM_MODULES.txt"
cat > "$OUT/release-manifest.json" <<EOF
{
  "project": "Baron Shared Brain",
  "version": "$VERSION",
  "source_revision": "${BARON_SOURCE_REVISION:-unknown}",
  "go_toolchain": "$(tr -d '\n' < "$OUT/GO_TOOLCHAIN.txt")",
  "cgo": false,
  "artifacts": ["baron-linux-amd64", "baron-windows-amd64.exe"],
  "sbom": "SBOM_MODULES.txt"
}
EOF
(cd "$OUT" && sha256sum baron-linux-amd64 baron-windows-amd64.exe release-manifest.json GO_TOOLCHAIN.txt SBOM_MODULES.txt > SHA256SUMS)
printf '%s\n' "Release artifacts written to $OUT"
