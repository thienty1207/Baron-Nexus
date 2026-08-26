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
HOST_TMP_ROOT=

cleanup() {
  if [ -n "$TMP_ROOT" ] && [ -d "$TMP_ROOT" ]; then
    rm -rf -- "$TMP_ROOT"
  fi
  if [ -n "$HOST_TMP_ROOT" ] && [ -d "$HOST_TMP_ROOT" ]; then
    rm -rf -- "$HOST_TMP_ROOT"
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
  printf '%s\n' "Baron is already installed at $DEST. Run 'baron update' to update Baron or 'baron deepseek api_key' to change the DeepSeek API key. Do not rerun ./install.sh; set BARON_ALLOW_REPLACE=1 only for an explicit binary migration." >&2
  exit 20
fi

sudo_retry() {
  if sudo -n "$@"; then
    return 0
  fi
  printf '%s\n' 'Baron sudo authorization expired; authenticate again to continue the host bootstrap.' >&2
  if ! sudo -v || ! sudo -n -v; then
    printf '%s\n' 'Baron could not revalidate sudo authorization; rerun sudo -v and retry.' >&2
    return 20
  fi
  sudo -n "$@"
}

sudo_check() {
  sudo -n "$@" >/dev/null 2>&1
}

node_supported() {
  command -v node >/dev/null 2>&1 || return 1
  NODE_RAW=$(node --version 2>/dev/null || true)
  NODE_MAJOR=$(printf '%s' "$NODE_RAW" | sed -n 's/^v\([0-9][0-9]*\)\..*/\1/p')
  NODE_MINOR=$(printf '%s' "$NODE_RAW" | sed -n 's/^v[0-9][0-9]*\.\([0-9][0-9]*\).*/\1/p')
  [ -n "$NODE_MAJOR" ] && [ -n "$NODE_MINOR" ] || return 1
  if [ "$NODE_MAJOR" -ge 24 ]; then
    return 0
  fi
  [ "$NODE_MAJOR" -eq 22 ] && [ "$NODE_MINOR" -ge 19 ]
}

node_repository_architecture() {
  case "$(uname -m)" in
    x86_64|amd64) printf '%s' 'amd64' ;;
    aarch64|arm64) printf '%s' 'arm64' ;;
    armv7l|armv6l) printf '%s' 'armhf' ;;
    ppc64le) printf '%s' 'ppc64el' ;;
    *) return 1 ;;
  esac
}

docker_repository_architecture() {
  case "$(uname -m)" in
    x86_64|amd64) printf '%s' 'amd64' ;;
    aarch64|arm64) printf '%s' 'arm64' ;;
    armv7l|armv6l) printf '%s' 'armhf' ;;
    ppc64le) printf '%s' 'ppc64el' ;;
    s390x) printf '%s' 's390x' ;;
    *) return 1 ;;
  esac
}

