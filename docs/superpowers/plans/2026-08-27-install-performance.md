# Install Performance Optimization Implementation Plan
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make repeated `baron install` runs perform live latest-version discovery while skipping downloads and mutations for components whose verified local state already matches upstream, without changing the existing release, credential, sudo, Docker repair, DSH, Codex, or Tencent safety contracts.

**Architecture:** Split bootstrap into discovery/planning and deterministic mutation/validation. Discovery uses injectable command runners and a shared bounded HTTP client, may run concurrently with a small limit, and produces component state reports. Mutations consume those reports sequentially. Existing public install functions remain compatible through wrappers while new report-returning seams expose whether state actually changed.

**Tech Stack:** Go 1.27, Cobra, `net/http`, `sync`, existing `CommandRunner`, `FileDownloader`, `ProgressReporter`, GitHub release APIs, npm CLI, apt/dpkg, and the current table-driven test fixtures.

**Spec:** `docs/superpowers/specs/2026-08-27-install-performance-design.md`

## Global Constraints

- Do not add Rust, a CodeGraph/indexing subsystem, or changes to Tencent Memory and project knowledge behavior.
- Preserve HTTPS restrictions, release manifests, SHA256 checksums, candidate validation, atomic replacement, rollback, credential redaction, sudo boundaries, and existing Docker repair behavior.
- Every latest check is live for the current run. No persistent or long-lived latest-version cache is introduced.
- Discovery must not mutate the system. Global npm installs, apt/dpkg operations, Docker daemon operations, binary replacement, adapter/config writes, and plugin changes remain sequential.
- Use `apply_patch` for manual edits. For every production change, add or update a focused test first, run it red, implement the smallest change, then run it green.
- Preserve explicit-version installation behavior and existing injectable test seams.
- Progress output may include component names, phases, versions, and actionable errors, but never raw child-process output that can contain secrets.
- Keep the existing untracked `BARON_RUST_CODE_INTELLIGENCE_IDEA.md` untouched.

## 1. Establish State and Version Primitives

**Files:** `internal/install/dependencies.go` (new), `internal/install/dependencies_test.go` (new), `internal/install/commands.go`, `internal/install/host.go`.

- [x] Add a small normalized version/state model:
  - `ComponentState` with component name, installed flag, normalized local/latest versions, `NeedsUpdate`, `Changed`, and `ConfigurationChanged`.
  - `NormalizeVersion` accepting `v` prefixes and extracting a semantic version from command/npm output using the existing validation rules.
  - A report constructor that never marks a component latest when the upstream version is unknown.
- [x] Add failing unit tests for `v0.12.6`, plain `0.12.6`, command output such as `uv 0.12.6`, malformed versions, missing local versions, and unknown upstream versions.
- [x] Add a reusable npm discovery/ensure seam around `npm view <package> version` and exact `npm install --global <package>@<resolved-version>`:
  - Check the local command when present.
  - Always query the registry when npm is available.
  - Return an actionable error when the registry query fails; never silently treat the local version as latest.
  - Install only when missing or normalized versions differ.
  - Re-check the command version after mutation.
  - Retain the current normal-install then sudo fallback.
- [x] Add failing tests proving equal versions skip install, stale versions install the exact resolved version, missing commands install, registry failures fail, and post-install validation remains required.
- [x] Implement the primitives and run:
  - `go test ./internal/install -run 'Test(Normalize|NPM|ComponentState)' -count=1`

## 2. Make Baron Release Checks Cheap and Observable

**Files:** `internal/release/release.go`, `internal/release/release_test.go`, `internal/app/app.go`, `internal/app/bootstrap.go`.

- [x] Extend the existing latest-release fixture with request counters and add a failing test that an equal current Baron version calls only `/releases/latest`, not the manifest, checksum, or binary endpoints.
- [x] Preserve the current release order and security checks for outdated versions, including exact tag normalization, manifest/artifact matching, checksum verification, candidate `--version` validation, atomic replacement, and rollback.
- [x] Change bootstrap to call `installBaronBinary(false, reporter)` instead of forcing a binary download on every install.
- [x] Add progress assertions for a latest no-op and an outdated update, while ensuring no sensitive child-process output is emitted.
- [x] Run focused release/app tests and verify the old forced-download regression is gone:
  - `go test ./internal/release ./internal/app -run 'Test(InstallLatest|ReleaseHTTPClient|Bootstrap)' -count=1`

