#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
GO_BIN=${GO_BIN:-/usr/local/go/bin/go}
VERSION=${BARON_VERSION:-0.1.0}
OUT=${BARON_RELEASE_DIR:-"$ROOT/dist/$VERSION"}
SOURCE_REVISION=${BARON_SOURCE_REVISION:-unknown}
if [ "$SOURCE_REVISION" = "unknown" ]; then
  SOURCE_REVISION=$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || true)
  if [ -z "$SOURCE_REVISION" ]; then
    SOURCE_REVISION=unknown
  fi
fi
mkdir -p "$OUT"

if [ -d "$ROOT/target" ]; then
  printf '%s\n' 'Refusing release: Rust target/ output is present in the repository.' >&2
  exit 20
fi

if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  printf '%s\n' 'BARON_VERSION must be a semantic version such as 0.1.0.' >&2
  exit 2
fi

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$GO_BIN" build -trimpath -ldflags "-s -w -X github.com/baron-shared-brain/baron/internal/version.Value=$VERSION" -o "$OUT/baron-linux-amd64" "$ROOT/cmd/baron"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 "$GO_BIN" build -trimpath -ldflags "-s -w -X github.com/baron-shared-brain/baron/internal/version.Value=$VERSION" -o "$OUT/baron-windows-amd64.exe" "$ROOT/cmd/baron"
cp "$ROOT/install.sh" "$OUT/install.sh"
cp "$ROOT/install.ps1" "$OUT/install.ps1"
"$GO_BIN" version > "$OUT/GO_TOOLCHAIN.txt"
"$GO_BIN" list -m all > "$OUT/SBOM_MODULES.txt"
cat > "$OUT/release-manifest.json" <<EOF
{
  "project": "Baron Nexus",
  "version": "$VERSION",
  "source_revision": "$SOURCE_REVISION",
  "go_toolchain": "$(tr -d '\n' < "$OUT/GO_TOOLCHAIN.txt")",
  "cgo": false,
  "artifacts": ["baron-linux-amd64", "baron-windows-amd64.exe"],
  "installers": ["install.sh", "install.ps1"],
  "sbom": "SBOM_MODULES.txt",
  "linux_bootstrap": "baron tencent-memory init installs Docker only on supported Ubuntu/Debian after sudo preflight",
  "windows_prerequisites": "Docker Desktop, WSL2, Ubuntu, and Tencent services are user-installed; Baron does not claim silent UI automation",
  "rollback": "baron repair plus native binary/Tencent pinned-deployment rollback"
}
EOF
(cd "$OUT" && sha256sum baron-linux-amd64 baron-windows-amd64.exe install.sh install.ps1 release-manifest.json GO_TOOLCHAIN.txt SBOM_MODULES.txt > SHA256SUMS)
printf '%s\n' "Release artifacts written to $OUT"
