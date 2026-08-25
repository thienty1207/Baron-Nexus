# Baron Nexus 0.1.1 One-Command Bootstrap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Release Baron Nexus `0.1.1` with an idempotent `baron install` bootstrap that performs Linux sudo/Docker/DSH/Codex/Tencent/project setup in one flow while asking for credentials only when they are genuinely missing.

**Architecture:** Keep the verified release client and first-download shell installer as the binary distribution layer. Add a small application bootstrap coordinator behind the existing `install` command; it runs host preflight first, then the existing DSH/Codex/Tencent initializers, then current-project setup and a final diagnostic summary. Existing credential stores remain authoritative, so reruns reuse DSH/Tencent credentials and Codex authentication instead of prompting again.

**Tech Stack:** Go 1.27, Cobra, native `os/exec`, SQLite/WAL project state, existing DSH/Codex/Tencent adapters, POSIX shell release installer, GitHub release artifacts.

**Spec:** `docs/specs/baron-shared-brain-design.md` plus the user-approved one-time credential/bootstrap requirements in this task.

## Global Constraints

- Version output and release artifacts must be exactly `baron 0.1.1`.
- Keep public commands `baron install`, `baron update`, and `baron --version`.
- Linux automatic Docker bootstrap is limited to supported Ubuntu/Debian hosts.
- Sudo must be preflighted before Docker/Tencent network or package downloads; Baron must never receive or persist the sudo password.
- DeepSeek/Tencent secrets must use hidden prompts and official/managed credential stores; readiness checks remain read-only and never prompt.
- Codex ChatGPT login remains Codex-owned; Baron reports the first-login action and reuses Codex global auth on later projects.
- Rerunning install/init/setup must preserve user-owned DSH/Codex config, project IDs, credentials, checkpoints, and Tencent mappings.
- Windows must report Docker Desktop/WSL2/Ubuntu/Tencent prerequisites rather than claim silent UI automation.
- Do not stage or publish user-local `.baron/` state.

---

### Task 1: Lock the 0.1.1 version and release contract

**Files:**
- Modify: `internal/version/version.go`
- Modify: `internal/cli/cli_test.go`
- Modify: `scripts/build-release.sh`
- Modify: `README.md`
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: existing `version.Value` and release build `BARON_VERSION` override.
- Produces: default source-built version `0.1.1`, exact CLI output `baron 0.1.1`, and release metadata defaults that accept `0.1.1`.

- [X] **Step 1: Write the failing version assertion**

Add a default-version assertion that reads `internal/version.Value` through the CLI package and expects `0.1.1`, then run:

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/cli -run TestVersionFlagUsesBaronFormat -count=1
```

Expected: FAIL because the source default is still `0.1.0`.

- [X] **Step 2: Implement the 0.1.1 defaults**

Set `internal/version.Value` to `0.1.1`, set `BARON_VERSION` in `scripts/build-release.sh` to `0.1.1`, and update the README first-release/version wording to `0.1.1` without changing command names.

- [X] **Step 3: Run the focused version test**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/cli -run TestVersionFlagUsesBaronFormat -count=1
/home/ty/.local/bin/baron --version
```

Expected: source test passes; the installed binary is rebuilt in Task 6 before its output is used as final evidence.

- [X] **Step 4: Commit the version contract**

```bash
git add internal/version/version.go internal/cli/cli_test.go scripts/build-release.sh README.md
git commit -m "release: bump Baron Nexus to 0.1.1"
```

### Task 2: Make first-download installation perform a sudo preflight

**Files:**
- Modify: `install.sh`
- Create: `scripts/check-install-preflight_test.sh`
- Test: `scripts/check-install-preflight_test.sh`

**Interfaces:**
- Consumes: existing `EnsureDocker`, `InteractiveCommandRunner`, and `sudo -v` policy.
- Produces: interactive sudo authorization before the shell installer downloads release metadata/assets; no password is handled by Baron.

- [X] **Step 1: Write the failing installer-contract test**

Add a static installer-contract test that asserts the Linux-only `sudo -v` preflight appears before the first release `curl` and that the installer does not contain a password variable or echo operation:

