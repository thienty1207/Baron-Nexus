# Baron Shared Brain — Final Acceptance Report

## Candidate

- Source revision: `19d6e6e2bdf9597efbe5b488eaff42ff4a26b7c6`
- Version: `0.1.0`
- Toolchain: `go1.27.0 linux/amd64`
- Release directory: `/tmp/baron-release.candidate.ju8tHW`
- Linux SHA-256: `0727fc0ccc85b0fef2557bb984ce4c6d94ebfb5289f2ed614197179dd31f1058`
- Windows SHA-256: `48e799b13e35e66616e891ba88c75f3c8f24f066efb212e87a6344f388828aa6`

## Implemented surface

- Native Go CLI with the frozen command surface, stable exit codes, JSON
  readiness output, atomic configuration writes, backup/restore, and binary
  rollback support.
- Project-local identity and state under `.baron/`, including restrictive
  `.env` permissions, Git-ignore merging, move-stable IDs, symlink/tamper
  rejection, SQLite WAL journaling, durable queue state, checkpoints, and
  recovery packets.
- Local-first hook processing for Codex and DSH with canonical events,
  bounded/redacted logs, idempotency, fail-open client responses, and strict
  project/team/agent/user isolation on remote requests.
- Pinned DSH installation/profile integration, embedded Baron DSH adapter,
  DuckDuckGo MCP profile patch, official nested Codex hook shape, Tencent v3
  client, identity provisioning, and managed deployment bootstrap code.
- Linux amd64 and Windows amd64 CGO-free release artifacts, checksum manifest,
  module SBOM, shell/PowerShell installers, and update rollback logic.

## Verification evidence

The following commands passed from the repository root:

- `GOTOOLCHAIN=local /usr/local/go/bin/go test -count=1 ./...`
- `GOTOOLCHAIN=local /usr/local/go/bin/go vet ./...`
- `GOTOOLCHAIN=local CGO_ENABLED=0 /usr/local/go/bin/go test -count=1 ./...`
- `git diff --check`
- `sh -n install.sh scripts/build-release.sh`
- Release `SHA256SUMS` verification for both binaries, manifest, toolchain,
  and SBOM files.
- Linux artifact `--help` smoke test.
- Isolated temporary-project smoke: `setup`, JSON `status`, Codex hook input,
  project-local state creation, and mode `0600` `.env`.
- Installer SHA verification and refusal to overwrite an existing binary
  without `BARON_ALLOW_REPLACE=1` (exit 20).

The release directory contains a static Linux ELF and a Windows PE32+ binary;
the manifest embeds the candidate source revision above.

## Blocked acceptance gates

These are environment limitations, not converted mock passes:

- Docker and the TencentDB Agent Memory stack are unavailable, so live
  provisioning, health/auth, volume, and remote continuity gates did not run.
- Node/npm/npx/pnpm, uv/uvx, DSH, DeepSeek credentials, and interactive Codex
  authentication are unavailable, so real client/plugin/hook lifecycle gates
  did not run.
- Windows execution and PowerShell installer runtime checks are unavailable.
- `go test -race ./...` cannot run: the default invocation requires cgo, and
  `CGO_ENABLED=1` fails because `gcc` is absent.

## Final status

FINAL STATUS: BLOCKED