## 3. Apply Exact npm Latest Checks to DSH, Codex, and pnpm

**Files:** `internal/install/commands.go`, `internal/install/commands_test.go`, `internal/install/install_test.go`, `internal/install/host.go`, `internal/install/host_test.go`, `internal/install/latest_test.go`.

- [x] Add report-returning latest paths for DSH and Codex that use the shared npm ensure seam, while keeping `InstallDSHWithVersion` and `InstallCodexWithVersion` as compatibility wrappers for explicit versions.
- [x] Update DSH tests to cover:
  - Existing DSH equal to `npm view @deepseek-ai/dsh version` skips `npm install`.
  - Existing DSH behind the registry version installs the exact resolved version, including sudo fallback.
  - Missing DSH and registry errors have distinct behavior and useful errors.
- [x] Update Codex tests to cover equal, stale, missing, sudo fallback, and registry failure cases. Remove the old expectation that an existing Codex binary is always reinstalled with `@latest`.
- [x] Refactor pnpm setup to run local `pnpm --version` plus `npm view pnpm version`, install the exact resolved version only when required, and report its state.
- [x] Add progress hooks around each npm latest check and mutation; do not print npm command output.
- [x] Run:
  - `go test ./internal/install -run 'Test(InstallDSH|InstallCodex|EnsurePnpm|NPM)' -count=1`

## 4. Track DSH Plugin, Adapter, Profile, and Probe Changes

**Files:** `internal/install/commands.go`, `internal/install/install.go`, `internal/install/assets.go`, `internal/install/commands_test.go`, `internal/install/install_test.go`, `internal/app/app.go`, `internal/app/app_test.go`.

- [x] Add a report-returning DSH plugin initializer that dumps web/headless profiles before mutation and adds only missing or stale Baron-managed entries. Keep the required plugin set and deep post-mutation verification.
- [x] Preserve plugin sources and profile names; use the profile dump as the authoritative installed-state seam and query npm versions only for managed npm plugins where the current command/source supports it.
- [x] Add change-aware wrappers for embedded adapter materialization and profile patch creation. Identical existing bytes are not a change; missing or different bytes are a change.
- [x] Have `DSHInit` aggregate binary, plugin, adapter, profile, and config changes into a report. Keep credential resolution, receipts, config persistence, and `VerifyDSHProfile` intact.
- [x] Run `ProbeDSHStartup` on first install or when any DSH-managed state changed, and skip it when all relevant state is unchanged. Preserve the existing bounded timeout and immediate-error behavior.
- [x] Add tests for unchanged profiles, missing plugin entries, adapter repair, patch repair, probe-on-change, probe-skip-on-no-change, and unchanged receipt/config behavior.
- [x] Run:
  - `go test ./internal/install ./internal/app -run 'Test(DSH|Install|Bootstrap|App)' -count=1`

## 5. Make uv/uvx and Node Version-Aware

**Files:** `internal/install/host.go`, `internal/install/host_test.go`, `internal/install/latest_test.go`.

- [x] Add `HTTPClient *http.Client` to `HostToolchainOptions` and thread it through the default downloader without breaking custom `FileDownloader` fixtures.
- [x] Refactor `httpFileDownloader` to reuse the injected client/transport, retain TLS minimum 1.2, bounded timeouts, response cleanup, and progress reporting.
- [x] Keep the live uv release-tag query, parse `uv --version`, and skip archive/checksum/extraction/atomic replacement when both `uv` and `uvx` match the normalized latest tag. Reuse the existing retry and checksum flow for stale/missing installs.
- [x] Add uv tests proving latest metadata is queried on every run, equal versions make zero archive/checksum requests, stale versions install the exact release, checksum mismatch still fails, and `uvx` absence forces repair.
- [x] Refactor Node discovery to resolve the current supported latest channel/release from the existing Node index, compare the local `node --version`, and skip repository/key/apt/node work when the selected channel is already current. Keep npm/npx repair if either command is missing.
- [x] Add Node tests for current, stale, missing, unsupported, malformed-index, and metadata-download failure paths. Ensure no repeated apt work on the current path.
- [x] Run:
  - `go test ./internal/install -run 'Test(EnsureHostToolchain|UV|Node|Latest)' -count=1`

## 6. Plan Docker and apt Work Without Redundant Mutation