```bash
preflight=$(rg -n 'sudo -v' install.sh | cut -d: -f1 | head -n1)
download=$(rg -n 'curl --fail' install.sh | cut -d: -f1 | head -n1)
test -n "$preflight" && test -n "$download" && test "$preflight" -lt "$download"
! rg -n 'SUDO_PASSWORD|sudo_password|printf.*PASSWORD|echo.*PASSWORD' install.sh
```

- [X] **Step 2: Run the focused test and confirm RED**

```bash
./scripts/check-install-preflight_test.sh
```

Expected: FAIL because the current shell installer has no `sudo -v` preflight.

- [X] **Step 3: Implement the minimum sudo-first shell path**

Keep the existing native `doctor.OSProbe.RunInteractive` path for `baron install`, and update `install.sh` to run a Linux-only `sudo -v` preflight before its first `curl`. The shell path must fail before network activity when sudo is missing and must never print the password.

- [X] **Step 4: Run Docker/bootstrap and shell checks**

```bash
./scripts/check-install-preflight_test.sh
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/install -run 'TestEnsureDocker(RequiresSudoBeforeNetwork|InstallsOfficialPackagesAfterSudoPreflight|WindowsReturnsManualGuidanceBeforeHostInspection)$' -count=1
sh -n install.sh scripts/build-release.sh
```

Expected: all focused tests and shell syntax checks pass.

- [X] **Step 5: Commit the sudo-first installer behavior**

```bash
git add install.sh scripts/check-install-preflight_test.sh
git commit -m "feat: preflight sudo before first download"
```

### Task 3: Add the idempotent bootstrap coordinator behind `baron install`

**Files:**
- Create: `internal/app/bootstrap.go`
- Modify: `internal/app/app.go`
- Modify: `internal/cli/cli.go`
- Modify: `internal/cli/cli_test.go`
- Test: `internal/app/bootstrap_test.go`

**Interfaces:**
- Consumes: `App.installBaronBinary`, `App.DSHInit`, `App.CodexInit`, `App.TencentInit`, `App.SetupProject`, `install.EnsureDocker`, and existing release handler wiring.
- Produces: `func (a *App) installAndBootstrap(context.Context) (string, error)` and a deterministic `runBootstrap(context.Context, BootstrapSteps) error` sequence.

- [X] **Step 1: Write the failing coordinator test**

Create `BootstrapSteps` test seams and assert the exact order `host-preflight → dsh → codex → tencent → project-setup`; one coordinator invocation is enough because each delegated initializer already owns its persisted/idempotent state:

```go
func TestRunBootstrapExecutesOneTimeSetupInOrder(t *testing.T) {
	var got []string
	steps := BootstrapSteps{
		Preflight: func(context.Context) error { got = append(got, "preflight"); return nil },
		DSH:       func() error { got = append(got, "dsh"); return nil },
		Codex:     func() error { got = append(got, "codex"); return nil },
		Tencent:   func(context.Context) error { got = append(got, "tencent"); return nil },
		Setup:     func(context.Context) error { got = append(got, "setup"); return nil },
	}
	if err := runBootstrap(context.Background(), steps); err != nil { t.Fatal(err) }
	want := []string{"preflight", "dsh", "codex", "tencent", "setup"}
	if !reflect.DeepEqual(got, want) { t.Fatalf("steps=%v want=%v", got, want) }
}
```

