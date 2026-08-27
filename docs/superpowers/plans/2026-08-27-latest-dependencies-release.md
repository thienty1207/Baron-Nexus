# Latest-at-Run Dependencies and 0.1.4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Baron Nexus resolve every externally managed dependency to the newest compatible upstream release or revision at each explicit bootstrap/initialization run, then publish the result as `v0.1.4`.

**Architecture:** Keep dependency resolution at the existing installer boundaries. Each mutable latest source is resolved once per operation: npm packages use `@latest`, Node uses the newest official Node release major available through NodeSource, uv uses one latest release tag for both archive and checksum, and Tencent resolves the upstream default HEAD to an immutable commit before checkout. Existing local state and credentials remain protected; resolved versions, commits, and image digests are recorded as receipts/manifests.

**Tech Stack:** Go 1.27, Cobra, Go standard-library HTTP/JSON/tar/gzip/checksum code, POSIX `sh`, PowerShell, npm/pnpm/uvx, GitHub Releases, NodeSource apt repository, Docker Compose, Go test/vet/race.

**Spec:** `docs/implementation/RELEASE_0.1.4.md`

## Global Constraints

- All external package/repository/image selectors used by the automatic bootstrap are latest-at-run; no old DSH, Codex, plugin, Reverse Skill, Node, or Tencent revision is silently selected as fallback.
- A mutable latest source is resolved once per operation, then integrity-checked and used consistently for that operation.
- Existing project identity, checkpoints, user-owned agent settings, credentials, Tencent `.env`, and Docker volumes are preserved.
- Failed integrity, compatibility, or startup verification never overwrites a working installed dependency or managed state.
- Secrets never appear in logs, receipts, diagnostics, manifests, or error output.
- Ubuntu/Debian Linux remains the only automatic host bootstrap target; Windows keeps its documented Docker Desktop/WSL boundary.
- The release version is `0.1.4`; `v0.1.3` remains an immutable historical release.

---

### Task 1: Make uv latest resolution consistent and retryable

**Files:**
- Modify: `internal/install/host.go`
- Test: `internal/install/host_test.go`

**Interfaces:**
- `ensureUV` continues to accept `FileDownloader` and returns `(bool, error)`.
- Add a small resolver that derives a single tag-scoped archive/checksum URL pair from the latest uv release metadata.

- [ ] **Step 1: Write the failing tests**
  - Add a fixture proving a checksum mismatch is retried with the same release pair and succeeds on the second attempt.
  - Add a fixture proving a mismatch error includes expected and actual SHA-256 values but never archive bytes.
  - Add a URL assertion proving archive and checksum use the same resolved release tag rather than two independent `latest/download` requests.

- [ ] **Step 2: Run the focused tests and verify RED**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/install -run 'TestEnsureHostToolchain(UV|Uv)' -count=1
```

Expected result: the new tests fail because the current implementation performs no retry and downloads both files through mutable `latest/download` URLs.

- [ ] **Step 3: Implement the minimal resolver and retry loop**
  - Resolve the latest uv tag from the official GitHub release endpoint.
  - Download the archive and checksum from `/releases/download/<resolved-tag>/...`.
  - Verify the exact digest before extracting; retry the complete pair once on mismatch.
  - Keep installation atomic by writing `uv` and `uvx` only after archive parsing and checksum verification pass.

- [ ] **Step 4: Run focused tests and verify GREEN**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/install -run 'TestEnsureHostToolchain(UV|Uv)' -count=1
```

- [ ] **Step 5: Commit the isolated uv fix**

```bash
git add internal/install/host.go internal/install/host_test.go
git commit -m "fix: resolve uv latest release consistently"
```

### Task 2: Resolve and refresh latest host dependencies on Ubuntu/Debian

**Files:**
- Modify: `internal/install/host.go`
- Modify: `install.sh`
- Test: `internal/install/host_test.go`
- Test: `scripts/check-install-preflight_test.sh`

**Interfaces:**
- `EnsureHostToolchain` continues to perform sudo preflight before network/package work.
- NodeSource repository generation receives the latest Node major resolved from official Node release metadata.

- [ ] **Step 1: Write failing tests**
  - Assert an existing supported but stale Node installation triggers latest-channel refresh.
  - Assert pnpm is refreshed through `npm install --global pnpm@latest` rather than skipped solely because it exists.
  - Assert a latest Node major is used in the apt source definition and no `node_22.x` constant remains in the automatic path.

- [ ] **Step 2: Run focused tests and verify RED**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/install -run 'TestEnsureHostToolchain' -count=1
sh scripts/check-install-preflight_test.sh
```

- [ ] **Step 3: Implement latest host resolution**
  - Resolve the first stable official Node release from `https://nodejs.org/dist/index.json` and use its major for NodeSource.
  - Refresh Node/npm/npx and pnpm when Baron performs host bootstrap, while retaining the existing supported-version validation.
  - Keep uv/uvx on the consistent latest resolver from Task 1.
  - Mirror the Node latest-major and uv latest-pair behavior in `install.sh`.

