# Baron Nexus Implement Roadmap

> **Management checklist:** `[X]` means the implementation and the stated local evidence exist. `[ ]` means the work is not complete, or its required live/external acceptance evidence has not been run. A phase gate is checked only when every mandatory test for that phase is genuinely green.

**Product:** Baron Nexus  
**Source contract:** `/home/ty/Baron dsh-codex-tencent/IMPLEMENT.pdf`  
**Predecessor concept:** Baron Engine  
**Roadmap created:** 2026-08-24  
**Command compatibility:** keep the user-facing command as `baron`; rename the product/display branding to Baron Nexus.

## Locked product objective

Baron Nexus is the cross-agent continuity and memory-control plane for DeepSeek
Harness and OpenAI Codex. It preserves the Baron Engine ideas of project
identity, memory, checkpoint, recovery, evidence, and safe continuation, while
using TencentDB Agent Memory as the long-term memory and knowledge plane.

The normal user workflow must remain short:

```bash
git clone https://github.com/thienty1207/Baron-Nexus.git
cd Baron-Nexus
./install.sh
cd /path/to/project
baron install
```

The individual `baron ... init` commands remain available for advanced repair;
`baron install` is the idempotent first-run coordinator.

After setup, DSH and Codex are used normally. Baron Nexus must automatically
handle local event capture, checkpointing, recovery, Tencent memory, Wiki,
CodeGraph, Skill Memory, project isolation, offline queueing, and cross-agent
handoff. The user must not need to open Tencent Panel or manually register a
project's memory assets.

## Platform policy

- Ubuntu/Debian Linux: `baron install` and `baron tencent-memory init` may
  install Docker Engine and Compose through the official package path, but they
  must check `sudo` before any network download. If sudo is unavailable or
  unauthorized, they must stop before creating partial state and print the
  exact repair instruction.
- Windows: Baron does not silently install Docker Desktop, WSL, or Ubuntu. It
  prints an actionable prerequisite guide and preserves the same command
  surface.
- Linux support starts with Ubuntu/Debian. Unsupported distributions receive a
  clear manual-install diagnostic rather than a guessed package-manager action.
- Docker/Tencent deployment must be idempotent, restart-safe, secret-safe, and
  recorded with the resolved compatible version, commit, or image digest.

## Tencent capability boundary

Nexus must integrate both Tencent planes rather than treating Tencent as only a
conversation endpoint:

1. MemoryCore: L0 conversations, L1 atomic memories, L2 scenarios, L3 core
   memory, Skill Memory, users, teams, agents, tasks, assets, and access
   metadata.
2. Knowledge Service: LLM-Wiki, CodeGraph indexing/sync/search, callers,
   callees, impact analysis, files, symbols, pages, graph queries, and tool
   discovery.
3. Baron Nexus control plane: project identity, lifecycle events, local
   checkpoint, recovery packet, evidence policy, bounded context compilation,
   offline queue, and DSH/Codex adapters.

Tencent is a knowledge/memory service, not the source of truth for the current
working tree. Git, source inspection, and test evidence always outrank stale
remote memory.

## Counts

- Original `IMPLEMENT.pdf`: P0-P19, 20 phases, 205 implementation tasks (including `P18-T07B`); the roadmap carries four later clarification tasks in those phases.
- Baron Nexus expansion: P20-P28, 9 phases, 73 additional implementation
  tasks.
- Full roadmap: **29 phases, 282 implementation tasks**, plus the mandatory
  phase-test and final-acceptance checklists below.

## Verification checkpoint — 2026-08-25

This checkpoint records fresh evidence from the current worktree. It does not
promote external acceptance items to `[X]`.

- Checklist accounting: **399/428 checked (93.22%)**; implementation tasks are
  **263/269 (97.77%)**, and mandatory phase tests are **128/151 (84.77%)**.
- Local code gates PASS: full Go tests, CGO-free tests, `go vet`, formatting,
  diff check, shell syntax, branding/platform/DSH/release-gate scripts, and
  Linux/Windows CGO-free release artifacts with verified SHA256SUMS.
- Host preflight remains BLOCKED: both requested non-interactive sudo checks
  returned `sudo: interactive authentication is required`; Docker CLI exists,
  but direct daemon access is denied by `/var/run/docker.sock` permissions.
- Fresh `/home/ty/.local/bin/baron test --json` and a rebuilt current-source
  artifact both return `ready:false`, exit `12`, only for Docker daemon and
  sudo; DSH/Codex/Tencent user-level checks remain ready.
- All four managed Tencent `/health` endpoints returned HTTP `200`. The current
  project Wiki has ten raw seed sources but Tencent still reports
  `status=processing` (local registry remains honestly `pending`), and no
  verified Git remote is available for CodeGraph.
- A real repair bug was fixed: when all Tencent health endpoints are already
  healthy, `baron repair` now skips Docker bootstrap/deployment and can flush
  Baron queues without a second sudo authorization. The regression test and a
  real rebuilt-binary `baron repair` both pass.
- External tasks remain unchecked for clean-machine bootstrap, service
  restart/volume/power-loss runs, Windows runtime, cgo race execution, and
  first-installer acceptance from a fresh sudo-authorized machine.

## Current baseline ledger

| Phase | Gate | Current truth |
| --- | --- | --- |
| P0 | [X] | Baseline, contracts, Go repository, acceptance contract, and local tests complete. |
| P1 | [X] | CLI, configuration, atomic writes, backups, redaction, and exit codes complete. |
| P2 | [X] | SQLite WAL, local event journal, checkpoints, hooks, concurrency, and local fault tests complete. |
| P3 | [X] | Readiness probes and secret-safe diagnostics complete. |
| P4 | [X] | DSH installer/profile/adapter, live startup/MCP probes, and automated hidden credential bootstrap through DSH's official store complete. |
| P5 | [X] | Codex merge/fail-open code, authenticated interactive CLI hook smoke, and the documented CLI-only Desktop boundary pass. |
| P6 | [ ] | Tencent v3 client, managed deployment, proxy permission recovery, and live health/project setup are verified; clean-machine/restart/volume acceptance remains blocked. |
| P7 | [X] | Project identity, setup, permissions, Git-ignore, move, and tamper tests complete locally. |
| P8 | [ ] | Project-agent/isolation implementation and live disposable Agent/Wiki setup exist; five-run namespace and asynchronous CodeGraph acceptance remains blocked. |
| P9 | [X] | Local checkpoint/recovery engine and interruption/drift tests complete. |
| P10 | [ ] | Memory/recall/queue implementation and HTTP fixtures exist; live Tencent acceptance is blocked. |
| P11 | [X] | Codex hook implementation/tests, authenticated interactive SessionStart/UserPromptSubmit readback, and the CLI-only Desktop boundary pass; paired-agent handoff is tracked separately by P12/P13. |
| P12 | [X] | DSH adapter/profile bundle and real DSH lifecycle acceptance pass; paired-agent handoff remains tracked by P13. |
| P13 | [X] | Recovery/handoff implementation and real two-client handoff acceptance pass, including verified-completion ordering. |
| P14 | [ ] | Queue/concurrency implementation exists; race/SIGKILL/live outage acceptance is blocked. |
| P15 | [ ] | Isolation and tamper implementation/tests exist; legacy/live Tencent snapshot acceptance is blocked. |
| P16 | [ ] | Local backup/checksum/rollback implementation exists; Docker-volume and cross-OS acceptance is blocked. |
| P17 | [X] | Local security, permission, redaction, tamper, bounded-output, and vet evidence complete. |
| P18 | [ ] | Release artifacts, `baron --version`, verified `baron install/update`, and Linux installer smoke pass; public release and Windows runtime acceptance remain blocked. |
| P19 | [ ] | Local implementation/report/evidence are current, but the full external release gate is not green. |
| P20-P27 | [ ] | Baron Nexus expansion is locally implemented through P26; the public `v0.1.1` Linux release/download/update path and `v0.1.2` release are historical, while the `v0.1.3` candidate carries the concise DeepSeek key-rotation command and installer guidance; P22 live Tencent and P27 clean-machine, authenticated-agent, first-installer, and Windows-runtime gates remain blocked. |
| P28 | [ ] | Credential validation, protected rotation, sudo reauthorization, and Ubuntu/Debian dependency bootstrap are implemented with local fixtures; clean-machine and real provider rotation acceptance remain open. |

## P0 — Baseline, contracts, and Go repository bootstrap `[X]`

**Goal:** Freeze the R1-R15 contract and create the standalone Go foundation.  
**Primary files:** `docs/specs/`, `docs/plans/`, `acceptance/`, `go.mod`,
`cmd/baron/`, `internal/`, `.gitignore`.  
**Gate:** `[X]` local baseline and contract evidence pass; live integrations are
not required by this phase.

### Implementation tasks

- [X] **P0-T01** Create the implementation branch/worktree strategy, record the starting repository state, Go version, OS/architecture, Docker/tool availability, and the fact that the supplied directory initially had no Git metadata.
- [X] **P0-T02** Create `docs/specs/baron-shared-brain-design.md` with the frozen R1-R15 product contracts from `IMPLEMENT.pdf`.
- [X] **P0-T03** Create `docs/plans/baron-shared-brain-implementation.md` with traceable phase/task identifiers.
- [X] **P0-T04** Create `acceptance/acceptance-contract.json` containing each R1-R15 contract and every final F01-F24 acceptance identifier exactly once.
- [X] **P0-T05** Create the Go package boundaries: `cmd/baron`, `internal/cli`, `internal/project`, `internal/storage`, `internal/continuity`, `internal/recovery`, `internal/memory/tencent`, `internal/hooks`, `internal/install`, `internal/doctor`, and test support.
- [X] **P0-T06** Document dependency direction so DSH/Codex adapters depend on narrow core contracts and core packages do not import upstream agent implementation details.
- [X] **P0-T07** Define the `MemoryBackend` interface and leave Tencent HTTP behavior replaceable by fake/HTTP-test implementations.
- [X] **P0-T08** Define client and canonical event enums independent of upstream DSH/Codex payload formats.
- [X] **P0-T09** Define compatibility metadata and fail-closed behavior for unsupported DSH/Codex/Tencent/plugin versions.
- [X] **P0-T10** Define preserve-first, no-destructive-migration rules for Tencent data, DSH config, Codex config, legacy Baron state, and project state.
- [X] **P0-T11** Create `go.mod` with Go 1.27.0 and minimal dependencies: Cobra, `modernc.org/sqlite`, TOML, and narrowly justified helpers.
- [X] **P0-T12** Add repository `.gitignore` for Go output, coverage/profiles, editors, runtime fixtures, credentials, and Rust `target/`/vendor output.

### Mandatory tests

- [X] **P0-TEST01** Record the clean bootstrap baseline and explicitly document that the new repository did not import a legacy Rust test suite as a release dependency.
- [X] **P0-TEST02** Build the Go skeleton for Linux amd64 and Windows amd64 with CGO disabled and no Rust/Cargo invocation.
- [X] **P0-TEST03** Verify the acceptance contract contains every R1-R15 identifier exactly once.
- [X] **P0-TEST04** Run `go test ./...`, `go vet ./...`, bounded-repository checks, and the no-`target/` check.

## P1 — CLI skeleton and Baron-owned configuration `[X]`

**Goal:** Provide the stable command surface, safe config paths, diagnostics,
exit codes, atomic writes, backups, and redaction.  
**Primary files:** `cmd/baron/`, `internal/cli/`, `internal/config/`,
`internal/install/`.  
**Gate:** `[X]` local CLI/config/install evidence passes.

### Implementation tasks

