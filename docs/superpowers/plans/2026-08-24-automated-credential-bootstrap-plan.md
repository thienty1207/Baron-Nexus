# Automated credential bootstrap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (or superpowers:subagent-driven-development) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the existing Baron initializer commands prompt for missing provider credentials securely, configure DSH/Tencent automatically, and continue through Docker/service/identity validation without manual file editing.

**Architecture:** Add a small injectable credential-prompt boundary to the Go CLI, a preserve-first DSH credential-store writer for DSH's version-1 YAML format, and a managed Tencent runtime resolver that merges environment, existing deployment state, DSH reuse, defaults, and hidden prompts. Keep credentials outside project state, keep `baron test` read-only, and keep Linux sudo preflight before network work.

**Tech Stack:** Go 1.27, Cobra, `golang.org/x/term`, `gopkg.in/yaml.v3`, existing atomic-write/config/install packages, Go unit/fixture tests, Docker/DSH/Tencent live smoke where available.

**Spec:** `docs/superpowers/specs/2026-08-24-automated-credential-bootstrap-design.md`

## Global Constraints

- Preserve the existing `baron deepseek-harness init`, `baron codex-cli init`, `baron tencent-memory init`, `baron test`, and `baron setup` command surface.
- Never log, print, pass as command arguments, or persist secrets in project state, receipts, Git files, or diagnostics.
- DSH keys belong in `$DSH_HOME/.credentials.yaml`; Tencent provider keys belong only in the protected managed deployment `.env`.
- `baron test` is read-only and never prompts; non-interactive init fails before network/state mutation with exact remediation.
- Ubuntu/Debian must run sudo/OS preflight before downloads; Windows must keep its explicit manual Docker/WSL/Tencent boundary.
- Preserve existing user-owned files and data; rollback only newly created Baron-owned state.

---

### Task 1: Add the injectable hidden-prompt boundary

**Files:**
- Create: `internal/credentials/prompt.go`
- Test: `internal/credentials/prompt_test.go`
- Modify: `internal/app/app.go`, `internal/cli/cli.go`, `cmd/baron/main.go`
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Produce `credentials.Prompter{In io.Reader, Out io.Writer, ReadSecret func(io.Reader) ([]byte, error)}` with `Secret(label string) (string, error)` and `Value(label, defaultValue string) (string, error)`.
- Add injectable app input/output while preserving `CLIOptions(out, errOut)` compatibility through defaults to `os.Stdin` and the supplied output writer.

- [X] Write tests for hidden-secret input, newline trimming, default values, EOF/non-interactive errors, and output that never contains the secret.
- [X] Run the targeted red test before implementation, then make it green.
- [X] Implement terminal echo-disabled reading with `golang.org/x/term`, a deterministic injected reader for tests, and safe non-TTY errors.
- [X] Run the targeted tests and then `go test ./internal/credentials ./internal/cli ./internal/app -count=1`.

### Task 2: Implement DSH credential-store detection and merge

**Files:**
- Create: `internal/install/dsh_credentials.go`
- Test: `internal/install/dsh_credentials_test.go`
- Modify: `internal/install/install.go`, `internal/app/app.go`

**Interfaces:**
- Produce `ReadDSHProviderKey(env map[string]string) (string, error)` and `EnsureDSHProviderKey(env map[string]string, key string) error`.
- Use `DSH_HOME` or `~/.dsh`, file `.credentials.yaml`, version `1`, and reference `DEEPSEEK_API_KEY`.

- [X] Write tests for environment precedence, absent store, valid preserve-first merge of refs/records, mode `0600`, invalid-store fail-closed behavior, and secret-free errors.
- [X] Run the targeted red test before implementation, then make it green.
- [X] Implement YAML node/map merge, atomic write, owner-only permissions, and no-op behavior when an inherited environment key already exists.
- [X] Run the targeted tests and `go test ./internal/install -count=1`.

### Task 3: Add managed Tencent runtime loading and credential reuse