- [X] **Step 2: Run the coordinator test and confirm RED**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/app -run TestRunBootstrapExecutesOneTimeSetupInOrder -count=1
```

Expected: FAIL because `BootstrapSteps`, `runBootstrap`, and the install coordinator do not yet exist.

- [X] **Step 3: Implement the coordinator**

Add the small `BootstrapSteps` struct and `runBootstrap` helper. Wire production steps as follows:

```go
func (a *App) installAndBootstrap(ctx context.Context) (string, error) {
	releaseMessage, err := a.installBaronBinary(true)
	if err != nil { return "", err }
	if err := runBootstrap(ctx, BootstrapSteps{
		Preflight: a.preflightBootstrap,
		DSH: a.DSHInit,
		Codex: a.CodexInit,
		Tencent: a.TencentInit,
		Setup: func(ctx context.Context) error { _, err := a.SetupProject(ctx, ""); return err },
	}); err != nil { return "", err }
	return releaseMessage + " Bootstrap complete.", nil
}
```

`preflightBootstrap` must call `install.EnsureDocker` on Linux before DSH/Tencent downloads, reuse the existing Windows prerequisite error, and never store sudo credentials. A healthy stack may return without a second deployment, but the first install path must still run the host preflight before other downloads.

- [X] **Step 4: Wire only `install` to the coordinator**

Keep `baron update` binary-only and change `CLIOptions.Install` to call `installAndBootstrap`; preserve the existing release client’s no-mutation guarantees for `update`.

- [X] **Step 5: Run focused app/CLI tests**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/app ./internal/cli -run 'TestRunBootstrapExecutesOneTimeSetupInOrder|TestInstallAndUpdateCommandsInvokeDedicatedHandlers|TestInstallAndUpdatePropagateHandlerExitErrors' -count=1
```

Expected: PASS, with update still using the verified binary handler and install invoking the bootstrap handler.

- [X] **Step 6: Commit the bootstrap coordinator**

```bash
git add internal/app/bootstrap.go internal/app/bootstrap_test.go internal/app/app.go internal/cli/cli.go internal/cli/cli_test.go
git commit -m "feat: make baron install bootstrap the runtime"
```

### Task 4: Lock one-time credential and Codex-auth behavior

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/cli/cli.go`
- Modify: `README.md`
- Test: `internal/app/app_test.go`

**Interfaces:**
- Consumes: `resolveDSHCredential`, `resolveTencentRuntimeConfig`, `resolveTencentAdminKey`, `ReadDSHProviderKey`, `codexAuthReady`, and dynamic `InitNoticeFunc`.
- Produces: hidden prompts only on missing DSH/Tencent values; explicit one-time Codex login notice; no prompt from `baron test`.

- [X] **Step 1: Write failing one-time credential tests**

Add a CLI test that `codex-cli init` prints an actionable one-time login notice without exposing credentials. The existing `resolveDSHCredential`, Tencent provider, and Tencent admin tests already prove persisted values are reused without reprompting.

- [X] **Step 2: Run focused tests and confirm RED**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/app ./internal/cli -run 'TestCodexInitPrintsOneTimeLoginNotice' -count=1
```

Expected: FAIL because the new named tests and notice wiring are absent.

- [X] **Step 3: Implement the minimum behavior**

Reuse the official DSH credential file and managed Tencent `.admin-key`/runtime values already implemented. Add a dynamic `InitNoticeFunc` entry for Codex stating that `codex` login is required only when auth is absent; do not invoke or capture the login flow, and do not make readiness checks interactive. Preserve the existing one-time DSH/Tencent credential tests as the storage contract.