- [X] **P1-T01** Implement Cobra commands for `deepseek-harness init`, `codex-cli init`, `tencent-memory init`, `test`, `setup`, `status`, `doctor`, `repair`, `backup`, and `restore`.
- [X] **P1-T02** Make `baron setup` without a path resolve the current working directory and make an explicit path absolute/existing according to the contract.
- [X] **P1-T03** Define stable exit codes: 0 success, 2 usage, 10 missing dependency, 11 auth/config incomplete, 12 Tencent unavailable, 13 project missing, 14 unsupported upstream, 20 integrity failure, 30 partial result.
- [X] **P1-T04** Implement structured diagnostic records with concise English human output by default.
- [X] **P1-T05** Add `--json` to `test`, `status`, and `doctor` without changing the simple default UX.
- [X] **P1-T06** Use standard OS global config directories and never use the repository `.env` for global admin credentials.
- [X] **P1-T07** Implement temp-file write, sync, rename, and directory-sync atomic writes where supported.
- [X] **P1-T08** Back up user-owned DSH/Codex config before Baron modifies it.
- [X] **P1-T09** Redact API keys, bearer tokens, user keys, DeepSeek keys, Codex auth, and admin keys from logs/output.
- [X] **P1-T10** Keep business behavior in Go; shell/PowerShell installers only invoke the released binary.

### Mandatory tests

- [X] **P1-TEST01** Exercise command snapshots, help, invalid arguments, and exit codes.
- [X] **P1-TEST02** Run commands from paths containing spaces and Unicode/Vietnamese characters.
- [X] **P1-TEST03** Inject an interrupted config write and prove the original file remains valid/recoverable.
- [X] **P1-TEST04** Feed representative `sk-*`, Bearer, admin-key, and token values through redaction and prove no raw secret remains.

## P2 — Local state engine and hook runtime foundation `[X]`

**Goal:** Make local SQLite the durable authority so Baron works without Tencent,
Internet, or a resident daemon.  
**Primary files:** `internal/storage/`, `internal/hooks/`, `internal/continuity/`,
`.baron/runtime/`.  
**Gate:** `[X]` local crash/concurrency evidence passes; cgo race evidence is
recorded separately as host-blocked.

### Implementation tasks

- [X] **P2-T01** Use `modernc.org/sqlite` at `.baron/runtime/state.db`, enable WAL and foreign keys, and keep CGO disabled.
- [X] **P2-T02** Add explicit transactional schema migrations with backup/fail-closed behavior.
- [X] **P2-T03** Create tables for projects, sessions, events, work state, sync queue, memory receipts, and locks/leases.
- [X] **P2-T04** Give every event a UUID, project/session/client/event metadata, timestamp, payload hash, and idempotency key.
- [X] **P2-T05** Add short-lived per-project locks for checkpoint and local binding materialization; never hold them across network calls.
- [X] **P2-T06** Make duplicate event insertion idempotent and prevent duplicate state mutation.
- [X] **P2-T07** Derive readable `checkpoint.json` from SQLite rather than treating it as the authority.
- [X] **P2-T08** Implement hidden `baron hook <client> <event>` JSON-stdin/JSON-stdout entrypoint.
- [X] **P2-T09** Persist locally before remote work, use bounded hook timeouts, and queue slow/failed Tencent calls.
- [X] **P2-T10** Add bounded/redacted rotating runtime logs under `.baron/runtime/logs`.

### Mandatory tests

- [X] **P2-TEST01** Write 10,000 synthetic events from multiple processes and verify no corruption plus event/idempotency invariants.
- [X] **P2-TEST02** Fault a writer between temp checkpoint creation and rename; the next read must find valid prior/new JSON.
- [X] **P2-TEST03** Disable network and prove local hook persistence completes within the bounded runtime.
- [X] **P2-TEST04** Replay one event 100 times and prove canonical work state equals single delivery.

## P3 — Environment readiness and `baron test` `[X]`

**Goal:** Give users exact read-only readiness diagnostics.  
**Primary files:** `internal/doctor/`, app readiness wiring, CLI JSON output.  
**Gate:** `[X]` local fixture matrix passes; old P3 intentionally reports Docker
as a prerequisite, while new P21 adds Linux installation automation.

### Implementation tasks

- [X] **P3-T01** Detect Docker CLI and daemon separately without silently installing it from `baron test`.
- [X] **P3-T02** Detect Node/npm/npx and the pinned DSH/Codex version range.
- [X] **P3-T03** Detect uv/uvx and report the DuckDuckGo MCP prerequisite.
- [X] **P3-T04** Detect DSH availability/version.
- [X] **P3-T05** Detect DuckDuckGo, Superpowers, Reverse Skill, and Baron DSH adapter components.
- [X] **P3-T06** Detect Codex, version, and authentication readiness without reading secrets into output.
- [X] **P3-T07** Detect MemoryCore, MemoryHub, and Proxy health separately as installed/stopped/unhealthy.
- [X] **P3-T08** Detect Baron Tencent identity/team metadata and distinguish not-initialized from failure.
- [X] **P3-T09** Render actionable English diagnostics with the exact next command where possible.
- [X] **P3-T10** Keep `baron test` read-only: no user/team/agent/task/hook/file creation.

### Mandatory tests

- [X] **P3-TEST01** Dependency-absence matrix reports only the missing component.
- [X] **P3-TEST02** Installed Docker with stopped daemon reports daemon unavailable, not Docker missing.
- [X] **P3-TEST03** Installed but unauthenticated Codex reports auth incomplete without auth-file contents.
- [X] **P3-TEST04** All-green fixtures end with `All required components are ready.` and exit 0.

## P4 — DeepSeek Harness baseline initializer `[X]`

**Goal:** Install and verify DSH, DuckDuckGo MCP, Superpowers, Reverse Skill,
and the Baron adapter while preserving user-owned DSH settings.  
**Primary files:** `internal/install/commands.go`, `internal/install/install.go`,
`internal/install/assets/`, `adapters/dsh/`, `configs/compatibility.json`.  
**Gate:** the pinned DSH/profile/MCP smoke and the automated credential path
have fresh evidence; no-key behavior is accepted as DSH's canonical actionable
`MISSING_CREDENTIAL` result while Baron handles interactive collection.

### Implementation tasks

- [X] **P4-T01** Pin tested DSH `0.1.1-rc.2`, commit metadata, and unsupported-version diagnostics.
- [X] **P4-T02** Install/verify the official `@deepseek-ai/dsh` package through the supported npm mechanism.
- [X] **P4-T03** Run a real safe DSH startup probe such as `dsh web --no-open` without requiring a browser.
- [X] **P4-T04** Require/verify pnpm only for the selected plugin workflow and report it separately from npm.
- [X] **P4-T05** Install pinned `superpowers-dsh` through the official DSH profile plugin mechanism.
- [X] **P4-T06** Install and verify the pinned immutable `dsh-reverse-skill` commit.
- [X] **P4-T07** Configure the pinned DuckDuckGo MCP package through uvx.
- [X] **P4-T08** Register stable `ddg-search` without overwriting unrelated user MCP rows.
- [X] **P4-T09** Materialize and install the Baron DSH adapter package/bundle.
- [X] **P4-T10** Merge only Baron-owned DSH fragments and preserve user model/provider/skill settings.
- [X] **P4-T11** Detect/reuse `DEEPSEEK_API_KEY`, collect a missing key with hidden terminal input, write DSH's official version-1 credential store atomically at mode `0600`, and keep it out of Baron project state.
- [X] **P4-T12** Write installation receipts with versions, commits, sources, and checksums where available.

### Mandatory tests

- [X] **P4-TEST01** Fresh Ubuntu with Node/uv produces runnable DSH and all four mandatory Baron baseline components.
- [X] **P4-TEST02** Local merge fixture proves rerun idempotence and custom DSH rows survive; real DSH profile confirmation passes.
- [X] **P4-TEST03** Remove Superpowers from a real profile, rerun init, and prove repair.
- [X] **P4-TEST04** Remove Reverse Skill from a real profile, rerun init, and prove repair.
- [X] **P4-TEST05** Perform a real DuckDuckGo MCP search and classify network/rate-limit failure separately from install failure.
- [X] **P4-TEST06** Start DSH without a DeepSeek key and prove it reports the canonical actionable `MISSING_CREDENTIAL` result without corrupting config; Baron’s initializer collects the key automatically when interactive.

## P5 — Codex CLI initializer and hook capability baseline `[X]` PASS

**Goal:** Install/verify Codex, preserve user-owned skills/config, and register
officially shaped hooks.  
**Primary files:** `internal/install/install.go`, `internal/app/app.go`,
`internal/doctor/`, `adapters/codex/`, Codex hook tests.
**Gate:** pinned install, authenticated interactive CLI readback, and the tested CLI-only Desktop boundary pass; Desktop itself remains outside the supported runtime boundary.

### Implementation tasks

- [X] **P5-T01** Pin/test Codex `0.149.0` and use the official package installation path.
- [X] **P5-T02** Verify `codex --version` and non-secret authentication readiness.
- [X] **P5-T03** Report the supported Codex sign-in action when unauthenticated without treating installation as failed.
- [X] **P5-T04** Merge the pinned official nested hook shape for session/prompt/tool/stop/end/compact events.
- [X] **P5-T05** Back up and merge only Baron-owned Codex hook entries.
- [X] **P5-T06** Never install DSH skills into Codex and preserve unrelated user skills/plugins.
- [X] **P5-T07** Detect interactive Codex hook trust/enablement and provide the exact user approval instruction.
- [X] **P5-T08** Run real Codex Desktop compatibility validation; otherwise publish a truthful CLI-only support boundary.
- [X] **P5-T09** Materialize an explicit Codex adapter bridge with lifecycle mapping, bounded fail-open behavior, and a Baron-owned receipt without taking ownership of Codex skills/auth.

### Mandatory tests

- [X] **P5-TEST01** Fresh fixture has the pinned Codex binary and version.
- [X] **P5-TEST02** Local custom-hook/skill fixture survives except for the intended Baron merge.
- [X] **P5-TEST03** Missing auth produces no secret output and an auth-incomplete readiness result.
- [X] **P5-TEST04** Synthetic Codex hook input reaches Baron and receives valid bounded JSON.
- [X] **P5-TEST05** The embedded Codex adapter materializes safely, preserves the direct Go hook path, and forwards a real SessionStart payload to Baron.

## P6 — TencentDB Agent Memory initializer and Baron identity `[ ]` BLOCKED

**Goal:** Automate the managed Tencent deployment and create the Baron user,
user key, and `baron-projects` team without touching legacy entities.  
**Primary files:** `internal/install/tencent.go`, `internal/app/app.go`,
`internal/memory/tencent/`, global config/receipts.  
**Gate:** Docker/Tencent live stack is unavailable in the current environment.

### Implementation tasks

- [X] **P6-T01** Check Docker CLI/daemon and report the prerequisite without silently installing it under the original contract; P21 supersedes this behavior for Ubuntu/Debian.
- [X] **P6-T02** Clone/fetch/checkout the pinned Tencent Agent Memory deployment into a Baron-managed directory.
- [X] **P6-T03** Preserve upstream `.env.example` structure and create a restrictive `.env` without overwriting user values.
- [X] **P6-T04** Verify/start the official Core, Hub, and Proxy services with safe command-output handling.
- [X] **P6-T05** Read `.admin-key` only in-process for administrative API calls and never print/copy it to project state.
- [X] **P6-T06** Create/reuse business user `baron` through Tencent v3 metadata APIs.
- [X] **P6-T07** Create/reuse one active Baron user key and keep the master copy in protected global state.
- [X] **P6-T08** Create/reuse the `baron-projects` team and capture `team_id`.
- [X] **P6-T09** Do not create `Main Developer`; project agents are created by setup.
- [X] **P6-T10** Verify auth, team lookup, Core/Hub health, and a data-plane read.
- [X] **P6-T11** Leave pre-existing default-team/Main Developer/legacy entities untouched.
- [X] **P6-T12** Add safe rollback for newly created Baron metadata when a later step fails; never delete pre-existing entities.

