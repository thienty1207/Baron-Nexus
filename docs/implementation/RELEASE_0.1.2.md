# Baron Nexus v0.1.2

## Scope

`v0.1.2` packages the approved validated-credential and Ubuntu/Debian
first-install automation work from P28:

- visible DeepSeek key entry with an explicit warning and live provider
  validation before persistence;
- protected DSH/Tencent credential rotation through
  `baron deepseek api_key` (with the old command retained as an alias);
- sudo-first, bounded reauthorization for Go-managed system operations;
- automatic Ubuntu/Debian installation of Docker Engine/Compose,
  Node/npm/npx, pnpm, and checksum-verified uv/uvx;
- matching `./install.sh` behavior and explicit Windows prerequisite guidance.

## Verification

The release candidate must pass the repository's full Go test/vet suite,
race tests in the GCC-enabled `golang:1.27-bookworm` container, shell/static
checks, release checksum verification, and Linux/Windows artifact type and
version smoke checks before publication.

The release workflow publishes artifacts only from the `v0.1.2` tag. Clean
Ubuntu/Debian first-install, Windows runtime, and real provider replacement /
outage acceptance remain external gates and are not inferred from local
fixtures.