**Files:** `internal/install/apt.go` (new), `internal/install/linux.go`, `internal/install/linux_test.go`, `internal/install/host.go`, `internal/install/host_test.go`.

- [x] Add a small per-bootstrap `AptSession` carrying the injected runner and a refreshed flag. Its refresh operation runs `apt-get update` at most once for the planning/mutation cycle; standalone `EnsureDocker` and `EnsureHostToolchain` calls create their own session for compatibility.
- [x] Add package-state discovery using `dpkg-query` and apt candidate metadata for the managed Docker packages. Keep discovery read-only and distinguish unavailable package metadata from a confirmed current package.
- [x] Change `EnsureDocker` so `Refresh=true` means check health/latest state, not unconditional reinstall:
  - Healthy Docker with all installed versions equal to candidates skips repository writes, apt refresh, package installation, and daemon mutation.
  - Missing/outdated packages prepare the repository, refresh through the session, install only the required package set, and retain daemon start/repair behavior.
  - Unhealthy Docker still performs the existing repair/start flow after package state is handled.
- [x] Pass one `AptSession` from `internal/app/bootstrap.go` through host and Docker preflight so Node/Docker operations do not independently refresh apt metadata when both can share the cycle. Keep apt/dpkg mutations strictly sequential.
- [x] Add tests for healthy/current Docker no-op, healthy/stale Docker update, missing Docker install, stopped-daemon repair, apt refresh de-duplication, cleanup, and command failure propagation.
- [x] Run:
  - `go test ./internal/install -run 'Test(EnsureDocker|Apt|HostToolchain)' -count=1`

## 7. Build the Bootstrap Discovery/Mutation Coordinator

**Files:** `internal/app/bootstrap_plan.go` (new), `internal/app/bootstrap.go`, `internal/app/app.go`, `internal/app/app_test.go`, `internal/install/progress.go`.

- [x] Add a `BootstrapPlan` containing component states for the dependency discovery used by bootstrap, including local/latest versions, update flags, and configuration-change flags.
- [x] Add injectable discovery functions that use the existing app command runner. Run independent read-only checks with a bounded semaphore of six and a cancellation path; collect results deterministically by component name.
- [x] Keep mutation order deterministic and explicit: host prerequisites, Baron replacement if needed, DSH, Codex, Tencent restore, and project setup. Do not parallelize apt/dpkg, npm installs, Docker operations, binary replacement, config writes, or Tencent/project mutations.
- [x] Avoid duplicate DSH/Codex latest queries by allowing their ensure functions to consume discovery reports. Unknown npm discovery fails before mutation rather than claiming current.
- [x] Add a lightweight opt-in phase timer for discovery, component steps, validation, and total duration. Emit timing lines only when `BARON_INSTALL_TIMINGS=1`; keep normal output concise and secret-free.
- [x] Update progress/coordinator tests for bounded discovery, deterministic failure, and timing output.
- [x] Run:
  - `go test ./internal/app -run 'Test(Bootstrap|Discovery|Progress|Timing)' -count=1`

## 8. Documentation and Regression Coverage

**Files:** `README.md`, `internal/app/bootstrap_benchmark_test.go` (new), `internal/install/benchmark_test.go` (new), relevant existing test files.

- [x] Document that `baron install` checks upstream on every run, skips verified-equal components, shows discovery/mutation progress, and reports actionable upstream failures.
- [x] Add deterministic benchmarks for version/state and bounded discovery planning using fake inputs. Do not benchmark real apt, npm, GitHub, or Docker services.
- [x] Add regression tests proving adapters, receipts, and user-owned configuration remain compatible after a no-op and after an update. Existing Tencent/project behavior remains untouched.
- [x] Run formatting and the complete suite:
  - `gofmt -l` over the changed Go files
  - `go test ./... -count=1`
  - `go vet ./...`
- [x] Inspect `git diff --check`, confirm the untracked Rust idea file is untouched, and review the final diff for secret leakage and accidental destructive commands.

## 9. Final Verification and Handoff

- [x] Run focused tests again after the final formatting pass so the reported results are fresh.
- [x] Capture actual local benchmark output for repeat-all-latest and one-component-update scenarios; report timings without claiming a speedup unless the measurement supports it.
- [x] Confirm `baron update` also uses the cheap Baron latest check and that explicit version APIs still install exact requested versions.
- [x] Summarize changed files, verification commands/results, any environment-limited checks (real apt/npm/Docker), and the exact next command for the user to try.