### Mandatory tests

- [ ] **P6-TEST01** Fresh Docker deployment creates exactly one Baron user, active key, team, and healthy services.
- [X] **P6-TEST02** Five repeated inits produce no duplicate user/team/key explosion; live before/after metadata snapshots remained 1 managed user, 2 existing keys, and 1 managed team.
- [ ] **P6-TEST03** Legacy Tencent entity metadata remains byte/hash-equivalent after init.
- [ ] **P6-TEST04** Stopped Core is reported unavailable and becomes green after restart without re-init.
- [ ] **P6-TEST05** Live command/log scan proves admin and user keys never appear in plaintext.

## P7 — Project identity and `baron setup` `[X]`

**Goal:** Make `baron setup` the single per-project provisioning command with a
stable identity that survives path and operating-system changes.  
**Primary files:** `internal/project/`, `internal/app/app.go`,
`internal/config/`, project security tests.  
**Gate:** `[X]` local setup/move/path/permission evidence passes.

### Implementation tasks

- [X] **P7-T01** Resolve explicit project paths or the current directory and prefer the Git top-level unless an existing Baron project file establishes a nearer authority.
- [X] **P7-T02** Validate existing `.baron/project.toml` and never silently regenerate its `project_id`.
- [X] **P7-T03** Generate a cryptographically random `prj-` project ID and a display-safe project name when metadata is absent.
- [X] **P7-T04** Create `.baron` and runtime subdirectories with restrictive permissions.
- [X] **P7-T05** Write `project.toml` atomically, commit-safely, and without secrets.
- [X] **P7-T06** Write `.baron/.env` atomically from global identity plus project team/agent binding when available.
- [X] **P7-T07** Merge `.gitignore` rules for `.baron/.env`, checkpoint, runtime, backups, and temp files without destroying user rules.
- [X] **P7-T08** Enforce mode 0600 for `.baron/.env` on Unix and record best-effort Windows ACL warnings.
- [X] **P7-T09** Initialize a session-independent SQLite project record.
- [X] **P7-T10** Make rerun setup repair Baron-owned state without changing project ID or namespace.
- [X] **P7-T11** Reject filesystem roots, unsafe global directories, and symlinked Baron-owned paths.

### Mandatory tests

- [X] **P7-TEST01** Run setup twice and prove stable project ID and expected file hashes.
- [X] **P7-TEST02** Move/clone a project with the same `project.toml` and resolve the same project ID.
- [X] **P7-TEST03** Use spaces and Vietnamese/Unicode characters in project paths.
- [X] **P7-TEST04** Preserve existing Git-ignore comments/rules and add Baron rules once.
- [X] **P7-TEST05** Prove `.baron/.env` is not world-readable on Ubuntu.

## P8 — Tencent project namespace provisioning and binding `[ ]` BLOCKED

**Goal:** Map each project to exactly one Tencent Agent under `baron-projects`
and reconstruct local bindings safely.  
**Primary files:** `internal/app/app.go`, `internal/memory/tencent/client.go`,
project binding/identity tests.  
**Local implementation:** HTTP fixtures pass. A live Tencent-backed project
setup now creates/reuses an isolated Agent and Wiki; clean-machine and
multi-project acceptance remain release gates.

### Implementation tasks

- [X] **P8-T01** Query for a Baron-managed Agent using `project_id`, never display name alone.
- [X] **P8-T02** Store `project_id` in Agent metadata/description or a Baron registry when the Tencent schema lacks a safe custom field.
- [X] **P8-T03** Create a display-safe project Agent and link its owner/team correctly when absent.
- [X] **P8-T04** Disambiguate same-name agents with different project IDs and reject ambiguous binding.
- [X] **P8-T05** Write project team/agent/user/endpoints/service values into protected `.baron/.env`.
- [X] **P8-T06** Verify a benign isolated L0/L1/L3 read before setup reports success.
- [X] **P8-T07** Create a non-secret project identity marker for diagnostics when needed.
- [X] **P8-T08** Show project mapping in `baron status` without exposing user key.

### Mandatory tests

- [X] **P8-TEST01** HTTP fixture provisions distinct agents for Project A and Project B under the same team.
- [X] **P8-TEST02** Identical directory basenames on different paths remain distinct by project ID.
- [X] **P8-TEST03** Removing `.baron/.env` allows setup to reconstruct the existing binding rather than duplicate it.
- [ ] **P8-TEST04** Binding a project to an Agent belonging to another project fails before remote use. The local fixture passes, but live Tencent confirmation remains unchecked at the phase gate.

## P9 — Continuity checkpoint engine `[X]`

**Goal:** Capture enough evidence to resume after switching agents, crash, network
failure, or power loss.  
**Primary files:** `internal/continuity/`, `internal/recovery/`, Git inspection,
checkpoint tests.  
**Gate:** `[X]` local checkpoint/recovery evidence passes.

### Implementation tasks

- [X] **P9-T01** Define `WorkState` with goal, status, successful/current/next steps, changed files, Git data, tests, errors, client, timestamps, and completion verification.
- [X] **P9-T02** Define active, clean-closed, interrupted, stale, and recovered session states; hook silence never means completion.
- [X] **P9-T03** Record session start and repository snapshot metadata.
- [X] **P9-T04** Derive changed files, command/test evidence, and current-step evidence from meaningful events.
- [X] **P9-T05** Capture bounded final assistant/turn summaries and next action when available.
- [X] **P9-T06** Mark sessions cleanly closed while keeping task completion independent.
- [X] **P9-T07** Classify a session interrupted when active heartbeat/closure is absent beyond threshold.
- [X] **P9-T08** Materialize versioned, timestamped checkpoint JSON after every state-changing transaction.
- [X] **P9-T09** Require explicit completion plus verification evidence before marking a task complete.
- [X] **P9-T10** Inspect bounded Git HEAD/status/changed-file metadata without storing the whole repository.

### Mandatory tests

- [X] **P9-TEST01** Three-step synthetic task leaves a checkpoint matching the latest committed event.
- [X] **P9-TEST02** Kill an unclosed session and recover interrupted status plus last/current step.
- [X] **P9-TEST03** Clean-close an unfinished task and keep it `IN_PROGRESS`.
- [X] **P9-TEST04** Modify files externally and report repository drift for reconciliation.

## P10 — Memory capture, recall, and canonical context `[ ]` BLOCKED

**Goal:** Synchronize safe long-term memory and produce bounded project-only
context without blocking local continuity.  
**Primary files:** `internal/memory/tencent/`, `internal/continuity/context.go`,
`internal/continuity/sync.go`, `internal/hooks/`.  
**Local implementation:** HTTP fixtures and local fallback pass. **Gate:** live
Tencent read/write is pending.

### Implementation tasks

- [X] **P10-T01** Implement v3 conversation add/search, atomic search, and core/profile reads with isolation fields.
- [X] **P10-T02** Capture project instructions, final assistant summaries, decisions, test/error evidence, and continuity summaries while excluding system prompts, hidden reasoning, raw auth, and tool noise.
- [X] **P10-T03** Redact before local persistence and Tencent sync using patterns plus exact loaded secrets.
- [X] **P10-T04** Convert local events to bounded memory records with client/session/time/evidence/project fields.
- [X] **P10-T05** Search using current prompt, goal, file, command, and error terms.
- [X] **P10-T06** Merge L3/L2/L1/L0 memory and local continuity into one source-labelled context packet.
- [X] **P10-T07** Deduplicate repeated DSH/Codex records with normalized hashes and idempotency receipts.
- [X] **P10-T08** Enforce character/token limits and prioritize local current continuity before broader history.
- [X] **P10-T09** Label recalled memory as historical reference and escape delimiters so memory cannot grant permissions.
- [X] **P10-T10** Return local context immediately when Tencent fails and queue remote capture without blocking the agent.

### Mandatory tests

- [X] **P10-TEST01** HTTP fixture writes a Codex memory record and recalls it under the same Project A isolation context.
- [X] **P10-TEST02** Project B fixture cannot retrieve Project A content.
- [X] **P10-TEST03** Fake API key injected into a tool result is absent from local and remote payload fixtures.
- [X] **P10-TEST04** Duplicate summaries from DSH/Codex produce one normalized recall record.
- [X] **P10-TEST05** Tencent outage still returns local checkpoint context within the hook budget and grows the queue.

## P11 — Codex adapter and lifecycle hooks `[X]` PASS

**Goal:** Make normal Codex sessions automatically participate in shared memory
and continuity.  
**Primary files:** `internal/install/install.go`, `internal/hooks/`, `internal/app/`,
Codex hook tests.  
**Gate:** authenticated interactive Codex CLI and the tested CLI-only Desktop
boundary pass; full paired-agent handoff remains the separate P12/P13 gate.

### Implementation tasks

- [X] **P11-T01** Install merge-safe Baron handlers in the pinned Codex hook representation.
- [X] **P11-T02** Resolve project, record session, inspect interrupted work, and prepare recovery at SessionStart.
- [X] **P11-T03** Persist user prompt locally and inject bounded memory/context at UserPromptSubmit.
- [X] **P11-T04** Record PostToolUse command/file/test/error fields from structured payloads.
- [X] **P11-T05** Checkpoint around compaction events where the supported hook surface exposes them.
- [X] **P11-T06** Capture Stop summary/next action without automatically completing the task.
- [X] **P11-T07** Mark SessionEnd cleanly closed and queue final memory sync.
- [X] **P11-T08** Fail open on malformed state/Tencent outage while retaining diagnostics.
- [X] **P11-T09** Bound hook execution and convert remote retries into queue state.
- [X] **P11-T10** Detect and explain interactive Codex hook trust/enablement state.
- [X] **P11-T11** Validate Codex Desktop lifecycle or publish a tested CLI-only boundary.

### Mandatory tests

- [X] **P11-TEST01** Real interactive Codex Project A prompt receives a known memory sentinel; after the hook-path repair, an authenticated Codex v0.149.0 PTY session executed SessionStart/UserPromptSubmit and returned `CODEX_INTERACTIVE_CANONICAL_HOOK_SEEN`.
- [X] **P11-TEST02** Synthetic Codex edit/failing-test payload records changed file, command, and failure evidence.
- [X] **P11-TEST03** Local fixture with Tencent disabled continues and grows `sync_queue`.
- [X] **P11-TEST04** Corrupting a non-critical runtime log leaves the hook fail-open and doctor-diagnosable.
- [X] **P11-TEST05** Local custom hook/skill fixture remains functional after merge; the canonical Baron hooks were also confirmed by a real Codex session.

## P12 — DeepSeek Harness adapter and lifecycle hooks `[X]` PASS

**Goal:** Give DSH the same canonical project brain while retaining its own
model, plugins, and search baseline.  
**Primary files:** `adapters/dsh/`, `internal/install/assets/dsh-adapter/`,
DSH profile patch and compatibility metadata.  
**Gate:** real DSH runtime/auth/plugin and canonical failure-checkpoint acceptance pass; cross-client takeover remains the separate P13 gate.

### Implementation tasks

- [X] **P12-T01** Implement the Baron DSH plugin using the DSH plugin architecture, not upstream source patches.
- [X] **P12-T02** Subscribe to session start, pre-step, turn stopping, session events/flush, and tool evidence surfaces.
- [X] **P12-T03** Resolve project/session at DSH start and prepare prior Codex/DSH recovery context.
- [X] **P12-T04** Inject bounded historical-reference context before a model step.
- [X] **P12-T05** Capture DSH command/file/test evidence in the canonical local event format.
- [X] **P12-T06** Persist final response/summary and next-action evidence on turn stop.
- [X] **P12-T07** Mark clean close on flush/dispose without conflating it with task completion.
- [X] **P12-T08** Coexist with DuckDuckGo, Superpowers, Reverse Skill, and user plugins.
- [X] **P12-T09** Leave DeepSeek model traffic configured directly to the user’s provider.
- [X] **P12-T10** Enforce adapter API/version compatibility and actionable unsupported-version errors.