**Files:**
- Modify: `internal/install/tencent_env.go`
- Test: `internal/install/tencent_env_test.go`
- Create: `internal/install/provider_credentials.go`

**Interfaces:**
- Produce `LoadTencentRuntimeConfig(deployRoot string) (TencentRuntimeConfig, error)` and a merge function with explicit precedence: process environment, managed `.env`, reusable DSH key, DeepSeek defaults, prompt values.
- Keep `MissingProviderValues` stable and return only variable names, never values.

- [X] Write tests for `.env` reuse, environment override, DSH-key reuse, DeepSeek defaults, custom-provider override, missing-key classification, and redacted errors.
- [X] Run the targeted red test before implementation, then make it green.
- [X] Implement loading/merging without rewriting existing values and ensure `EnsureTencentRuntimeEnv` receives the resolved config before checkout/start.
- [X] Run `go test ./internal/install -run 'TencentRuntime|Provider' -count=1` and the full install package tests.

### Task 4: Wire prompt-before-network initializer orchestration

**Files:**
- Modify: `internal/app/app.go`, `internal/install/linux.go`, `internal/cli/cli.go`
- Test: `internal/app/app_test.go`, `internal/install/linux_test.go`, `internal/cli/cli_test.go`

**Interfaces:**
- Add `App.resolveDSHCredential()` and `App.resolveTencentRuntimeConfig()` helpers that use the injected prompter and return redacted classified errors.
- Ensure `TencentInit` performs OS/sudo preflight, resolves credentials without network, then installs Docker/deployment and provisions identity.

- [X] Write tests proving a missing credential prompts before checkout/package/network calls, supplied input reaches the managed writer, admin-key fallback remains process-only, and proxy permission repair can restart an exited container.
- [X] Run the orchestration red tests before implementation, then make them green.
- [X] Implement DSH init prompt/reuse/probe wiring and Tencent init prompt/reuse/defaults with up to three validation attempts, preserving existing rollback behavior.
- [X] Run targeted app/install/CLI tests and inspect all captured output for secret absence.

### Task 5: Add automatic repair/readiness guidance and documentation

**Files:**
- Modify: `internal/doctor/doctor.go`, `README.md`, `docs/implementation/IMPLEMENT_PROGRESS.md`
- Test: `internal/doctor/doctor_test.go`, `scripts/check-platform-guidance_test.sh`

- [X] Write tests for actionable missing-credential suggestions, non-interactive diagnostics, and unchanged `baron test` read-only behavior.
- [X] Implement messages that tell users to rerun the same initializer and never ask them to edit `.env` manually.
- [X] Update Linux/Windows quick-start and troubleshooting to describe the prompt flow and exact boundaries.
- [X] Run doctor, guidance, full Go tests, vet, formatting, and diff checks.

### Task 6: Run live onboarding and acceptance evidence

**Files:**
- Modify: `Baron-Nexus Implement Roadmap.md`, `docs/implementation/IMPLEMENT_PROGRESS.md`, `docs/implementation/FINAL_ACCEPTANCE_REPORT.md`
- Create: `docs/implementation/P27_AUTOMATED_ONBOARDING_EVIDENCE.md`

- [X] Build `/home/ty/.local/bin/baron`, run the real DSH init/probe, and run a disposable non-interactive missing-key check that proves no credential file is created before dependency/network work.
- [ ] Run Tencent init with the available provider credential, verify all service endpoints, identity reuse, restart policy, and repeated init behavior without printing secrets; the current agent terminal lacks a sudo ticket.
- [X] Run available real DSH/Codex/Tencent health and project-setup checks; record unavailable Windows/clean-machine and cross-agent gates explicitly.
- [X] Mark only freshly evidenced roadmap items `[X]`; leave impossible external gates unchecked and keep final status truthful.
- [X] Run the final local verification suite: `go test ./...`, `go vet ./...`, `CGO_ENABLED=0 go test ./...`, formatting, guidance, and `git diff --check`.