- [ ] **Step 4: Run focused tests and verify GREEN**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/install -run 'TestEnsureHostToolchain' -count=1
sh scripts/check-install-preflight_test.sh
```

- [ ] **Step 5: Commit host bootstrap changes**

```bash
git add internal/install/host.go internal/install/host_test.go install.sh scripts/check-install-preflight_test.sh
git commit -m "feat: refresh latest Ubuntu dependency toolchain"
```

### Task 3: Install latest DSH, plugins, and Codex CLI with real receipts

**Files:**
- Modify: `internal/install/commands.go`
- Modify: `internal/install/install.go`
- Modify: `internal/app/app.go`
- Test: `internal/install/commands_test.go`
- Test: `internal/install/install_test.go`
- Test: `internal/app/app_test.go`

**Interfaces:**
- DSH and Codex installers retain compatibility wrappers while their default production selectors become `latest`.
- Receipts record the version reported by the freshly installed command, not a stale hard-coded version.
- DSH plugin specs become `superpowers-dsh@latest`, the unpinned Reverse Skill repository, and `@deepseek-ai/dsh-mcp-client@latest`.

- [ ] **Step 1: Write failing tests**
  - Assert DSH installation invokes `@deepseek-ai/dsh@latest` and records the reported version.
  - Assert each DSH profile receives latest plugin selectors and preserves user-owned entries.
  - Assert Codex installation invokes `@openai/codex@latest` even when an older Codex binary is already present, then verifies the actual reported version.

- [ ] **Step 2: Run focused tests and verify RED**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/install ./internal/app -run '(Install(DSH|Codex)|DSHPlugins|CodexInit)' -count=1
```

- [ ] **Step 3: Implement latest package and plugin installation**
  - Remove production dependence on pinned upstream package versions.
  - Parse the installed command version only for evidence and receipts; do not require a preselected old version.
  - Keep embedded Baron adapters versioned by the Baron release itself.

- [ ] **Step 4: Run focused tests and verify GREEN**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/install ./internal/app -run '(Install(DSH|Codex)|DSHPlugins|CodexInit)' -count=1
```

- [ ] **Step 5: Commit agent dependency changes**

```bash
git add internal/install/commands.go internal/install/install.go internal/app/app.go internal/install/commands_test.go internal/install/install_test.go internal/app/app_test.go
git commit -m "feat: install latest DSH plugins and Codex"
```

### Task 4: Resolve latest Tencent source while preserving immutable rollback

**Files:**
- Modify: `internal/install/tencent.go`
- Modify: `internal/install/tencent_manifest.go`
- Modify: `internal/install/commands_test.go`
- Modify: `internal/app/app.go`

**Interfaces:**
- Empty `TencentDeploymentOptions.Ref` resolves the official repository default HEAD to a 40-character commit before checkout.
- Explicit immutable refs remain supported for rollback and controlled tests.
- `deployment-manifest.json` records the resolved commit and container image digests, with no secrets.

- [ ] **Step 1: Write failing tests**
  - Assert a default Tencent deployment calls `git ls-remote <repository> HEAD`, checks out the returned commit, and records it.
  - Assert a moving branch ref is still rejected when explicitly supplied.
  - Assert rollback continues to use the previous immutable manifest commit rather than resolving latest.

- [ ] **Step 2: Run focused tests and verify RED**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/install ./internal/app -run '(TencentDeployment|TencentInit|Rollback)' -count=1
```

- [ ] **Step 3: Implement latest Tencent resolution**
  - Resolve default HEAD once, fetch/check out that immutable commit, run the official verification/start scripts with `PULL=1`, and retain existing `.env`/volume protections.
  - Preserve the current health-first behavior for already healthy services; an explicit Tencent initialization/repair uses the latest resolved source and image pull path.

- [ ] **Step 4: Run focused tests and verify GREEN**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/install ./internal/app -run '(TencentDeployment|TencentInit|Rollback)' -count=1
```

- [ ] **Step 5: Commit Tencent resolution changes**

```bash
git add internal/install/tencent.go internal/install/tencent_manifest.go internal/install/commands_test.go internal/app/app.go
git commit -m "feat: resolve latest Tencent deployment revision"
```

### Task 5: Version, documentation, release artifacts, and publication

**Files:**
- Modify: `internal/version/version.go`
- Modify: `internal/version/version_test.go`
- Modify: `internal/cli/cli_test.go`
- Modify: `scripts/build-release.sh`
- Modify: `install.ps1`
- Modify: `README.md`
- Modify: `Baron-Nexus Implement Roadmap.md`
- Modify: `docs/implementation/IMPLEMENT_PROGRESS.md`
- Modify: `docs/implementation/FINAL_ACCEPTANCE_REPORT.md`
- Create: `docs/implementation/RELEASE_0.1.4.md`

- [ ] **Step 1: Update version and current-release documentation to `0.1.4`**
- [ ] **Step 2: Run the full local test, vet, formatting, shell, and release checks**
- [ ] **Step 3: Build Linux/Windows artifacts and verify every SHA-256 entry**
- [ ] **Step 4: Commit the release changes**
- [ ] **Step 5: Push `main`, create annotated tag `v0.1.4`, and push the tag**
- [ ] **Step 6: Wait for GitHub Actions, verify the public release has eight assets, download them, and run `sha256sum -c SHA256SUMS` plus Linux/Windows smoke checks**

## Verification Matrix

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test -count=1 ./...
GOTOOLCHAIN=local /usr/local/go/bin/go vet ./...
test -z "$(gofmt -l .)"
git diff --check
sh -n install.sh scripts/build-release.sh
sh scripts/check-install-preflight_test.sh
sh scripts/check-branding_test.sh
sh scripts/check-platform-guidance_test.sh
sh scripts/check-release-gate_test.sh
bash scripts/check-dsh-adapter_test.sh
```

The release is published only after local verification and public asset
checksum verification pass. External clean-machine, Windows runtime, and
provider outage gates remain reported honestly if the environment cannot
prove them.