### Mandatory tests

- [X] **P12-TEST01** Real DSH receives a Codex-written memory sentinel; the prompt omitted the sentinel and the authenticated DSH model returned the exact value injected by Baron from the prior Codex checkpoint.
- [X] **P12-TEST02** Real DSH failing command produces a canonical checkpoint containing the real command, stderr evidence, `status=failed`, `class=tool_error`, and `exit_code=23`.
- [X] **P12-TEST03** Real DSH `web` and `headless` profiles discover all mandatory plugins and Baron adapter together; live dumps showed Superpowers, Reverse Skill, DuckDuckGo MCP, and `baron-dsh-adapter` in each composed profile.
- [X] **P12-TEST04** Local outage fixture keeps DSH-side local continuity behavior available; real DSH confirmation remains pending.
- [X] **P12-TEST05** Unsupported-version compatibility fixture fails clearly without corrupting profile state.

## P13 — Cross-agent handoff and interrupted-work recovery `[X]` PASS

**Goal:** Deliver the core product promise: one agent takes over unfinished work
from the other using evidence rather than invented state.  
**Primary files:** `internal/recovery/`, `internal/continuity/`, `internal/hooks/`,
handoff receipts.  
**Gate:** live failure recovery, explicit unverified handoff, concurrent
Codex/DSH event evidence, and verified-completion/no-false-interruption all
pass. The remaining release matrix is tracked by P19/P22/P27.

### Implementation tasks

- [X] **P13-T01** Define `RecoveryPacket` with project/client/session, interruption, goal/status, steps, files, Git snapshots, test/error evidence, and memory citations.
- [X] **P13-T02** Compare checkpoint Git metadata to current Git and mark drift before edits.
- [X] **P13-T03** Generate concise known/unknown/failed/rerun/next-action recovery context.
- [X] **P13-T04** Preserve evidence provenance and never invent a broken line without captured tool/test evidence.
- [X] **P13-T05** Tell the next agent to rerun unfinished tests rather than trust stale results.
- [X] **P13-T06** Provide changed-file and bounded diff/hash evidence for in-progress edits.
- [X] **P13-T07** Convert explicitly completed, verified work into a recent project summary rather than an interruption warning.
- [X] **P13-T08** State uncertainty when the prior session died before a meaningful checkpoint.
- [X] **P13-T09** Store source-to-target handoff receipts with event/checkpoint IDs.

### Mandatory tests

- [X] **P13-TEST01** Real Codex failing work was killed after the durable failing tool result; DSH received the checkpoint and reran the exact marker command with exit code 0.
- [X] **P13-TEST02** Real DSH work labeled `status=not_yet_verified` was handed to Codex, which returned the exact sentinel without claiming pass/fail.
- [X] **P13-TEST03** Verified completion is not falsely classified as interrupted by the other agent; a real Codex clean-close/final ordering probe followed by DSH headless handoff preserved `clean_closed`, `completed`, and `completion_verified`.
- [X] **P13-TEST04** Local drift fixture warns and requires Git reconciliation before implementation.

## P14 — Offline queue, retry, concurrency, and crash consistency `[ ]` BLOCKED

**Goal:** Preserve locally committed continuity during Tencent/network outages and
sync it exactly once after recovery.  
**Primary files:** `internal/continuity/sync.go`, `internal/storage/`,
`internal/hooks/`, `internal/doctor/`, fault-injection tests.  
**Gate:** local queue fixtures pass; SIGKILL/race/live outage evidence is pending.

### Implementation tasks

- [X] **P14-T01** Persist every redacted remote capture in `sync_queue` with an idempotency key before network send.
- [X] **P14-T02** Atomically mark successful Tencent writes delivered and store remote request/receipt IDs.
- [X] **P14-T03** Apply bounded exponential backoff with jitter and leave remaining retries for later hooks/status/repair.
- [X] **P14-T04** Flush a small bounded old-queue batch before a hook returns when time remains.
- [X] **P14-T05** Merge concurrent DSH/Codex events from event evidence rather than blind JSON overwrite.
- [X] **P14-T06** Represent conflicting simultaneous goals as separate streams or require explicit correlation.
- [X] **P14-T07** Use transaction/fsync boundaries so a delivered queue item has a corresponding receipt.
- [X] **P14-T08** Bound queue disk growth and warn without silently deleting unsynchronized work.
- [X] **P14-T09** Provide daemon-free `repair`/`doctor` queue flush diagnostics.

### Mandatory tests

- [X] **P14-TEST01** Local outage fixture persists 20 events and continues agent operations with expected queue count.
- [X] **P14-TEST02** Local fixture restores backend and flushes each queued record exactly once.
- [X] **P14-TEST03** SIGKILL a hook at controlled remote-delivery points and prove pending/delivered but never lost/duplicated semantics.
- [X] **P14-TEST04** Real DSH and Codex events interleaved on the same project while the DSH tool was running; SQLite integrity was `ok`, idempotency duplicates were 0, and pending/dead-letter queue counts remained 0.

## P15 — Project isolation, legacy coexistence, and migration safety `[ ]` BLOCKED

**Goal:** Prove no project contamination and preserve all legacy Tencent/Baron
data unless the user explicitly migrates.  
**Primary files:** `internal/project/`, `internal/memory/tencent/`,
`internal/app/`, isolation/security tests.  
**Gate:** local negative tests pass; legacy/live Tencent snapshot is pending.

### Implementation tasks

- [X] **P15-T01** Attach `project_id` to every local event and require team/agent for every Tencent data operation.
- [X] **P15-T02** Centralize isolation context construction so adapters cannot invent raw Tencent fields.
- [X] **P15-T03** Reject project `.env` mappings that conflict with Baron registry/Tencent identity metadata.
- [X] **P15-T04** Require `agent_id` in normal recall and forbid unscoped team-wide search.
- [X] **P15-T05** Leave legacy default-team/Main Developer and prior identities untouched.
- [X] **P15-T06** Keep any future migration explicit, dry-run, selective, backup-first, and outside v1 setup.
- [X] **P15-T07** Show only non-secret current project mapping in `baron status`.

### Mandatory tests

- [X] **P15-TEST01** Ten-project local fixture returns only each project’s sentinel.
- [X] **P15-TEST02** Tampering Project B to Project A’s agent fails before memory query/write.
- [ ] **P15-TEST03** Live legacy Tencent snapshot remains unchanged except allowed service timestamps.
- [X] **P15-TEST04** Same-name projects on different paths retain independent project IDs and local memory.

## P16 — Backup, restore, reinstall, and cross-OS recovery `[ ]` BLOCKED

**Goal:** Make OS reinstall/migration safe while preserving project identity and
Tencent mappings.  
**Primary files:** `internal/app/backup.go`, `internal/install/update.go`,
backup/restore tests, `install.ps1`.  
**Gate:** local archive/checksum/rollback evidence passes; Docker-volume and
Windows runtime acceptance is pending.

### Implementation tasks

- [X] **P16-T01** Define versioned backup manifest for global config policy, Tencent metadata/volumes policy, project registry, optional checkpoints, and receipts.
- [X] **P16-T02** Stage `baron backup <destination>`, checksum it, archive it, and verify before claiming success.
- [X] **P16-T03** Exclude plaintext credentials by default and document re-key/re-init; do not place user keys in portable archives.
- [X] **P16-T04** Preflight/validate/conflict-check `baron restore <archive>` and back up current state before mutation.
- [X] **P16-T05** Restore Tencent Docker data before project binding validation and verify user/team/project metadata.
- [X] **P16-T06** Reconstruct `.baron/.env` from `project.toml` plus restored/global mapping after reinstall.
- [X] **P16-T07** Preserve project ID when moving between Ubuntu and Windows.
- [X] **P16-T08** Document that uncommitted source bytes require Git/separate backup and cannot be recovered from memory alone.
- [X] **P16-T09** Complete Windows filesystem/ACL and Docker Desktop/Engine path adaptation.

### Mandatory tests

- [ ] **P16-TEST01** Clean Ubuntu restore with Docker, clone, setup, and prior memory recall.
- [ ] **P16-TEST02** Ubuntu-to-Windows migration preserves project ID and Tencent Agent mapping.
- [X] **P16-TEST03** Corrupt backup checksum causes restore refusal before current-state mutation.
- [X] **P16-TEST04** Existing conflicting Baron state stops restore until a safe mode is explicitly selected.

## P17 — Security hardening and adversarial memory safety `[X]`

**Goal:** Prevent secret persistence, memory-to-permission escalation, path
escape, unsafe hook execution, and untrusted memory injection.  
**Primary files:** `SECURITY.md`, `internal/config/`, `internal/project/`,
`internal/hooks/`, `internal/install/`, `internal/memory/tencent/`.  
**Gate:** `[X]` local security tests and `go vet` evidence pass; external
dependency review remains outside this run.

### Implementation tasks

- [X] **P17-T01** Threat-model project files, hook payloads, Tencent memory, external search, DSH/Codex config, credentials, and shell execution.
- [X] **P17-T02** Use direct argv APIs and never build shell commands from unescaped project paths.
- [X] **P17-T03** Redact known patterns and exact loaded secrets before local logs/memory/Tencent sync.
- [X] **P17-T04** Label retrieved memory and external search as untrusted historical data; never turn it into permissions.
- [X] **P17-T05** Reject symlink/path traversal attempts that escape the project root.
- [X] **P17-T06** Check `.baron/.env` and global credential permissions; report weak permissions in doctor.
- [X] **P17-T07** Exclude Codex auth, DeepSeek keys, admin keys, SSH keys, and full application `.env` contents from memory.
- [X] **P17-T08** Limit tool output, memory record size, logs, and context to prevent exhaustion.
- [X] **P17-T09** Produce checksummed/sourced installation receipts for third-party components where signing is unavailable.
- [X] **P17-T10** Publish `SECURITY.md` scope and responsible-disclosure boundary.

### Mandatory tests

- [X] **P17-TEST01** Prompt-injection memory remains historical and cannot change tool authorization.
- [X] **P17-TEST02** Shell metacharacters in project paths do not execute during setup/hooks.
- [X] **P17-TEST03** Symlinked project `.env` outside the project is rejected/safely handled.
- [X] **P17-TEST04** Secret corpus scan finds no known raw credentials in local state/checkpoint/logs/fixtures.
- [X] **P17-TEST05** Malformed hook/fuzz-style inputs fail open while preserving diagnostic integrity.

## P18 — Packaging, update, repair, and release engineering `[ ]` BLOCKED

**Goal:** Ship a reproducible native Go release with simple install, repair,
compatibility, and rollback.  
**Primary files:** `scripts/build-release.sh`, `install.sh`, `install.ps1`,
`.github/workflows/ci.yml`, `internal/install/`, `README.md`.  
**Gate:** Linux artifact/build evidence passes; Windows runtime and full clean-OS
release acceptance are pending.

### Implementation tasks