bootstrap_linux_dependencies() {
  [ -r /etc/os-release ] || {
    printf '%s\n' 'Baron host bootstrap requires /etc/os-release for Ubuntu/Debian detection.' >&2
    exit 14
  }
  ID=
  VERSION_ID=
  VERSION_CODENAME=
  UBUNTU_CODENAME=
  . /etc/os-release
  case "${ID:-}" in
    ubuntu|debian) ;;
    *) printf '%s\n' "Automatic host bootstrap supports Ubuntu/Debian only; detected ${ID:-unknown}." >&2; exit 14 ;;
  esac
  HOST_TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/baron-host.XXXXXX")
  mkdir -p -- "$HOME/.local/bin"

  if ! command -v apt-get >/dev/null 2>&1; then
    printf '%s\n' 'apt-get is required for the Ubuntu/Debian host bootstrap.' >&2
    exit 10
  fi
  if ! command -v curl >/dev/null 2>&1 || ! command -v sha256sum >/dev/null 2>&1; then
    if ! sudo_retry apt-get update || ! sudo_retry apt-get install -y ca-certificates curl coreutils; then
      printf '%s\n' 'Baron could not install the base download tools through apt.' >&2
      exit 10
    fi
  fi

  if ! node_supported || ! command -v npm >/dev/null 2>&1 || ! command -v npx >/dev/null 2>&1; then
    NODE_ARCH=$(node_repository_architecture) || {
      printf '%s\n' "Unsupported Linux architecture for Node bootstrap: $(uname -m)." >&2
      exit 14
    }
    if ! sudo_retry apt-get update || ! sudo_retry apt-get install -y ca-certificates gnupg; then
      printf '%s\n' 'Baron could not install Node bootstrap prerequisites.' >&2
      exit 10
    fi
    NODE_KEY="$HOST_TMP_ROOT/nodesource.asc"
    NODE_KEYRING="$HOST_TMP_ROOT/nodesource.gpg"
    NODE_SOURCES="$HOST_TMP_ROOT/nodesource.sources"
    curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
      https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key -o "$NODE_KEY"
    gpg --batch --yes --dearmor --output "$NODE_KEYRING" "$NODE_KEY"
    printf '%s\n' \
      'Types: deb' \
      'URIs: https://deb.nodesource.com/node_22.x' \
      'Suites: nodistro' \
      'Components: main' \
      "Architectures: $NODE_ARCH" \
      'Signed-By: /etc/apt/keyrings/nodesource.gpg' \
      '' > "$NODE_SOURCES"
    sudo_retry install -m 0755 -d /etc/apt/keyrings
    sudo_retry install -m 0644 "$NODE_KEYRING" /etc/apt/keyrings/nodesource.gpg
    sudo_retry install -m 0644 "$NODE_SOURCES" /etc/apt/sources.list.d/nodesource.sources
    sudo_retry apt-get update
    sudo_retry apt-get install -y nodejs
    node_supported || {
      printf '%s\n' 'Node bootstrap completed with an unsupported Node version; expected Node 22.19+ or 24+.' >&2
      exit 10
    }
  fi
  if ! command -v npm >/dev/null 2>&1 || ! command -v npx >/dev/null 2>&1; then
    sudo_retry apt-get install -y npm
  fi
  command -v npm >/dev/null 2>&1 && command -v npx >/dev/null 2>&1 || {
    printf '%s\n' 'Node is installed but npm/npx are not available on PATH.' >&2
    exit 10
  }

  if ! command -v pnpm >/dev/null 2>&1; then
    sudo_retry npm install --global pnpm@latest
  fi
  command -v pnpm >/dev/null 2>&1 || {
    printf '%s\n' 'pnpm was installed but is not available on PATH.' >&2
    exit 10
  }

  if ! command -v uv >/dev/null 2>&1 || ! command -v uvx >/dev/null 2>&1; then
    case "$(uname -m)" in
      x86_64|amd64) UV_ARCH=x86_64 ;;
      aarch64|arm64) UV_ARCH=aarch64 ;;
      ppc64le) UV_ARCH=powerpc64le ;;
      s390x) UV_ARCH=s390x ;;
      *) printf '%s\n' "Unsupported Linux architecture for uv bootstrap: $(uname -m)." >&2; exit 14 ;;
    esac
    UV_ASSET="uv-${UV_ARCH}-unknown-linux-gnu.tar.gz"
    UV_ARCHIVE="$HOST_TMP_ROOT/$UV_ASSET"
    UV_SUM="$UV_ARCHIVE.sha256"
    curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
      "https://github.com/astral-sh/uv/releases/latest/download/$UV_ASSET" -o "$UV_ARCHIVE"
    curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
      "https://github.com/astral-sh/uv/releases/latest/download/$UV_ASSET.sha256" -o "$UV_SUM"
    UV_EXPECTED=$(awk '{print $1; exit}' "$UV_SUM")
    UV_ACTUAL=$(sha256sum "$UV_ARCHIVE" | awk '{print $1}')
    [ -n "$UV_EXPECTED" ] && [ "$UV_EXPECTED" = "$UV_ACTUAL" ] || {
      printf '%s\n' 'Refusing to install uv: release checksum verification failed.' >&2
      exit 20
    }
    UV_EXTRACT="$HOST_TMP_ROOT/uv-extract"
    mkdir -p -- "$UV_EXTRACT"
    tar -xzf "$UV_ARCHIVE" -C "$UV_EXTRACT"
    UV_BIN=$(find "$UV_EXTRACT" -type f -name uv -print -quit)
    UVX_BIN=$(find "$UV_EXTRACT" -type f -name uvx -print -quit)
    [ -n "$UV_BIN" ] && [ -n "$UVX_BIN" ] || {
      printf '%s\n' 'The uv release archive did not contain both uv and uvx.' >&2
      exit 20
    }
    install -m 0755 "$UV_BIN" "$HOME/.local/bin/uv"
    install -m 0755 "$UVX_BIN" "$HOME/.local/bin/uvx"
    PATH="$HOME/.local/bin:$PATH"
    export PATH
  fi
  command -v uv >/dev/null 2>&1 && command -v uvx >/dev/null 2>&1 || {
    printf '%s\n' 'uv/uvx are not available on PATH after bootstrap.' >&2
    exit 10
  }

  docker_ready=0
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    docker_ready=1
  elif command -v docker >/dev/null 2>&1 && sudo_check docker info; then
    docker_ready=1
  fi
  if [ "$docker_ready" -ne 1 ]; then
    DOCKER_CODENAME=${VERSION_CODENAME:-${UBUNTU_CODENAME:-}}
    [ -n "$DOCKER_CODENAME" ] || {
      printf '%s\n' "Could not determine the ${ID} release codename for Docker." >&2
      exit 14
    }
    DOCKER_ARCH=$(docker_repository_architecture) || {
      printf '%s\n' "Unsupported Linux architecture for Docker bootstrap: $(uname -m)." >&2
      exit 14
    }
    sudo_retry apt-get update
    sudo_retry apt-get install -y ca-certificates gnupg
    DOCKER_KEY="$HOST_TMP_ROOT/docker.asc"
    DOCKER_KEYRING="$HOST_TMP_ROOT/docker.gpg"
    DOCKER_SOURCES="$HOST_TMP_ROOT/docker.sources"
    curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
      "https://download.docker.com/linux/$ID/gpg" -o "$DOCKER_KEY"
    gpg --batch --yes --dearmor --output "$DOCKER_KEYRING" "$DOCKER_KEY"
    printf '%s\n' \
      'Types: deb' \
      "URIs: https://download.docker.com/linux/$ID" \
      "Suites: $DOCKER_CODENAME" \
      'Components: stable' \
      "Architectures: $DOCKER_ARCH" \
      'Signed-By: /etc/apt/keyrings/docker.gpg' \
      '' > "$DOCKER_SOURCES"
    sudo_retry install -m 0755 -d /etc/apt/keyrings
    sudo_retry install -m 0644 "$DOCKER_KEYRING" /etc/apt/keyrings/docker.gpg
    sudo_retry install -m 0644 "$DOCKER_SOURCES" /etc/apt/sources.list.d/docker.sources
    sudo_retry apt-get update
    sudo_retry apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    sudo_retry systemctl enable --now docker
    sudo_retry docker info >/dev/null
  fi
  printf '%s\n' 'Ubuntu/Debian host dependencies are ready: Docker, Node/npm/npx, pnpm, and uv/uvx.'
}

case "$(uname -s)" in
  Linux) bootstrap_linux_dependencies ;;
esac

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
    *) printf '%s\n' 'BARON_RELEASE_VERSION must be latest or a semantic version such as 0.1.3.' >&2; exit 2 ;;
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