- [X] **Step 4: Run credential safety tests**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test ./internal/app ./internal/cli ./internal/credentials ./internal/install -run 'Credential|Prompt|CodexInit|TestReadiness' -count=1
```

Expected: PASS with no secret in captured prompt/output buffers.

- [X] **Step 5: Commit credential-flow wiring**

```bash
git add internal/app/app.go internal/app/app_test.go internal/cli/cli.go README.md
git commit -m "feat: make credential prompts one-time and explicit"
```

### Task 5: Update user documentation and acceptance records for 0.1.1

**Files:**
- Modify: `README.md`
- Modify: `docs/specs/baron-shared-brain-design.md`
- Modify: `docs/implementation/IMPLEMENT_PROGRESS.md`
- Modify: `docs/implementation/FINAL_ACCEPTANCE_REPORT.md`
- Modify: `Baron-Nexus Implement Roadmap.md`

**Interfaces:**
- Consumes: implemented command behavior and test evidence from Tasks 1–4.
- Produces: a truthful quick-start showing first download, `baron install` bootstrap, one-time prompts, rerun behavior, and remaining external gates.

- [X] **Step 1: Replace the multi-command quick start**

Document the supported Linux user path as:

```bash
git clone https://github.com/thienty1207/Baron-Nexus.git
cd Baron-Nexus
./install.sh
cd /path/to/project
baron install
```

Explain that `install.sh` performs the initial verified binary download and sudo preflight, while `baron install` performs the idempotent runtime/project bootstrap. Document that DSH/Tencent secrets are asked once and Codex login is global/reused; mention reauthentication only after logout/expiration.

- [X] **Step 2: Update release metadata wording**

Change current candidate references to `0.1.1` and explicitly leave public GitHub publication, Windows runtime, clean-machine restart/volume, and cgo race gates unchecked until evidence exists.

- [X] **Step 3: Run documentation/static checks**

```bash
sh -n install.sh scripts/build-release.sh
./scripts/check-branding.sh
./scripts/check-platform-guidance.sh
git diff --check
```

Expected: all checks pass and no document claims silent Windows automation or 100% external acceptance.

- [X] **Step 4: Commit documentation**

```bash
git add README.md docs/specs/baron-shared-brain-design.md docs/implementation/IMPLEMENT_PROGRESS.md docs/implementation/FINAL_ACCEPTANCE_REPORT.md 'Baron-Nexus Implement Roadmap.md'
git commit -m "docs: publish Baron Nexus 0.1.1 bootstrap flow"
```

### Task 6: Build, install, and run the real 0.1.1 acceptance flow

**Files:**
- Modify: `/tmp/baron-release-0.1.1.final` artifacts only; do not add them to Git.
- Verify: `/home/ty/project-test`, `/home/ty/.local/bin/baron`

**Interfaces:**
- Consumes: the committed 0.1.1 source and the published local bootstrap coordinator.
- Produces: fresh release hashes, local tag `v0.1.1`, and evidence separating user-level PASS from unavailable external Docker/sudo/GitHub/Windows gates.

- [ ] **Step 1: Run the complete local verification suite**

```bash
GOTOOLCHAIN=local /usr/local/go/bin/go test -count=1 ./...
GOTOOLCHAIN=local /usr/local/go/bin/go vet ./...
GOTOOLCHAIN=local CGO_ENABLED=0 /usr/local/go/bin/go test -count=1 ./...
gofmt -l .
git diff --check
```

Expected: exit 0, no formatting output, and no diff-check output.

- [ ] **Step 2: Build and install the 0.1.1 Linux/Windows artifacts**

```bash
BARON_VERSION=0.1.1 BARON_RELEASE_DIR=/tmp/baron-release-0.1.1.final ./scripts/build-release.sh
release_binary=/tmp/baron-release-0.1.1.final/baron-linux-amd64
release_sha=$(sha256sum "$release_binary" | awk '{print $1}')
BARON_BINARY_SOURCE="$release_binary" BARON_BINARY_SHA256="$release_sha" BARON_INSTALL_PATH=/home/ty/.local/bin/baron BARON_ALLOW_REPLACE=1 ./install.sh
(cd /tmp/baron-release-0.1.1.final && sha256sum -c SHA256SUMS)
/home/ty/.local/bin/baron --version
```

Expected: `baron 0.1.1` and all checksum entries report `OK`.

- [ ] **Step 3: Run the real target-project bootstrap**

From `/home/ty/project-test`, run `baron install` in a terminal with the normal PATH and credential stores. Record whether the release endpoint is available; if GitHub is not published yet, run the coordinator through its local test seam and report the release 404 honestly rather than bypassing it.

- [ ] **Step 4: Verify one-time rerun behavior**

Run the initialized DSH/Codex/Tencent/project flow a second time with a prompt reader that would fail if called. Confirm project ID, Tencent identity, Codex hooks, DSH credential path, queue counts, and Wiki status are unchanged except for fresh timestamps.

- [ ] **Step 5: Run the release gate and record blockers**

```bash
./scripts/check-release-gate.sh
git tag -a -f v0.1.1 -m "Baron Nexus v0.1.1" HEAD
```

Expected: local artifacts and code gates pass; the release gate remains `BLOCKED` until public GitHub authentication, clean-machine/Windows, cgo race, and other external evidence is actually available.