- [X] **P18-T01** Build Linux amd64 and Windows amd64 native Go binaries without Rust or CGO; add macOS only after platform gates are green.
- [X] **P18-T02** Install the one Go binary through shell/PowerShell installers and refuse silent replacement of a legacy binary.
- [X] **P18-T03** Maintain compatibility metadata for DSH, Codex, Tencent, DuckDuckGo, Superpowers, Reverse Skill, and adapter versions.
- [X] **P18-T04** Make `baron repair` touch only Baron-owned adapters/hooks/config and validate the result.
- [X] **P18-T05** Make `baron doctor` report core/local, DSH, Codex, Tencent, project, queue, permissions, and compatibility layers.
- [X] **P18-T06** Implement update staging, migration backup, validation, and rollback.
- [X] **P18-T07** Generate checksums, module SBOM, Go toolchain metadata, and release manifest; verify downloads before install/update.
- [X] **P18-T07B** Use a reproducible Go release pipeline with pinned Go, `-trimpath`, deterministic version metadata, and external caches.
- [X] **P18-T08** Keep README quick start limited to the simple user command surface and put diagnostics in troubleshooting.
- [X] **P18-T09** Document the current Docker prerequisite and supported platform boundaries; P21 will replace the Linux prerequisite with automated bootstrap.
- [X] **P18-T10** Enforce that a public release requires complete P19 acceptance evidence, not only local artifact smoke.
- [X] **P18-T11** Embed Baron Nexus version `0.1.1` and expose the stable `baron --version` output contract.
- [X] **P18-T12** Add verified GitHub release metadata/checksum download, atomic `baron install`, idempotent `baron update`, and rollback validation without touching project/Tencent state.
- [X] **P18-T13** Make Linux/PowerShell first-download installers use the verified GitHub release channel and add a tag-triggered public release workflow.

### Mandatory tests

- [X] **P18-TEST01** Release Linux artifact runs setup/status/hook smoke in an isolated project without a source checkout.
- [ ] **P18-TEST02** Fresh Windows artifact and installer run all subcommands; legacy collision produces safe migration guidance.
- [X] **P18-TEST03** Upgrade from a previous release preserves project IDs and Tencent mappings.
- [X] **P18-TEST04** Local repair/update fixtures restore missing Baron-owned hook/profile components without unrelated changes.
- [X] **P18-TEST05** Injected migration failure restores the prior binary/config/database.
- [X] **P18-TEST06** Release audit proves no Rust/Cargo dependency, no `target/`, CGO policy compliance, and native artifact launch.
- [X] **P18-TEST07** Local release fixture proves `baron install/update` reject checksum/manifest/version failures before mutation and preserve rollback state.
- [X] **P18-TEST08** Versioned Linux/Windows `0.1.1` artifacts, installers, manifest, SBOM, toolchain metadata, and SHA256SUMS verify locally.
- [ ] **P18-TEST09** Public GitHub `v0.1.1` release downloads through the first installer and a clean user can run `baron --version` and `baron update`.

## P19 — Final full-system acceptance and release gate `[ ]` BLOCKED

**Goal:** Run the exact full user journey and resilience matrix; this is the
authority for the word “done.”  
**Primary files:** `docs/implementation/FINAL_ACCEPTANCE_REPORT.md`, release
artifacts, acceptance logs.  
**Gate:** every P19 test must pass; no waiver is allowed for isolation,
continuity, Tencent, DSH, or security contracts.

### Implementation tasks

- [ ] **P19-T01** Create a clean Ubuntu environment with Docker but no DSH/Codex/Tencent/Baron project state.
- [X] **P19-T02** Run DSH init with a supported test credential and verify DSH, DuckDuckGo, Superpowers, Reverse Skill, and Baron adapter.
- [X] **P19-T03** Run Codex init, complete supported authentication, and verify hooks while preserving custom skills.
- [X] **P19-T04** Run Tencent init and verify Baron user/key/team plus all deployed services.
- [X] **P19-T05** Run `baron test` and require all components green with exit 0.
- [X] **P19-T06** Create Project A and verify its unique project/Tencent Agent mapping.
- [X] **P19-T07** Create Project B and verify its isolated mapping.
- [X] **P19-T08** Run a real Codex-to-DSH unfinished failing-test handoff; the killed Codex failure was recovered and rerun by DSH.
- [X] **P19-T09** Run a real DSH-to-Codex unfinished/unverified handoff; Codex received the explicit not-yet-verified state.
- [X] **P19-T10** Disable Tencent, accumulate queue, restore connectivity, and verify exact-once synchronization.
- [X] **P19-T11** Kill processes at checkpoint boundaries and verify safe recovery.
- [X] **P19-T12** Run concurrent real Codex/DSH same-project events and verify database/state integrity; the live journal had interleaved timestamps, SQLite integrity `ok`, no duplicate idempotency keys, and zero pending/dead-letter items.
- [X] **P19-T13** Run the 10-project isolation suite.
- [ ] **P19-T14** Run backup, clean reinstall, restore, clone, setup, and recall.
- [ ] **P19-T15** Repeat core acceptance on a Windows release candidate.
- [X] **P19-T16** Maintain a final acceptance report with candidate revision, versions, tests, artifacts, limitations, and release decision; the current report truthfully says BLOCKED.

### Mandatory tests

- [ ] **P19-TEST01** All three init commands plus `baron test` pass on clean Ubuntu.
- [X] **P19-TEST02** DSH baseline is functional end-to-end.
- [X] **P19-TEST03** Codex CLI auth/hooks are functional and custom skills survive.
- [X] **P19-TEST04** Codex unfinished failing work was killed after `tool_finished`, recovered by DSH, and continued with an exit-0 rerun.
- [X] **P19-TEST05** DSH unfinished work explicitly labeled not-yet-verified was handed to real Codex without an invented pass/failure.
- [X] **P19-TEST06** Power-loss simulation never falsely completes a task and leaves a usable durable checkpoint.
- [X] **P19-TEST07** Network loss preserves local continuity and syncs the queue exactly once after recovery.
- [X] **P19-TEST08** Ten-project isolation has zero cross-project memory leakage.
- [ ] **P19-TEST09** Backup/restore reconnects the same project ID and Tencent Agent on a clean OS.
- [ ] **P19-TEST10** Known secrets are absent from persisted state/Tencent exports and memory cannot escalate tool permissions.
- [X] **P19-TEST11** Local Go tests, vet, CGO-free release smoke, and supported release audit pass; race mode is recorded as blocked by missing gcc.
- [ ] **P19-TEST12** Ubuntu and Windows release candidates satisfy the same identity/memory contracts.

## P20 — Baron Nexus rebrand and compatibility lock `[X]`

**Goal:** Rename the product/display identity to Baron Nexus without breaking
the existing `baron` command, project files, module compatibility, or user
workflow.  
**Primary files:** `README.md`, `SECURITY.md`, `docs/`, `configs/`, CLI help,
release manifest, receipts, acceptance/report files.  
**Gate:** all public text says Baron Nexus; technical command compatibility is
explicitly preserved.

### Implementation tasks

- [X] **P20-T01** Replace user-facing `Baron Shared Brain` branding with `Baron Nexus` in README, docs, help text, receipts, reports, and release metadata.
- [X] **P20-T02** Keep the executable and command path as `baron`; do not force users to migrate existing shell hooks or scripts to `nexus`.
- [X] **P20-T03** Update compatibility/config schemas, acceptance/report headings, installation messages, and product descriptions to the Nexus name.
- [X] **P20-T04** Add a rebrand regression scan that fails if the old public product name remains outside historical/spec provenance where it is intentionally required.
- [X] **P20-T05** Run a clean binary/help/release smoke and record that branding changed while command behavior and project IDs did not.

### Mandatory tests

- [X] **P20-TEST01** Repository public-text scan finds Baron Nexus branding and only approved historical references to the predecessor name.
- [X] **P20-TEST02** `baron --help`, `baron test --json`, and hook output keep the existing machine-facing command/JSON contract.
- [X] **P20-TEST03** Existing `.baron/project.toml`, global state, receipts, and project IDs survive the branding migration unchanged.

## P21 — Ubuntu/Debian Linux bootstrap and sudo preflight `[X]`

**Goal:** Make `baron tencent-memory init` install Docker on supported Linux
without partial downloads or mid-run privilege surprises.  
**Primary files:** new Linux bootstrap package under `internal/install/`,
`internal/app/app.go`, `internal/doctor/`, package-manager fixtures, README
platform instructions.  
**Gate:** `[X]` the command checks sudo before any network operation, installs only on
Ubuntu/Debian, and is safe to rerun.

### Implementation tasks

- [X] **P21-T01** Detect Linux, Ubuntu/Debian release identity, architecture, interactive terminal availability, package manager, and unsupported distributions before touching the network.
- [X] **P21-T02** Run a non-destructive sudo preflight at the very beginning; if sudo is missing, unauthorized, or cannot authenticate, stop with the exact `sudo -v`/administrator instruction and leave no downloaded state.
- [X] **P21-T03** Build an official Docker apt-repository installation plan for Ubuntu/Debian with distro/architecture validation, avoiding arbitrary `curl | sh` installers.
- [X] **P21-T04** Install Docker Engine, CLI, containerd, Buildx, and Compose plugin through the validated package path using sudo only for system operations.
- [X] **P21-T05** Enable/start Docker service, verify daemon readiness, and handle an already-installed/stopped/outdated Docker installation without duplicate packages.
- [X] **P21-T06** Keep downloaded source/config user-owned, avoid silently adding the user to the root-equivalent `docker` group, and clearly report any required logout/re-login if a supported permission change is selected.
- [X] **P21-T07** Add system/container restart behavior so Tencent services return after Docker starts/reboots; preserve service logs and avoid starting duplicate stacks.
- [X] **P21-T08** Make interrupted installation resumable/repairable, clean only Baron-owned partial state, and expose `baron doctor/test` diagnostics for package, daemon, sudo, and service failures.

### Mandatory tests

- [X] **P21-TEST01** Ubuntu and Debian fixtures select the correct official package path and reject unsupported OS/architecture before download.
- [X] **P21-TEST02** Missing sudo/sudoers/TTY fixtures stop before any network, clone, package, or Tencent state change.
- [X] **P21-TEST03** Successful sudo preflight allows the one-command bootstrap to continue and records no password/token.
- [X] **P21-TEST04** Existing Docker, stopped Docker, and current Docker fixtures are repaired idempotently.
- [X] **P21-TEST05** Package/install failure rolls back only Baron-owned partial state and prints an actionable recovery command.
- [X] **P21-TEST06** Reboot/start fixture proves Docker and Tencent containers restart without requiring manual Tencent Panel interaction.

## P22 — Full Tencent deployment bootstrap `[ ]`

**Goal:** Turn `baron tencent-memory init` into the one-command Tencent
bootstrap for MemoryCore, MemoryHub/Panel, Proxy, Knowledge Service, and their
required configuration.  
**Primary files:** `internal/install/tencent.go`, deployment manifests/assets,
`internal/app/`, `internal/doctor/`, `configs/compatibility.json`.  
**Gate:** a clean supported Ubuntu/Debian machine reaches healthy memory and
knowledge endpoints without manual Tencent registration.

### Implementation tasks

- [X] **P22-T01** Resolve the latest compatible Tencent deployment release/commit/image digest, record it, and refuse unreviewed incompatible moving targets.
- [X] **P22-T02** Extend the managed deployment from Core/Hub/Proxy to the selected Knowledge Service/Panel/combined service path and record each endpoint/port.
- [X] **P22-T03** Generate/preserve `.env` safely, configure local endpoints, service ID, data directories, and required LLM/proxy binding without logging secrets.
- [X] **P22-T04** Pull and start the complete service set with bounded output, deterministic container names, volumes, health checks, and no duplicate stack.
- [X] **P22-T05** Verify Core, MemoryHub/Panel, Proxy, Knowledge Service, CodeGraph, Wiki, and tools-discovery health separately and distinguish missing/stopped/unhealthy.
- [X] **P22-T06** Read the managed admin key in-process, create/reuse Baron user/key/team, verify user auth, and provision no legacy/default entities.
- [X] **P22-T07** Configure restart policy/system service behavior and ensure service data directories survive Docker or host reboot.
- [X] **P22-T08** Implement repair/update/rollback for the complete stack, preserving existing Tencent data and reporting exact version/image changes.

