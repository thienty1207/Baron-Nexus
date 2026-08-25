#!/usr/bin/env sh
set -eu

# The installer downloads only a verified GitHub release by default. A local
# BARON_BINARY_SOURCE remains available for offline/package tests.
REPOSITORY=${BARON_RELEASE_REPOSITORY:-thienty1207/Baron-Nexus}
RELEASE_VERSION=${BARON_RELEASE_VERSION:-latest}
SOURCE=${BARON_BINARY_SOURCE:-}
DEST=${BARON_INSTALL_PATH:-"$HOME/.local/bin/baron"}
EXPECTED_SHA256=${BARON_BINARY_SHA256:-}
TMP_ROOT=

cleanup() {
  if [ -n "$TMP_ROOT" ] && [ -d "$TMP_ROOT" ]; then
    rm -rf -- "$TMP_ROOT"
  fi
}
trap cleanup EXIT HUP INT TERM

if ! printf '%s' "$REPOSITORY" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$'; then
  printf '%s\n' 'Invalid BARON_RELEASE_REPOSITORY; expected owner/repository.' >&2
  exit 2
fi

case "$(uname -s)" in
  Linux)
    command -v sudo >/dev/null 2>&1 || {
      printf '%s\n' 'sudo is required before Baron can download and install the Linux release.' >&2
      exit 20
    }
    if ! sudo -v; then
      printf '%s\n' 'sudo authorization is required before Baron can download and install the Linux release.' >&2
      exit 20
    fi
    if ! sudo -n -v; then
      printf '%s\n' 'Baron could not verify sudo authorization; rerun from an interactive terminal.' >&2
      exit 20
    fi
    ;;
esac

if [ -e "$DEST" ] && [ "${BARON_ALLOW_REPLACE:-0}" != "1" ]; then
  printf '%s\n' "Refusing to overwrite existing baron command at $DEST; set BARON_ALLOW_REPLACE=1 only for an explicit migration." >&2
  exit 20
fi

if [ -n "$SOURCE" ]; then
  if [ ! -f "$SOURCE" ]; then
    printf '%s\n' "BARON_BINARY_SOURCE is not a regular file: $SOURCE" >&2
    exit 2
  fi
  if [ -n "$EXPECTED_SHA256" ]; then
    ACTUAL_SHA256=$(sha256sum "$SOURCE" | awk '{print $1}')
    if [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
      printf '%s\n' 'Refusing to install: binary SHA-256 does not match BARON_BINARY_SHA256.' >&2
      exit 20
    fi
  fi
else
  command -v curl >/dev/null 2>&1 || { printf '%s\n' 'curl is required for Baron release installation.' >&2; exit 10; }
  command -v sha256sum >/dev/null 2>&1 || { printf '%s\n' 'sha256sum is required for Baron release installation.' >&2; exit 10; }
  case "$RELEASE_VERSION" in
    latest) BASE_URL="https://github.com/$REPOSITORY/releases/latest/download" ;;
    v*) BASE_URL="https://github.com/$REPOSITORY/releases/download/$RELEASE_VERSION" ;;
    [0-9]*.[0-9]*.[0-9]*) BASE_URL="https://github.com/$REPOSITORY/releases/download/v$RELEASE_VERSION" ;;
    *) printf '%s\n' 'BARON_RELEASE_VERSION must be latest or a semantic version such as 0.1.1.' >&2; exit 2 ;;
  esac
  case "$(uname -s):$(uname -m)" in
    Linux:x86_64|Linux:amd64) ASSET=baron-linux-amd64 ;;
    *) printf '%s\n' 'Baron shell installer currently supports Linux amd64 only; use install.ps1 on Windows.' >&2; exit 14 ;;
  esac
  TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/baron-install.XXXXXX")
  curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 "$BASE_URL/release-manifest.json" -o "$TMP_ROOT/release-manifest.json"
  curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 "$BASE_URL/SHA256SUMS" -o "$TMP_ROOT/SHA256SUMS"
  curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 "$BASE_URL/$ASSET" -o "$TMP_ROOT/$ASSET"
  chmod 0755 "$TMP_ROOT/$ASSET"
  RELEASE_MANIFEST_VERSION=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)".*/\1/p' "$TMP_ROOT/release-manifest.json" | head -n 1)
  if [ -z "$RELEASE_MANIFEST_VERSION" ]; then
    printf '%s\n' 'Baron release manifest has no valid version.' >&2
    exit 20
  fi
  case "$RELEASE_VERSION" in
    latest) ;;
    v*) [ "$RELEASE_MANIFEST_VERSION" = "${RELEASE_VERSION#v}" ] || { printf '%s\n' 'Baron release tag and manifest version differ.' >&2; exit 20; } ;;
    *) [ "$RELEASE_MANIFEST_VERSION" = "$RELEASE_VERSION" ] || { printf '%s\n' 'Baron release tag and manifest version differ.' >&2; exit 20; } ;;
  esac
  EXPECTED_SHA256=$(awk -v name="$ASSET" '$2 == name || $2 == "*" name {print $1; exit}' "$TMP_ROOT/SHA256SUMS")
  ACTUAL_SHA256=$(sha256sum "$TMP_ROOT/$ASSET" | awk '{print $1}')
  if [ -z "$EXPECTED_SHA256" ] || [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
    printf '%s\n' "Refusing to install: SHA-256 verification failed for $ASSET." >&2
    exit 20
  fi
  if [ "$("$TMP_ROOT/$ASSET" --version 2>/dev/null || true)" != "baron $RELEASE_MANIFEST_VERSION" ]; then
    printf '%s\n' 'Refusing to install: downloaded Baron binary failed version validation.' >&2
    exit 20
  fi
  SOURCE="$TMP_ROOT/$ASSET"
fi

mkdir -p -- "$(dirname -- "$DEST")"
install -m 0755 -- "$SOURCE" "$DEST"
printf '%s\n' "Installed $DEST"
