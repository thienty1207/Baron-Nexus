# Baron Nexus Release Install and Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add verified `baron --version`, `baron install`, and `baron update` for Baron Nexus `0.1.0`, publish reproducible GitHub release assets, and preserve all existing project/Tencent state during binary updates.

**Architecture:** Keep the existing CLI and atomic `internal/install.UpdateBinary` primitive. Add a small release client that reads GitHub release metadata, selects the native asset, verifies the manifest/checksum, validates the candidate version, and delegates replacement/rollback to the existing primitive. Keep first bootstrap in the shell/PowerShell installers and use the same release protocol for later CLI install/update operations.

**Tech Stack:** Go 1.27, Cobra, `net/http`, SHA-256, shell, PowerShell, GitHub Actions, existing Go test suite.

**Spec:** `docs/superpowers/specs/2026-08-25-baron-release-install-update-design.md`

## Global Constraints

- Initial Baron Nexus release version is exactly `0.1.0`.
- The existing `deepseek-harness`, `codex-cli`, `tencent-memory`, `setup`, `test`, `status`, `doctor`, `repair`, `backup`, and `restore` commands remain compatible.
- No source code, `.baron` project identity, local continuity database, credentials, Tencent metadata, or Tencent Docker volume is mutated by a binary update.
- Production release downloads use HTTPS GitHub release metadata/assets and verify the exact binary SHA-256 before replacement.
- Never print API keys, user keys, admin keys, authorization headers, or release response bodies.
- Do not add Rust/Cargo or CGO requirements.
- Do not mark external clean-machine, Windows runtime, or publication acceptance green from fixtures alone.

### Task 1: Central version and CLI contract

**Files:**
- Create: `internal/version/version.go`
- Modify: `cmd/baron/main.go`
- Modify: `internal/cli/cli.go`
- Modify: `internal/app/app.go`
- Test: `internal/cli/cli_test.go`

- [X] Add a default `0.1.0` version variable that can be overridden by release `-ldflags`.
- [X] Add `Version` to CLI options and configure Cobra so `baron --version` prints `baron 0.1.0`.
- [X] Add `Install` and `Update` callbacks to the CLI options and register exact-argument commands.
- [X] Write tests for exact output, command registration, and callback error/exit propagation.
- [X] Run the focused CLI tests and confirm the new tests fail before production implementation, then pass after implementation.

### Task 2: Verified GitHub release client

**Files:**
- Create: `internal/release/release.go`
- Create: `internal/release/release_test.go`
- Modify: `internal/install/update.go` only if the release client needs a narrowly scoped validation hook.

- [X] Define release metadata/assets and a client with injectable HTTP client, API base, repository, executable path, OS, and architecture.
- [X] Implement latest-release metadata fetch with bounded JSON reads, HTTPS/default GitHub validation, timeout, and safe user-agent headers.
- [X] Select only supported `linux/amd64` and `windows/amd64` assets.
- [X] Download the selected binary, `release-manifest.json`, and `SHA256SUMS` with size limits.
- [X] Verify tag/manifest version, exact asset name, and SHA-256 before any target mutation.
- [X] Validate the candidate by executing it with `--version` and requiring the expected version.
- [X] Delegate atomic replacement and automatic rollback to `UpdateBinary`.
- [X] Add tests for success, checksum mismatch, manifest mismatch, missing asset, oversized response, unsupported platform, validation failure, and preserved rollback artifact.
- [X] Run the focused release package tests and verify each failure test fails for the intended reason before implementation.

### Task 3: Wire `baron install` and `baron update`

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/cli/cli.go`
- Create or modify: `internal/app/release.go` if release wiring deserves a separate boundary.
- Test: `internal/app/app_test.go`, `internal/cli/cli_test.go`

- [X] Resolve the managed executable path without following unsafe symlinks or mutating an arbitrary path; allow an explicit test/install override.
- [X] Make `baron install` fetch and install the current latest verified release.
- [X] Make `baron update` report an idempotent no-op when the installed version equals the latest release and update otherwise.
- [X] Ensure release operations do not open project state or contact Tencent.
- [X] Return actionable messages for target/validation failures; leave live Windows replacement/restart acceptance as an external gate.
- [X] Add app-level tests proving no project/Tencent state mutation and correct release-client wiring.

### Task 4: First-download installers and release pipeline

**Files:**
- Modify: `install.sh`
- Modify: `install.ps1`
- Modify: `scripts/build-release.sh`
- Create: `.github/workflows/release.yml`
- Test: `scripts/check-release-gate_test.sh` or a focused release-script test.

- [X] Make shell and PowerShell installers download the versioned native asset from the default GitHub release, verify `SHA256SUMS`, and refuse silent overwrite.
- [X] Preserve the local `BARON_BINARY_SOURCE`/explicit source path as a test and offline installation escape hatch.
- [X] Set build default to `0.1.0`, inject the version consistently, and include install/update protocol fields in `release-manifest.json`.
- [X] Add a tag-triggered GitHub Actions release workflow that builds, checksums, and publishes Linux/Windows assets, installers, manifest, SBOM, and toolchain metadata.
- [X] Test shell syntax, PowerShell static structure, local release build, artifact launch, and checksum verification.

### Task 5: Documentation and roadmap evidence

**Files:**
- Modify: `README.md`
- Modify: `Baron-Nexus Implement Roadmap.md`
- Modify: `docs/implementation/IMPLEMENT_PROGRESS.md`
- Modify: `docs/implementation/FINAL_ACCEPTANCE_REPORT.md`

- [X] Document first bootstrap, exact `baron --version`, `baron install`, and `baron update` commands.
- [X] Explain that the first bootstrap downloads the binary installer and later updates use the verified release protocol.
- [X] Keep DSH/Codex/Tencent component commands separate as previously agreed.
- [X] Mark only locally verified release implementation rows as `[X]`; leave public GitHub release, clean-machine, Windows runtime, and other external gates unchecked until evidence exists.

### Task 6: Full verification and GitHub publication

**Files:**
- Inspect all changed files; no additional production file unless a verification failure requires it.

- [X] Run `gofmt -w` on changed Go files and `git diff --check`.
- [X] Run full Go tests, vet, CGO-free tests, release build, checksum verification, branding/platform/adapter/security scans, and release-gate tests.
- [X] Run the binary smoke tests for `baron --version`, `baron install`, and `baron update` against a local HTTP fixture.
- [X] Scan the complete staged tree for credentials and exclude `.baron` runtime/state/credential files.
- [ ] Commit the source and documentation as Baron Nexus `0.1.0`.
- [ ] Add `https://github.com/thienty1207/Baron-Nexus.git` as `origin` and push the source branch/main plus `v0.1.0` tag only after verification.
- [ ] Verify the remote commit/tag and report whether GitHub Actions created the release; do not claim public download/update until the release asset URLs are confirmed.