### Mandatory tests

- [ ] **P22-TEST01** Clean Ubuntu/Debian run of `baron tencent-memory init` installs/starts all required services with no Panel steps.
- [ ] **P22-TEST02** All memory and knowledge health endpoints are green and report their resolved compatible versions/digests.
- [ ] **P22-TEST03** `.env`, admin key, user key, container output, and diagnostics contain no raw credentials.
- [X] **P22-TEST04** Five repeated inits/repairs produce one managed stack and one Baron identity set; five live init runs all completed and the before/after managed metadata snapshot remained stable at 1 user, 2 existing keys, and 1 team.
- [ ] **P22-TEST05** Stopping each service produces a specific diagnostic and repair restores it without recreating legacy data.
- [ ] **P22-TEST06** Reboot/start scenario returns all services and preserves memory/knowledge asset metadata.
- [ ] **P22-TEST07** Version update failure restores the previous deployment/version and keeps project bindings valid.

## P23 — Full Tencent API and knowledge client `[X]`

**Goal:** Expose the full Tencent memory, knowledge, skill, metadata, Wiki, and
CodeGraph capability through narrow Go interfaces instead of ad hoc HTTP calls.  
**Primary files:** `internal/memory/tencent/`, new focused memory/knowledge
client files, contracts, HTTP fixtures, compatibility metadata.  
**Gate:** every enabled endpoint has isolation, response-envelope, redaction,
timeout, and fixture coverage.

### Implementation tasks

- [X] **P23-T01** Define typed Go interfaces and request/response models for MemoryCore, Knowledge Service, Skill Memory, and metadata assets while keeping adapters independent of raw Tencent JSON.
- [X] **P23-T02** Complete v3 L0/L1/L2/L3 operations: conversation add/query/search/delete/count, atomic query/search/update/delete/count, scenario list/read/write/remove/count, and core read/write/count.
- [X] **P23-T03** Implement `/v3/knowledge/*` metadata create/get/update/delete/list for Wiki and CodeGraph assets with service URL, team, user, branch, and repository ownership.
- [X] **P23-T04** Implement Wiki create/list/get/delete, raw source list/read/write/remove, ingest, page list/read/write/remove, graph, and search operations.
- [X] **P23-T05** Implement CodeGraph create/list/get/sync/delete/status/files/search/explore/node/callers/callees/impact operations and asynchronous status polling.
- [X] **P23-T06** Implement Skill Memory create/search/version/resource/extraction paths and map shared project skills without overwriting user-owned Codex skills.
- [X] **P23-T07** Implement user/team/agent/task/asset/membership/access metadata calls needed for provisioning, ownership, and cleanup.
- [X] **P23-T08** Implement Knowledge Service tools discovery/call with allowlisted tool names, bounded arguments, and historical-result labeling.
- [X] **P23-T09** Centralize v3 headers/body isolation, service ID, session ID, bearer/user key selection, response code handling, timeout, retry, and redaction behavior.
- [X] **P23-T10** Add OpenAPI-backed fixture compatibility tests and version capability discovery so unsupported Tencent features fail clearly rather than silently degrading.

### Mandatory tests

- [X] **P23-TEST01** Each L0-L3 operation round-trips through a strict isolated HTTP fixture.
- [X] **P23-TEST02** Wiki lifecycle creates, ingests, searches, reads, updates, and removes a project Wiki asset.
- [X] **P23-TEST03** CodeGraph lifecycle registers, indexes, syncs, searches, explores, and returns impact/caller/callee results.
- [X] **P23-TEST04** Skill lifecycle preserves versions/resources and rejects cross-team access.
- [X] **P23-TEST05** Metadata fixture provisions task/asset/access relationships without ambiguous project binding.
- [X] **P23-TEST06** Missing/invalid isolation fields fail before network or return classified 4xx without leakage.
- [X] **P23-TEST07** Knowledge service outage leaves local continuity usable and queues the correct typed operation.
- [X] **P23-TEST08** Unsupported endpoint/version produces actionable `baron doctor` output and never corrupts state.

## P24 — Automatic project knowledge provisioning `[X]`

**Goal:** Make `baron setup` automatically provision all Tencent memory/knowledge
assets for the current project.  
**Primary files:** `internal/project/`, `internal/app/`, `internal/config/`,
new knowledge registry/state tables, setup/repair tests.  
**Gate:** a project needs no manual Tencent Panel registration or asset ID entry.

### Implementation tasks

- [X] **P24-T01** Add a stable local project knowledge registry for `code_graph_id`, `wiki_id`, service URL, branch, repository identity, last sync commit, ingest state, and ownership.
- [X] **P24-T02** Define remote repository policy: use a verified Git remote/branch when available, and provide a safe local-only fallback when a project has no remote URL.
- [X] **P24-T03** Register/reuse CodeGraph by project ID/repository/branch and reject ambiguous same-name assets.
- [X] **P24-T04** Create/reuse a project Wiki and seed bounded sources such as README, docs, architecture/design, ADR, and changelog files without uploading credentials or arbitrary ignored files.
- [X] **P24-T05** Queue Wiki ingest and CodeGraph indexing/sync, poll readiness, and report pending/failed/ready state without blocking local setup longer than the configured budget.
- [X] **P24-T06** Make setup/repair reconstruct missing local asset IDs from Tencent metadata and repair only Baron-owned assets.
- [X] **P24-T07** Enforce per-project/team/agent ownership for all assets and preserve a project’s memory/knowledge when the directory moves or is cloned.

### Mandatory tests

- [X] **P24-TEST01** `baron setup` on a clean remote project creates/reuses exactly one agent, CodeGraph, Wiki, and registry mapping.
- [X] **P24-TEST02** Rerun setup five times without duplicate knowledge assets or Wiki pages.
- [X] **P24-TEST03** A Git branch/commit change triggers the correct CodeGraph sync and records the new commit/status.
- [X] **P24-TEST04** Wiki seed excludes `.env`, secrets, ignored runtime files, and hidden credentials.
- [X] **P24-TEST05** Removing local knowledge registry state reconstructs the same remote assets, not duplicates.
- [X] **P24-TEST06** Two projects with identical names/basenames receive isolated agent, Wiki, CodeGraph, and memory assets.

## P25 — Full context orchestration for DSH and Codex `[X]`

**Goal:** Automatically compile a bounded context packet from local continuity,
Tencent memory, Wiki, CodeGraph, Skill Memory, Git, and test evidence at the
right lifecycle points.  
**Primary files:** `internal/continuity/`, `internal/hooks/`, `internal/recovery/`,
`adapters/dsh/`, Codex hook integration, context tests.  
**Gate:** both agents receive useful, bounded, evidence-labelled context without
being flooded or given permissions by memory.

### Implementation tasks

- [X] **P25-T01** Define context layers, source labels, trust/freshness labels, precedence, maximum characters/tokens, and historical-reference boundaries.
- [X] **P25-T02** At session start, load L3/L2 profile/scenario, last checkpoint, interruption status, relevant Wiki pages, and CodeGraph project status.
- [X] **P25-T03** At user prompt/pre-step, derive search terms from goal, prompt, files, symbols, errors, tests, and Git branch; query bounded memory/knowledge results.
- [X] **P25-T04** After tools/tests, record changed files, commands, exit codes, diagnostics, symbol impact, and relevant memory/knowledge citations.
- [X] **P25-T05** At checkpoint/stop/final/flush, update scenario/core summaries, next action, completion verification, and queued sync operations.
- [X] **P25-T06** Use CodeGraph callers/callees/impact and Wiki graph/search only when relevant to the current task; never inject an entire repository or knowledge base.
- [X] **P25-T07** Keep DSH and Codex adapters symmetric on canonical events while preserving each agent’s independent model/provider/skills.
- [X] **P25-T08** Make all remote/context failures fail open to local continuity with diagnostics and no false completion claim.

### Mandatory tests

- [X] **P25-TEST01** Session start returns a bounded packet with current local state, remote memory, Wiki, and CodeGraph citations.
- [X] **P25-TEST02** A symbol-change task receives only relevant callers/callees/impact/files rather than the full graph.
- [X] **P25-TEST03** DSH and Codex receive equivalent canonical state while retaining independent skill/config surfaces.
- [X] **P25-TEST04** Stale/contested memory is labeled historical and cannot override newer Git/test evidence.
- [X] **P25-TEST05** Context over-limit results are truncated by priority and remain valid JSON/agent hook output.
- [X] **P25-TEST06** Tencent/Knowledge outage still delivers local checkpoint/recovery context within hook timeout.

## P26 — Full reliability, security, migration, and operations `[X]`

**Goal:** Make the expanded Tencent/Knowledge integration safe under outages,
concurrency, updates, stale indexes, secrets, and machine migration.  
**Primary files:** `internal/storage/`, `internal/continuity/`, `internal/recovery/`,
`internal/app/backup.go`, `internal/doctor/`, `SECURITY.md`.  
**Gate:** every remote operation is durable, idempotent, isolated, bounded, and
repairable without a daemon.

### Implementation tasks

- [X] **P26-T01** Add typed queue operations for memory capture, core/scenario update, Wiki ingest, CodeGraph sync, Skill update, and metadata repair.
- [X] **P26-T02** Add operation-specific retry/backoff/dead-letter diagnostics, receipt IDs, and bounded flush budgets.
- [X] **P26-T03** Track freshness, Git commit, Wiki ingest version, CodeGraph status, memory timestamps, conflicts, and supersession without silently overwriting newer truth.
- [X] **P26-T04** Extend secret redaction/trust boundaries to Wiki raw sources, CodeGraph metadata, Skill resources, tool output, Docker logs, backups, and exports.
- [X] **P26-T05** Extend backup/restore to knowledge registry, Wiki/CodeGraph asset metadata, sync queue, and Tencent deployment data policy without plaintext keys.
- [X] **P26-T06** Expose operational status for Docker, services, asset readiness, queue depth, stale indexes, last successful sync, and next repair action.
- [X] **P26-T07** Add concurrency/fault tests for simultaneous DSH/Codex events, Wiki ingest, CodeGraph sync, service restart, power loss, and update rollback.

### Mandatory tests

- [X] **P26-TEST01** Queue outage operations and recover them exactly once after memory/knowledge services return.
- [X] **P26-TEST02** Simultaneous CodeGraph sync and agent checkpoint leaves both durable and correctly ordered.
- [X] **P26-TEST03** Stale CodeGraph/Wiki status is reported and never presented as current verified source evidence.
- [X] **P26-TEST04** Full expanded secret corpus scan finds no raw admin/user/provider credentials in state, logs, assets, or backups.
- [X] **P26-TEST05** Backup/restore reconstructs knowledge asset mappings without duplicate remote assets or plaintext credentials.
- [X] **P26-TEST06** Service restart, abrupt hook kill, and update failure leave local continuity and queue recoverable.

## P27 — Cross-platform release, documentation, and final Nexus acceptance `[ ]`

**Goal:** Ship the invisible, one-command Ubuntu/Debian experience and the
truthful Windows guidance with complete Tencent knowledge functionality.  
**Primary files:** `README.md`, `SECURITY.md`, `docs/`, `.github/workflows/`,
release scripts/installers, final acceptance report.  
**Gate:** full Nexus release only after Linux auto-bootstrap, Tencent full-stack,
DSH/Codex handoff, isolation, recovery, and Windows guidance all have evidence.

### Implementation tasks

