# Baron Nexus v0.1.3

## Scope

`v0.1.3` makes validated DeepSeek API-key rotation discoverable for users who
already have Baron installed:

- canonical `baron deepseek api_key` command with visible interactive input;
- provider validation before DSH/Tencent persistence and no-overwrite-on-failure;
- compatibility alias `baron credentials set deepseek`;
- clearer Linux and Windows installer guidance when Baron is already installed;
- all `v0.1.2` automation, Tencent, Codex, DSH, and project-continuity behavior
  retained.

## Verification

The release candidate must pass the full Go test/vet suite, race tests in the
GCC-enabled `golang:1.27-bookworm` container, shell/static checks, release
checksum verification, and Linux/Windows artifact type and version smoke
checks before publication.

The release gate remains honest about external acceptance: clean
Ubuntu/Debian first-install, Windows runtime, and real provider replacement /
outage scenarios are not inferred from local fixtures.