- [X] **P27-T01** Update Linux/Windows quick start so the user-facing sequence is `./install.sh` followed by `baron install`, while individual init commands remain available for repair.
- [X] **P27-T02** Ship release artifacts/installers with Linux bootstrap prerequisites, Windows guidance, version/digest manifest, checksum, SBOM, and rollback evidence.
- [ ] **P27-T03** Run supported Ubuntu/Debian matrix: missing sudo, fresh Docker install, existing Docker, stopped daemon, reboot, rerun, repair, and uninstall-safe behavior.
- [ ] **P27-T04** Run real DSH/Codex/Tencent end-to-end scenarios with memory, Wiki, CodeGraph, Skill, checkpoints, recovery, and cross-agent handoff.
- [ ] **P27-T05** Run Windows release/guidance scenario and prove no false claim that Docker/WSL/Tencent were auto-installed.
- [X] **P27-T06** Generate the final evidence report with candidate SHA, component versions/digests, phase checklist, blockers, artifacts, and release decision.
- [X] **P27-T07** Publish only when all mandatory Nexus gates are checked; otherwise leave the final status explicitly `BLOCKED` with the exact missing dependency/evidence.
- [X] **P27-T08** Make `baron install` run the ordered host-preflight, DSH, Codex, Tencent, and current-project setup coordinator with idempotent delegated steps.
- [X] **P27-T09** Require native sudo authorization before the first release download and never read or persist the sudo password.
- [X] **P27-T10** Reuse official/managed DSH and Tencent credentials and suppress the Codex login notice once global Codex auth is ready.

### Mandatory tests

- [ ] **P27-TEST01** One-command Ubuntu/Debian bootstrap passes from a clean supported machine after the user grants sudo.
- [X] **P27-TEST02** A user without sudo is stopped before download and receives the exact remediation instruction.
- [X] **P27-TEST03** The complete user command sequence provisions Tencent memory/knowledge and a project without manual Panel operations.
- [ ] **P27-TEST04** CodeGraph/Wiki/Skill retrieval is bounded, isolated, cited, and available to both DSH and Codex.
- [ ] **P27-TEST05** Cross-agent unfinished work, network loss, service restart, and power-loss recovery pass on a real release candidate.
- [X] **P27-TEST06** Windows instructions are accurate, explicit, and do not claim unsupported automation.
- [X] **P27-TEST07** Final release artifacts, checksums, SBOM, rollback, docs, and acceptance report all agree on the same candidate revision.

## P28 — Validated credentials and complete Ubuntu/Debian first install `[ ]`

**Goal:** Make first-run Linux provisioning genuinely self-contained for
Ubuntu/Debian, while validating provider credentials before they are persisted
or copied into Tencent runtime configuration. Keep the explicit visible
DeepSeek-key entry policy, protected admin-key handling, bounded sudo
reauthorization, and the Windows manual boundary.
**Primary files:** `install.sh`, `internal/install/host.go`,
`internal/credentials/`, `internal/app/`, `internal/doctor/`, `README.md`.
**Gate:** local implementation and fixture evidence may be checked here, but
the phase stays unchecked until a clean supported machine and a real provider
credential rotation have been exercised.

### Implementation tasks

- [X] **P28-T01** Record the approved credential and host-bootstrap design and execution plan with explicit security and external-acceptance boundaries.
- [X] **P28-T02** Add a visible DeepSeek provider-key prompt that uses native terminal echo, warns the user, trims input, and never copies the value into Baron output or state.
- [X] **P28-T03** Add a bounded OpenAI-compatible `/models` validator with local weak-key rejection and separate invalid-versus-provider-unavailable classifications.
- [X] **P28-T04** Make DSH initialization validate existing, inherited, and newly entered provider credentials before reuse or persistence, with bounded replacement attempts and no overwrite on failure.
- [X] **P28-T05** Validate the Tencent runtime provider key before deployment and add atomic, protected, backup-aware provider-key rotation that preserves unrelated managed values.
- [X] **P28-T06** Add read-only readiness reporting for provider rejection/outage and expose `baron credentials set deepseek` for explicit one-time rotation.
- [X] **P28-T07** Add bounded native sudo reauthorization/retry to Go-managed system operations, without accepting or storing the sudo password.
- [X] **P28-T08** Add Ubuntu/Debian host bootstrap for Docker Engine/Compose, supported Node/npm/npx, latest pnpm, and checksum-verified uv/uvx before release/Tencent downloads.
- [X] **P28-T09** Extend `./install.sh` with the same sudo-first Ubuntu/Debian dependency bootstrap and retain explicit Windows prerequisite guidance.
- [X] **P28-T10** Document first-run credentials, visible-key behavior, rotation, sudo expiry, dependency bootstrap, environment overrides, and the remaining live acceptance gates.

### Mandatory tests

- [X] **P28-TEST01** Provider validator and visible-prompt fixtures cover accepted keys, weak keys, 400/401/403 rejection, 429/5xx/network outage, Authorization forwarding, and secret-safe errors.
- [X] **P28-TEST02** DSH replacement and Tencent rotation fixtures prove rejected/outage candidates do not overwrite existing stores and successful rotation is atomic/permission-protected.
- [X] **P28-TEST03** Host-bootstrap fixtures prove unsupported distributions stop before work, sudo preflight precedes downloads, sudo reauth is bounded, and uv checksum mismatch is rejected.
- [X] **P28-TEST04** Full local Go tests, vet/format/diff checks, shell syntax, installer preflight, and platform-guidance checks pass for the implementation.
- [ ] **P28-TEST05** A clean Ubuntu/Debian machine runs `./install.sh` and `baron install` after one sudo authorization, with missing dependencies installed and no manual package steps.
- [ ] **P28-TEST06** A real provider-backed run proves valid-key reuse, wrong-key rejection, replacement, and network-outage no-overwrite behavior on the published release.

## Current evidence ledger

This ledger records why a task is checked and why the remaining external gates
are intentionally left unchecked.

### Fresh verification — 2026-08-26 credential and host bootstrap

- Current-source local verification passed: `GOTOOLCHAIN=local
  /usr/local/go/bin/go test -count=1 ./...`, `go vet ./...`, `gofmt -l .`,
  `git diff --check`, `sh -n install.sh`, installer-preflight, platform
  guidance, branding, release-gate fixture, and the executable Bash DSH
  adapter test.
- The full race suite passed in `golang:1.27-bookworm` with GCC:
  `GOTOOLCHAIN=local /usr/local/go/bin/go test -race -count=1 ./...`. The
  host itself still has no `gcc`; the container is the recorded race
  environment.
- A temporary binary built directly from this worktree reports `baron 0.1.1`.
  Its live `baron test --json` reached Docker, Node/npm/npx, pnpm, uv/uvx,
  DSH, Codex, DSH provider validation, and all Tencent health/knowledge
  surfaces; it returned `ready:false`, exit `11`, only because sudo was not
  cached in the agent terminal. No key value appeared in the diagnostic.
- A current-source `baron install` probe stopped at native sudo preflight before
  the release download when this terminal could not authenticate. This proves
  ordering, not clean-machine bootstrap. The public release-gate script still
  correctly exits `21` while the older P19/P22/P27 external gates remain open.
- The official uv latest Linux archive/checksum path was downloaded and the
  archive contained regular `uv` and `uvx` binaries; checksum verification is
  required before installation.

### Fresh verification — 2026-08-25

- Full local verification passed from the repository root: `GOTOOLCHAIN=local /usr/local/go/bin/go test -count=1 ./...`, `GOTOOLCHAIN=local /usr/local/go/bin/go vet ./...`, `GOTOOLCHAIN=local CGO_ENABLED=0 /usr/local/go/bin/go test -count=1 ./...`, `gofmt -l .`, `git diff --check`, no-`target/`, shell syntax, branding, platform guidance, DSH adapter, and release-gate fixture checks.
- The direct DSH adapter test initially returned `126` because its shebang file had mode `0664`; the root cause was the untracked test script's missing executable bit, not adapter behavior. The mode is now `0755`, and direct execution passes. No production code was changed for this harness-only defect.
- A fresh CGO race attempt was made. `go test -race` correctly required cgo, and `CGO_ENABLED=1 go test -race ./...` stopped at the host boundary with `gcc not found`; this remains an unchecked race/environment gate rather than a claimed pass.
- A fresh native `0.1.1` release was rebuilt in `/tmp/baron-release-0.1.1.fixed.gOuFUD` with Go 1.27.0, CGO disabled, Linux amd64 and Windows amd64 artifacts, installers, manifest, module SBOM, toolchain metadata, and verified `SHA256SUMS`. The public `v0.1.1` release now serves the fixed assets; the release gate remains `BLOCKED` for incomplete P19/P22/P27 evidence.
- The public manifest, checksum list, and Linux binary were downloaded from
  GitHub and verified. The real `/home/ty/project-test` `baron install` run
  passed release download with the fixed binary and then stopped at the
  expected interactive sudo boundary; a real `baron update` reported version
  `0.1.1` already current. The remaining acceptance is clean-machine/first-
  installer and runtime evidence, not a release HTTP or three-second timeout.
- Live health checks returned HTTP `200` for MemoryCore `8420`, MemoryHub `8125`, Knowledge `8424`, and Proxy `8096`; the upstream Knowledge compose contract passes with its documented `docker/env.example`. The current agent terminal cannot access the Docker socket, and both requested non-interactive sudo checks require interactive authentication, so clean-machine bootstrap, restart/volume, and container inspection remain unchecked.
- A fresh `baron tencent-memory init` rerun exited `0` without a sudo prompt because its health-first idempotence saw the already-healthy four-endpoint stack. This proves safe rerun behavior only; it does not prove clean-machine Docker bootstrap and does not close the P22/P27 bootstrap gates.
- A live disposable non-Git project reused one Tencent Agent/Wiki binding; setup rerun reached Wiki `ready`, CodeGraph `local_only` for the absent Git remote, and zero pending/dead-letter queue items. A separate live Git-backed disposable project created a CodeGraph with honest `processing`/`pending` state after Tencent returned `409 busy`; it was not marked ready.
- Fresh DSH `headless` returned the exact probe sentinel and `dsh web --no-open` emitted a startup marker before the bounded probe timeout. Fresh Codex CLI `exec` returned its exact probe sentinel and left the Baron checkpoint `clean_closed`; these probes do not replace the still-unchecked clean-machine, Windows, full web-workspace, and first-installer gates.
- A health-first repair regression was added after a real `baron repair` hit sudo before flushing a healthy stack. The rebuilt binary now skips Docker bootstrap/deployment when all four health endpoints are green; the current Wiki remains honestly `processing`/local `pending` with ten raw seed entries.
- The fresh readiness report is intentionally not all-green in this terminal: `baron test --json` reports `ready=false`, `exit_code=12`, with Docker daemon unavailable and sudo incomplete while the user-level DSH/Codex/Tencent health checks are ready. No blocked phase checkbox was changed to `[X]` from this run.
- Disposable `/home/ty/project-test` acceptance evidence: real DSH, Codex, and Tencent initialization, `baron setup`, `baron repair`, status reconciliation, and a real Codex CLI sentinel all passed. `baron test --json` still honestly reports exit `12` in the agent terminal because Docker socket access and cached sudo are unavailable; Wiki is `ready`, the local queue is empty, CodeGraph is `local_only` without a verified Git remote, and the eight Baron Codex hooks are migrated to the supported three-second timeout.

- Local Go evidence: `GOTOOLCHAIN=local /usr/local/go/bin/go test ./...`, `go vet ./...`, `gofmt -l`, and `git diff --check` pass after the current implementation changes. The reliability fixtures also prove stale queue-lease recovery after an interrupted process, knowledge-aware backup/restore without duplicate remote assets, and an expanded admin/user/provider secret corpus scan across continuity artifacts.
- Local completion evidence: Codex hook inspection separates installed JSON entries from the unprovable interactive trust decision and prints the exact approval action; a pinned `@openai/codex@0.149.0` installer fixture passes, and a real host `codex-cli 0.149.0` smoke reuses the existing binary without npm before materializing the explicit adapter, hooks, and receipts; newly created Tencent user/key/team metadata has opaque-ID-only reverse-order compensation; a real child-process SIGKILL after queue claim recovers to one delivery/receipt; conflicting restore requires `--replace-existing` and retains the prior state as a sibling backup; the PowerShell installer applies explicit Windows ACL handling; and the release-gate script refuses publication while P19/P22/P27 remain incomplete.
- P4 live evidence: the pinned DSH init, bounded `dsh web --no-open` probe, profile repair, DuckDuckGo MCP smoke, and automated credential path pass. A no-key DSH request returns the upstream `MISSING_CREDENTIAL` result without mutating profile state; interactive Baron init collects and stores the key through DSH’s official credential file, while non-interactive init fails before installation with the exact environment remediation.
- P19 client-init evidence: the live DSH baseline reported DSH, DuckDuckGo, Superpowers, Reverse Skill, and Baron adapter readiness; the host Codex init preserved user-owned skills/config while installing the Baron adapter/hooks, and the user-provided authenticated Codex session confirmed trusted hooks; the user-provided Tencent init plus subsequent `baron test --json` confirmed the Baron identity/services were ready.
- Codex live-hook evidence: the implementation now resolves the official `$CODEX_HOME/hooks.json` path (default `~/.codex/hooks.json`) instead of the unused `~/.config/codex/hooks.json`; `baron codex-cli init` materialized eight Baron hooks there, and a real authenticated Codex v0.149.0 interactive PTY session ran `SessionStart` and `UserPromptSubmit` and returned `CODEX_INTERACTIVE_CANONICAL_HOOK_SEEN` from the seeded Baron memory context.
- DSH profile evidence: the installed real DSH v0.1.1-rc.2 `web` and
  `headless` profile dumps contained the mandatory Superpowers, Reverse Skill,
  DuckDuckGo MCP, and Baron adapter entries together; a real headless run
  returned `CODEX_TO_DSH_HEADLESS_SENTINEL_20260824` from local handoff
  context, and the real web server also started successfully. Selecting a
  workspace/session remains the external UI gate.
- Live cross-agent evidence: an official Codex payload with `tool_response` and
  `last_assistant_message` was normalized into canonical command/stderr/status/
  exit-code evidence. A controlled watcher killed Codex after its real exit-23
  tool result but before `assistant_final`/clean close; DSH recovered the
  durable checkpoint and reran the marker command with exit code 0. The reverse
  DSH `status=not_yet_verified` handoff was returned exactly by real Codex, and
  a live concurrent DSH/Codex run left SQLite integrity `ok`, zero duplicate
  idempotency keys, and zero pending/dead-letter queue items.
- Verified-completion ordering evidence: a real Codex lifecycle sequence marked
  a completion verified, emitted `session_clean_closed`, and was followed by a
  real DSH headless session. The DSH handoff contained no interruption packet,
  returned its exact live sentinel, and the final checkpoint remained
  `clean_closed` with `task_status=completed` and `completion_verified=true`.
- Live legacy-Knowledge evidence: current `baron setup` hit Tencent's nested-raw-source `ENOENT`; the bounded fallback retried with flat aliases, and live `wiki/raw/ls` confirmed all ten seed files uploaded without the path error. The client now batches the service's ten-file limit. A rebuilt real lifecycle hook drained the previously pending Wiki/core/scenario queue without manual SQLite edits (`pending_queue=0`, `dead_letter_queue=0`); Wiki and ingest are `ready`, while an unseeded L2 Scenario update safely falls back to L0. The repository has no verified Git remote, so CodeGraph correctly remains `local_only` rather than being marked ready.
- Live Tencent payload-boundary evidence: real DSH tool output exposed Tencent's `messages.0.content <= 8192` limit and created six bounded memory dead-letters. The client now truncates outbound conversation content by the provider's UTF-16 limit, and lifecycle flush automatically requeues only that exact historical size-error class. A rebuilt real hook replayed all six without manual database edits; the current project reports `0 pending` and `0 dead-letter` with durable delivered receipts.
- P22 acquisition/live evidence: the official Tencent repository is detached at `97f94654280b2932c35ba4806a491999ed244cc9`; the four local service health endpoints returned HTTP 200, and live `baron setup` created/reused a Tencent Agent and Wiki. The first live project-agent request exposed a missing `owner_user_id`; the payload is now corrected and the rerun succeeds. A real CodeGraph status call then exposed a second upstream contract mismatch: `/v3/code-graph/status` rejects shared identity fields, so Baron now sends only `code_graph_id` on that narrow route; the live status probe is green and an actual small Git repository remains truthfully `pending` while Tencent indexes it. Five repeated live inits completed with stable managed identity counts; clean-machine init and service restart/volume checks remain open.
- Linux bootstrap evidence: Ubuntu/Debian package-path, sudo-before-network, idempotent Docker repair, partial apt-state cleanup, restart-policy, and Windows guidance fixtures pass. Docker Engine/Compose were installed and `hello-world` was run successfully on the development host; the current user is not silently added to the root-equivalent `docker` group.
- Linux no-sudo evidence: the bootstrap fixture stops before any download or apt operation and emits the exact `sudo -v` remediation instruction.
- Tencent fixture evidence: strict v3 isolation, redaction, metadata ownership, complete L0-L3/Knowledge/Skill/management routes, project Wiki/CodeGraph create/reuse, bounded seed collection, readiness polling, freshness/supersession, separate Wiki/CodeGraph/tools diagnostics, immutable deployment manifests with image digests, pinned update rollback, typed queue outage recovery, dead-letter preservation, and delivery receipts pass locally.
- Continuity/recovery evidence: symbol-scoped callers/callees/impact retrieval, DSH/Codex canonical-state symmetry, five-run knowledge provisioning, registry-loss reconstruction, same-name project isolation, knowledge-aware backup/restore, stale queue-lease recovery after an abrupt process exit, expanded secret corpus scanning, and concurrent CodeGraph-sync/checkpoint durability pass in local fixtures.
- Restore hardening evidence: staged restore now runs Tencent deployment/health/identity/project-agent verification before installing the restored local state; a find-only agent lookup prevents restore from creating duplicate remote agents, with targeted and full Go tests green.
- Live isolation evidence: ten disposable projects were provisioned against the live Tencent stack with 10 unique project IDs and 10 unique Agent IDs; a direct project-B memory query for project-A's sentinel returned zero cross-project sentinel matches (the initial hook probe was corrected to exclude prompt echo before this result was recorded).
- Live offline-queue evidence: a disposable live project captured a Codex hook while Tencent was pointed at an unavailable endpoint, retained one pending local item with the hook still exiting 0, then restored the endpoint and flushed to four delivered queue/memory receipts with four unique idempotency keys and zero pending/dead-letter items; replaying the original event remained a no-op.
- Process-fault evidence: the controlled child-process SIGKILL and abrupt-restart fixtures both passed; a task stayed `in_progress` with `completion_verified=false`, the stale queue lease was recovered, and the operation delivered exactly once with a durable receipt. The concurrent CodeGraph/checkpoint fixture also passed.
- Platform/release evidence: Linux/Windows quick-start wording and the PowerShell installer are checked so Windows remains manual for Docker Desktop, WSL2, Ubuntu, and Tencent UI; native Linux/Windows artifacts, SBOM, checksums, and rollback metadata build successfully. Public `v0.1.1` manifest/binary download and `baron update` were verified after fixing the inherited three-second timeout. The current acceptance report records the release directory, artifact hashes, candidate SHA, and BLOCKED release decision. `scripts/check-release-gate.sh` still refuses a full publication PASS with exit 21 while P19/P22/P27 remain incomplete, and the gate fixture passes without mutating the roadmap/report.
- External acceptance still pending: the user-provided Linux terminal now proves
  `baron test --json` with `ready:true` and `exit_code:0`; live Project A/B
  setup and no-Panel Tencent/project provisioning are checked above. The
  clean-machine `baron tencent-memory init` journey could not be rerun without
  a sudo ticket in the agent terminal. Real CodeGraph completion (the live
  status endpoint is reachable but the upstream indexing job is still
  pending), legacy
  snapshots, service restart/volume acceptance, Windows runtime, and the
  remaining P19/P22/P27 release gates remain `[ ]`.

### Unchecked-item blocker index

The remaining unchecked boxes are not skipped implementation work. They map to
these concrete external acceptance prerequisites:

- `P4-TEST06` is green under the revised automation contract: the no-key model
  request returns DSH's upstream `MISSING_CREDENTIAL` without profile damage,
  and Baron’s interactive init now collects the credential itself.
- `P6-TEST01..05`, `P15-TEST03`, and `P22-TEST01..07`: a provider-backed,
  authenticated Tencent stack with live Docker containers, persistent volumes,
  legacy metadata, restart, and service logs.
- `P16-TEST01..02` and `P18-TEST02`: Docker-volume migration and a
  Windows runtime/PowerShell/WSL acceptance host; the local safe restore and
  ACL implementation are checked separately.
- `P19-T01`, `P19-T14..T15`, `P19-TEST01`, `P19-TEST09..10`,
  `P19-TEST12`, `P27-T03..T05`,
  `P27-TEST01`, `P27-TEST04..05`: the clean-machine final journey, live
  cross-agent recovery matrix, Windows candidate, and immutable publication
  candidate. These cannot be replaced by local mocks or the dirty worktree.

## User-visible completion contract

When the roadmap is complete, the user should only need to understand this:

```bash
git clone https://github.com/thienty1207/Baron-Nexus.git
cd Baron-Nexus
./install.sh
cd /path/to/project
baron install

# daily use
dsh web
codex
```

On Ubuntu/Debian, `./install.sh` performs the verified first binary download
only after native sudo authorization. Then `baron install` performs the Docker
preflight, installs/repairs the full Tencent stack, starts the services,
provisions the Baron identity, registers project memory/knowledge, and runs
project setup. Missing provider/admin values are collected through hidden input
and written only to their supported protected stores; users do not edit `.env`
or Tencent metadata manually. Codex login is requested only when its global
auth is missing and is reused afterward. On Windows, it prints the required
Docker Desktop/WSL/Ubuntu/Tencent actions instead of pretending to automate UI
installation.

## Global final checks

- [X] Every checked implementation task has a corresponding code/doc/test artifact.
- [X] Every unchecked item has an explicit blocker or remaining implementation path.
- [X] No checkbox is checked solely because a mock exists when the task requires a live service/client.
- [X] No release is called PASS while any R1/R3/R5/R7/R9/R10 or platform acceptance gate is unchecked.
- [X] `baron test` remains read-only; `baron setup` remains the only required per-project provisioning command.
- [X] Existing user-owned DSH/Codex settings and skills remain preserve-first.
- [X] Tencent data is project/team/agent/user isolated, and Git/source/test evidence outranks stale memory.
- [X] Final report contains the actual candidate revision, artifacts, checksums, test output, and known limitations.

## Evidence references

- [IMPLEMENT.pdf](</home/ty/Baron dsh-codex-tencent/IMPLEMENT.pdf>)
- [Baron Engine predecessor](https://github.com/thienty1207/Baron-Engine)
- [Current implementation progress](</home/ty/Baron dsh-codex-tencent/docs/implementation/IMPLEMENT_PROGRESS.md>)
- [Current acceptance report](</home/ty/Baron dsh-codex-tencent/docs/implementation/FINAL_ACCEPTANCE_REPORT.md>)
