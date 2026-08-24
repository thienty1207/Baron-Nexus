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
baron deepseek-harness init
baron codex-cli init
baron tencent-memory init
baron test

cd /path/to/project
baron setup
```

After setup, DSH and Codex are used normally. Baron Nexus must automatically
handle local event capture, checkpointing, recovery, Tencent memory, Wiki,
CodeGraph, Skill Memory, project isolation, offline queueing, and cross-agent
handoff. The user must not need to open Tencent Panel or manually register a
project's memory assets.

## Platform policy

- Ubuntu/Debian Linux: `baron tencent-memory init` may install Docker Engine and
  Compose through the official package path, but it must check `sudo` before
  any network download. If sudo is unavailable or unauthorized, it must stop
  before creating partial state and print the exact repair instruction.
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

- Original `IMPLEMENT.pdf`: P0-P19, 20 phases, 205 implementation tasks (including `P18-T07B`).
- Baron Nexus expansion: P20-P27, 8 phases, 60 additional implementation
  tasks.
- Full roadmap: **28 phases, 265 implementation tasks**, plus the mandatory
  phase-test and final-acceptance checklists below.

## Current baseline ledger

| Phase | Gate | Current truth |
| --- | --- | --- |
| P0 | [X] | Baseline, contracts, Go repository, acceptance contract, and local tests complete. |
| P1 | [X] | CLI, configuration, atomic writes, backups, redaction, and exit codes complete. |
| P2 | [X] | SQLite WAL, local event journal, checkpoints, hooks, concurrency, and local fault tests complete. |
| P3 | [X] | Readiness probes and secret-safe diagnostics complete. |
| P4 | [ ] | DSH installer/profile/adapter code exists; real Node/DSH/uvx/MCP acceptance is blocked. |
| P5 | [ ] | Codex merge/fail-open code exists; installed Codex/auth/Desktop acceptance is blocked. |
| P6 | [ ] | Tencent v3 client and managed deployment code exist; Docker/Tencent live acceptance is blocked. |
| P7 | [X] | Project identity, setup, permissions, Git-ignore, move, and tamper tests complete locally. |
| P8 | [ ] | Project-agent/isolation implementation and fixtures exist; live Tencent namespace acceptance is blocked. |
| P9 | [X] | Local checkpoint/recovery engine and interruption/drift tests complete. |
| P10 | [ ] | Memory/recall/queue implementation and HTTP fixtures exist; live Tencent acceptance is blocked. |
| P11 | [ ] | Synthetic Codex hook implementation/tests exist; real Codex lifecycle acceptance is blocked. |
| P12 | [ ] | DSH adapter/profile bundle exists; real DSH lifecycle acceptance is blocked. |
| P13 | [ ] | Recovery/handoff implementation exists; real two-client handoff acceptance is blocked. |
| P14 | [ ] | Queue/concurrency implementation exists; race/SIGKILL/live outage acceptance is blocked. |
| P15 | [ ] | Isolation and tamper implementation/tests exist; legacy/live Tencent snapshot acceptance is blocked. |
| P16 | [ ] | Local backup/checksum/rollback implementation exists; Docker-volume and cross-OS acceptance is blocked. |
| P17 | [X] | Local security, permission, redaction, tamper, bounded-output, and vet evidence complete. |
| P18 | [ ] | Release artifacts and Linux smoke pass; Windows runtime and full installer/upgrade acceptance are blocked. |
| P19 | [ ] | Final report exists, but the full external release gate is not green. |
| P20-P27 | [ ] | Baron Nexus full Tencent/auto-bootstrap expansion is approved but not yet implemented. |

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

## P4 — DeepSeek Harness baseline initializer `[ ]` BLOCKED

**Goal:** Install and verify DSH, DuckDuckGo MCP, Superpowers, Reverse Skill,
and the Baron adapter while preserving user-owned DSH settings.  
**Primary files:** `internal/install/commands.go`, `internal/install/install.go`,
`internal/install/assets/`, `adapters/dsh/`, `configs/compatibility.json`.  
**Local implementation:** code and fixture tests exist. **Gate:** real DSH,
Node, pnpm, uvx, credentials, and MCP network smoke are unavailable.

### Implementation tasks

- [X] **P4-T01** Pin tested DSH `0.1.1-rc.2`, commit metadata, and unsupported-version diagnostics.
- [X] **P4-T02** Install/verify the official `@deepseek-ai/dsh` package through the supported npm mechanism.
- [ ] **P4-T03** Run a real safe DSH startup probe such as `dsh web --no-open` without requiring a browser.
- [X] **P4-T04** Require/verify pnpm only for the selected plugin workflow and report it separately from npm.
- [X] **P4-T05** Install pinned `superpowers-dsh` through the official DSH profile plugin mechanism.
- [X] **P4-T06** Install and verify the pinned immutable `dsh-reverse-skill` commit.
- [X] **P4-T07** Configure the pinned DuckDuckGo MCP package through uvx.
- [X] **P4-T08** Register stable `ddg-search` without overwriting unrelated user MCP rows.
- [X] **P4-T09** Materialize and install the Baron DSH adapter package/bundle.
- [X] **P4-T10** Merge only Baron-owned DSH fragments and preserve user model/provider/skill settings.
- [X] **P4-T11** Keep DeepSeek credentials in the supported DSH path and emit an actionable readiness message rather than persisting them in project state.
- [X] **P4-T12** Write installation receipts with versions, commits, sources, and checksums where available.

### Mandatory tests

- [ ] **P4-TEST01** Fresh Ubuntu with Node/uv produces runnable DSH and all four mandatory Baron baseline components.
- [X] **P4-TEST02** Local merge fixture proves rerun idempotence and custom DSH rows survive; real DSH confirmation remains pending.
- [ ] **P4-TEST03** Remove Superpowers from a real profile, rerun init, and prove repair.
- [ ] **P4-TEST04** Remove Reverse Skill from a real profile, rerun init, and prove repair.
- [ ] **P4-TEST05** Perform a real DuckDuckGo MCP search and classify network/rate-limit failure separately from install failure.
- [ ] **P4-TEST06** Start DSH without a DeepSeek key and prove it reports `ACTION REQUIRED` without corrupting config.

## P5 — Codex CLI initializer and hook capability baseline `[ ]` BLOCKED

**Goal:** Install/verify Codex, preserve user-owned skills/config, and register
officially shaped hooks.  
**Primary files:** `internal/install/install.go`, `internal/app/app.go`,
`internal/doctor/`, Codex hook tests.  
**Gate:** real Codex install/auth/Desktop acceptance is pending.

### Implementation tasks

- [X] **P5-T01** Pin/test Codex `0.149.0` and use the official package installation path.
- [X] **P5-T02** Verify `codex --version` and non-secret authentication readiness.
- [X] **P5-T03** Report the supported Codex sign-in action when unauthenticated without treating installation as failed.
- [X] **P5-T04** Merge the pinned official nested hook shape for session/prompt/tool/stop/end/compact events.
- [X] **P5-T05** Back up and merge only Baron-owned Codex hook entries.
- [X] **P5-T06** Never install DSH skills into Codex and preserve unrelated user skills/plugins.
- [ ] **P5-T07** Detect interactive Codex hook trust/enablement and provide the exact user approval instruction.
- [ ] **P5-T08** Run real Codex Desktop compatibility validation; otherwise publish a truthful CLI-only support boundary.

### Mandatory tests

- [ ] **P5-TEST01** Fresh fixture has the pinned Codex binary and version.
- [X] **P5-TEST02** Local custom-hook/skill fixture survives except for the intended Baron merge.
- [X] **P5-TEST03** Missing auth produces no secret output and an auth-incomplete readiness result.
- [X] **P5-TEST04** Synthetic Codex hook input reaches Baron and receives valid bounded JSON.

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
- [ ] **P6-T12** Add safe rollback for newly created Baron metadata when a later step fails; never delete pre-existing entities.

### Mandatory tests

- [ ] **P6-TEST01** Fresh Docker deployment creates exactly one Baron user, active key, team, and healthy services.
- [ ] **P6-TEST02** Five repeated inits produce no duplicate user/team/key explosion.
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
**Local implementation:** HTTP fixtures pass. **Gate:** live Tencent namespace
validation remains pending.

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
- [X] **P8-TEST04** Binding a project to an Agent belonging to another project fails before remote use. Live Tencent confirmation remains unchecked at the phase gate.

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

## P11 — Codex adapter and lifecycle hooks `[ ]` BLOCKED

**Goal:** Make normal Codex sessions automatically participate in shared memory
and continuity.  
**Primary files:** `internal/install/install.go`, `internal/hooks/`, `internal/app/`,
Codex hook tests.  
**Gate:** installed/authenticated Codex and Desktop execution are pending.

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
- [ ] **P11-T10** Detect and explain interactive Codex hook trust/enablement state.
- [ ] **P11-T11** Validate Codex Desktop lifecycle or publish a tested CLI-only boundary.

### Mandatory tests

- [ ] **P11-TEST01** Real interactive Codex Project A prompt receives a known memory sentinel.
- [X] **P11-TEST02** Synthetic Codex edit/failing-test payload records changed file, command, and failure evidence.
- [X] **P11-TEST03** Local fixture with Tencent disabled continues and grows `sync_queue`.
- [X] **P11-TEST04** Corrupting a non-critical runtime log leaves the hook fail-open and doctor-diagnosable.
- [X] **P11-TEST05** Local custom hook/skill fixture remains functional after merge; real Codex confirmation remains pending.

## P12 — DeepSeek Harness adapter and lifecycle hooks `[ ]` BLOCKED

**Goal:** Give DSH the same canonical project brain while retaining its own
model, plugins, and search baseline.  
**Primary files:** `adapters/dsh/`, `internal/install/assets/dsh-adapter/`,
DSH profile patch and compatibility metadata.  
**Gate:** real DSH runtime/auth/plugin acceptance is pending.

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

- [ ] **P12-TEST01** Real DSH receives a Codex-written memory sentinel.
- [ ] **P12-TEST02** Real DSH failing command produces a canonical checkpoint.
- [ ] **P12-TEST03** Real DSH profile discovers all mandatory plugins and Baron adapter together.
- [X] **P12-TEST04** Local outage fixture keeps DSH-side local continuity behavior available; real DSH confirmation remains pending.
- [X] **P12-TEST05** Unsupported-version compatibility fixture fails clearly without corrupting profile state.

## P13 — Cross-agent handoff and interrupted-work recovery `[ ]` BLOCKED

**Goal:** Deliver the core product promise: one agent takes over unfinished work
from the other using evidence rather than invented state.  
**Primary files:** `internal/recovery/`, `internal/continuity/`, `internal/hooks/`,
handoff receipts.  
**Gate:** real Codex↔DSH takeover remains pending.

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

- [ ] **P13-TEST01** Real Codex failing work is killed, DSH receives goal/files/failure/next action, reruns, and continues.
- [ ] **P13-TEST02** Real DSH untested work is handed to Codex as “not yet verified,” not an invented failure/pass.
- [ ] **P13-TEST03** Verified completion is not falsely classified as interrupted by the other agent.
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
- [ ] **P14-TEST03** SIGKILL a hook at controlled remote-delivery points and prove pending/delivered but never lost/duplicated semantics.
- [ ] **P14-TEST04** Run real DSH and Codex simultaneously with interleaved events; only local synthetic concurrency is currently proven.

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
- [ ] **P16-T05** Restore Tencent Docker data before project binding validation and verify user/team/project metadata.
- [X] **P16-T06** Reconstruct `.baron/.env` from `project.toml` plus restored/global mapping after reinstall.
- [X] **P16-T07** Preserve project ID when moving between Ubuntu and Windows.
- [X] **P16-T08** Document that uncommitted source bytes require Git/separate backup and cannot be recovered from memory alone.
- [ ] **P16-T09** Complete Windows filesystem/ACL and Docker Desktop/Engine path adaptation.

### Mandatory tests

- [ ] **P16-TEST01** Clean Ubuntu restore with Docker, clone, setup, and prior memory recall.
- [ ] **P16-TEST02** Ubuntu-to-Windows migration preserves project ID and Tencent Agent mapping.
- [X] **P16-TEST03** Corrupt backup checksum causes restore refusal before current-state mutation.
- [ ] **P16-TEST04** Existing conflicting Baron state stops restore until a safe mode is explicitly selected.

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
- [ ] **P18-T10** Enforce that a public release requires complete P19 acceptance evidence, not only local artifact smoke.

### Mandatory tests

- [X] **P18-TEST01** Release Linux artifact runs setup/status/hook smoke in an isolated project without a source checkout.
- [ ] **P18-TEST02** Fresh Windows artifact and installer run all subcommands; legacy collision produces safe migration guidance.
- [ ] **P18-TEST03** Upgrade from a previous release preserves project IDs and Tencent mappings.
- [X] **P18-TEST04** Local repair/update fixtures restore missing Baron-owned hook/profile components without unrelated changes.
- [X] **P18-TEST05** Injected migration failure restores the prior binary/config/database.
- [X] **P18-TEST06** Release audit proves no Rust/Cargo dependency, no `target/`, CGO policy compliance, and native artifact launch.

## P19 — Final full-system acceptance and release gate `[ ]` BLOCKED

**Goal:** Run the exact full user journey and resilience matrix; this is the
authority for the word “done.”  
**Primary files:** `docs/implementation/FINAL_ACCEPTANCE_REPORT.md`, release
artifacts, acceptance logs.  
**Gate:** every P19 test must pass; no waiver is allowed for isolation,
continuity, Tencent, DSH, or security contracts.

### Implementation tasks

- [ ] **P19-T01** Create a clean Ubuntu environment with Docker but no DSH/Codex/Tencent/Baron project state.
- [ ] **P19-T02** Run DSH init with a supported test credential and verify DSH, DuckDuckGo, Superpowers, Reverse Skill, and Baron adapter.
- [ ] **P19-T03** Run Codex init, complete supported authentication, and verify hooks while preserving custom skills.
- [ ] **P19-T04** Run Tencent init and verify Baron user/key/team plus all deployed services.
- [ ] **P19-T05** Run `baron test` and require all components green with exit 0.
- [ ] **P19-T06** Create Project A and verify its unique project/Tencent Agent mapping.
- [ ] **P19-T07** Create Project B and verify its isolated mapping.
- [ ] **P19-T08** Run a real Codex-to-DSH unfinished failing-test handoff.
- [ ] **P19-T09** Run a real DSH-to-Codex unfinished/unverified handoff.
- [ ] **P19-T10** Disable Tencent, accumulate queue, restore connectivity, and verify exact-once synchronization.
- [ ] **P19-T11** Kill processes at checkpoint boundaries and verify safe recovery.
- [ ] **P19-T12** Run concurrent real Codex/DSH same-project events and verify database/state integrity.
- [ ] **P19-T13** Run the 10-project isolation suite.
- [ ] **P19-T14** Run backup, clean reinstall, restore, clone, setup, and recall.
- [ ] **P19-T15** Repeat core acceptance on a Windows release candidate.
- [X] **P19-T16** Maintain a final acceptance report with candidate revision, versions, tests, artifacts, limitations, and release decision; the current report truthfully says BLOCKED.

### Mandatory tests

- [ ] **P19-TEST01** All three init commands plus `baron test` pass on clean Ubuntu.
- [ ] **P19-TEST02** DSH baseline is functional end-to-end.
- [ ] **P19-TEST03** Codex CLI auth/hooks are functional and custom skills survive.
- [ ] **P19-TEST04** Codex unfinished failing work is recovered and continued by DSH.
- [ ] **P19-TEST05** DSH unfinished unverified work is recovered and continued by Codex.
- [ ] **P19-TEST06** Power-loss simulation never falsely completes a task and leaves a usable durable checkpoint.
- [ ] **P19-TEST07** Network loss preserves local continuity and syncs the queue exactly once after recovery.
- [ ] **P19-TEST08** Ten-project isolation has zero cross-project memory leakage.
- [ ] **P19-TEST09** Backup/restore reconnects the same project ID and Tencent Agent on a clean OS.
- [ ] **P19-TEST10** Known secrets are absent from persisted state/Tencent exports and memory cannot escalate tool permissions.
- [X] **P19-TEST11** Local Go tests, vet, CGO-free release smoke, and supported release audit pass; race mode is recorded as blocked by missing gcc.
- [ ] **P19-TEST12** Ubuntu and Windows release candidates satisfy the same identity/memory contracts.

## P20 — Baron Nexus rebrand and compatibility lock `[ ]`

**Goal:** Rename the product/display identity to Baron Nexus without breaking
the existing `baron` command, project files, module compatibility, or user
workflow.  
**Primary files:** `README.md`, `SECURITY.md`, `docs/`, `configs/`, CLI help,
release manifest, receipts, acceptance/report files.  
**Gate:** all public text says Baron Nexus; technical command compatibility is
explicitly preserved.

### Implementation tasks

- [ ] **P20-T01** Replace user-facing `Baron Shared Brain` branding with `Baron Nexus` in README, docs, help text, receipts, reports, and release metadata.
- [ ] **P20-T02** Keep the executable and command path as `baron`; do not force users to migrate existing shell hooks or scripts to `nexus`.
- [ ] **P20-T03** Update compatibility/config schemas, acceptance/report headings, installation messages, and product descriptions to the Nexus name.
- [ ] **P20-T04** Add a rebrand regression scan that fails if the old public product name remains outside historical/spec provenance where it is intentionally required.
- [ ] **P20-T05** Run a clean binary/help/release smoke and record that branding changed while command behavior and project IDs did not.

### Mandatory tests

- [ ] **P20-TEST01** Repository public-text scan finds Baron Nexus branding and only approved historical references to the predecessor name.
- [ ] **P20-TEST02** `baron --help`, `baron test --json`, and hook output keep the existing machine-facing command/JSON contract.
- [ ] **P20-TEST03** Existing `.baron/project.toml`, global state, receipts, and project IDs survive the branding migration unchanged.

## P21 — Ubuntu/Debian Linux bootstrap and sudo preflight `[ ]`

**Goal:** Make `baron tencent-memory init` install Docker on supported Linux
without partial downloads or mid-run privilege surprises.  
**Primary files:** new Linux bootstrap package under `internal/install/`,
`internal/app/app.go`, `internal/doctor/`, package-manager fixtures, README
platform instructions.  
**Gate:** the command checks sudo before any network operation, installs only on
Ubuntu/Debian, and is safe to rerun.

### Implementation tasks

- [ ] **P21-T01** Detect Linux, Ubuntu/Debian release identity, architecture, interactive terminal availability, package manager, and unsupported distributions before touching the network.
- [ ] **P21-T02** Run a non-destructive sudo preflight at the very beginning; if sudo is missing, unauthorized, or cannot authenticate, stop with the exact `sudo -v`/administrator instruction and leave no downloaded state.
- [ ] **P21-T03** Build an official Docker apt-repository installation plan for Ubuntu/Debian with distro/architecture validation, avoiding arbitrary `curl | sh` installers.
- [ ] **P21-T04** Install Docker Engine, CLI, containerd, Buildx, and Compose plugin through the validated package path using sudo only for system operations.
- [ ] **P21-T05** Enable/start Docker service, verify daemon readiness, and handle an already-installed/stopped/outdated Docker installation without duplicate packages.
- [ ] **P21-T06** Keep downloaded source/config user-owned, avoid silently adding the user to the root-equivalent `docker` group, and clearly report any required logout/re-login if a supported permission change is selected.
- [ ] **P21-T07** Add system/container restart behavior so Tencent services return after Docker starts/reboots; preserve service logs and avoid starting duplicate stacks.
- [ ] **P21-T08** Make interrupted installation resumable/repairable, clean only Baron-owned partial state, and expose `baron doctor/test` diagnostics for package, daemon, sudo, and service failures.

### Mandatory tests

- [ ] **P21-TEST01** Ubuntu and Debian fixtures select the correct official package path and reject unsupported OS/architecture before download.
- [ ] **P21-TEST02** Missing sudo/sudoers/TTY fixtures stop before any network, clone, package, or Tencent state change.
- [ ] **P21-TEST03** Successful sudo preflight allows the one-command bootstrap to continue and records no password/token.
- [ ] **P21-TEST04** Existing Docker, stopped Docker, and current Docker fixtures are repaired idempotently.
- [ ] **P21-TEST05** Package/install failure rolls back only Baron-owned partial state and prints an actionable recovery command.
- [ ] **P21-TEST06** Reboot/start fixture proves Docker and Tencent containers restart without requiring manual Tencent Panel interaction.

## P22 — Full Tencent deployment bootstrap `[ ]`

**Goal:** Turn `baron tencent-memory init` into the one-command Tencent
bootstrap for MemoryCore, MemoryHub/Panel, Proxy, Knowledge Service, and their
required configuration.  
**Primary files:** `internal/install/tencent.go`, deployment manifests/assets,
`internal/app/`, `internal/doctor/`, `configs/compatibility.json`.  
**Gate:** a clean supported Ubuntu/Debian machine reaches healthy memory and
knowledge endpoints without manual Tencent registration.

### Implementation tasks

- [ ] **P22-T01** Resolve the latest compatible Tencent deployment release/commit/image digest, record it, and refuse unreviewed incompatible moving targets.
- [ ] **P22-T02** Extend the managed deployment from Core/Hub/Proxy to the selected Knowledge Service/Panel/combined service path and record each endpoint/port.
- [ ] **P22-T03** Generate/preserve `.env` safely, configure local endpoints, service ID, data directories, and required LLM/proxy binding without logging secrets.
- [ ] **P22-T04** Pull and start the complete service set with bounded output, deterministic container names, volumes, health checks, and no duplicate stack.
- [ ] **P22-T05** Verify Core, MemoryHub/Panel, Proxy, Knowledge Service, CodeGraph, Wiki, and tools-discovery health separately and distinguish missing/stopped/unhealthy.
- [ ] **P22-T06** Read the managed admin key in-process, create/reuse Baron user/key/team, verify user auth, and provision no legacy/default entities.
- [ ] **P22-T07** Configure restart policy/system service behavior and ensure service data directories survive Docker or host reboot.
- [ ] **P22-T08** Implement repair/update/rollback for the complete stack, preserving existing Tencent data and reporting exact version/image changes.

### Mandatory tests

- [ ] **P22-TEST01** Clean Ubuntu/Debian run of `baron tencent-memory init` installs/starts all required services with no Panel steps.
- [ ] **P22-TEST02** All memory and knowledge health endpoints are green and report their resolved compatible versions/digests.
- [ ] **P22-TEST03** `.env`, admin key, user key, container output, and diagnostics contain no raw credentials.
- [ ] **P22-TEST04** Five repeated inits/repairs produce one managed stack and one Baron identity set.
- [ ] **P22-TEST05** Stopping each service produces a specific diagnostic and repair restores it without recreating legacy data.
- [ ] **P22-TEST06** Reboot/start scenario returns all services and preserves memory/knowledge asset metadata.
- [ ] **P22-TEST07** Version update failure restores the previous deployment/version and keeps project bindings valid.

## P23 — Full Tencent API and knowledge client `[ ]`

**Goal:** Expose the full Tencent memory, knowledge, skill, metadata, Wiki, and
CodeGraph capability through narrow Go interfaces instead of ad hoc HTTP calls.  
**Primary files:** `internal/memory/tencent/`, new focused memory/knowledge
client files, contracts, HTTP fixtures, compatibility metadata.  
**Gate:** every enabled endpoint has isolation, response-envelope, redaction,
timeout, and fixture coverage.

### Implementation tasks

- [ ] **P23-T01** Define typed Go interfaces and request/response models for MemoryCore, Knowledge Service, Skill Memory, and metadata assets while keeping adapters independent of raw Tencent JSON.
- [ ] **P23-T02** Complete v3 L0/L1/L2/L3 operations: conversation add/query/search/delete/count, atomic query/search/update/delete/count, scenario list/read/write/remove/count, and core read/write/count.
- [ ] **P23-T03** Implement `/v3/knowledge/*` metadata create/get/update/delete/list for Wiki and CodeGraph assets with service URL, team, user, branch, and repository ownership.
- [ ] **P23-T04** Implement Wiki create/list/get/delete, raw source list/read/write/remove, ingest, page list/read/write/remove, graph, and search operations.
- [ ] **P23-T05** Implement CodeGraph create/list/get/sync/delete/status/files/search/explore/node/callers/callees/impact operations and asynchronous status polling.
- [ ] **P23-T06** Implement Skill Memory create/search/version/resource/extraction paths and map shared project skills without overwriting user-owned Codex skills.
- [ ] **P23-T07** Implement user/team/agent/task/asset/membership/access metadata calls needed for provisioning, ownership, and cleanup.
- [ ] **P23-T08** Implement Knowledge Service tools discovery/call with allowlisted tool names, bounded arguments, and historical-result labeling.
- [ ] **P23-T09** Centralize v3 headers/body isolation, service ID, session ID, bearer/user key selection, response code handling, timeout, retry, and redaction behavior.
- [ ] **P23-T10** Add OpenAPI-backed fixture compatibility tests and version capability discovery so unsupported Tencent features fail clearly rather than silently degrading.

### Mandatory tests

- [ ] **P23-TEST01** Each L0-L3 operation round-trips through a strict isolated HTTP fixture.
- [ ] **P23-TEST02** Wiki lifecycle creates, ingests, searches, reads, updates, and removes a project Wiki asset.
- [ ] **P23-TEST03** CodeGraph lifecycle registers, indexes, syncs, searches, explores, and returns impact/caller/callee results.
- [ ] **P23-TEST04** Skill lifecycle preserves versions/resources and rejects cross-team access.
- [ ] **P23-TEST05** Metadata fixture provisions task/asset/access relationships without ambiguous project binding.
- [ ] **P23-TEST06** Missing/invalid isolation fields fail before network or return classified 4xx without leakage.
- [ ] **P23-TEST07** Knowledge service outage leaves local continuity usable and queues the correct typed operation.
- [ ] **P23-TEST08** Unsupported endpoint/version produces actionable `baron doctor` output and never corrupts state.

## P24 — Automatic project knowledge provisioning `[ ]`

**Goal:** Make `baron setup` automatically provision all Tencent memory/knowledge
assets for the current project.  
**Primary files:** `internal/project/`, `internal/app/`, `internal/config/`,
new knowledge registry/state tables, setup/repair tests.  
**Gate:** a project needs no manual Tencent Panel registration or asset ID entry.

### Implementation tasks

- [ ] **P24-T01** Add a stable local project knowledge registry for `code_graph_id`, `wiki_id`, service URL, branch, repository identity, last sync commit, ingest state, and ownership.
- [ ] **P24-T02** Define remote repository policy: use a verified Git remote/branch when available, and provide a safe local-only fallback when a project has no remote URL.
- [ ] **P24-T03** Register/reuse CodeGraph by project ID/repository/branch and reject ambiguous same-name assets.
- [ ] **P24-T04** Create/reuse a project Wiki and seed bounded sources such as README, docs, architecture/design, ADR, and changelog files without uploading credentials or arbitrary ignored files.
- [ ] **P24-T05** Queue Wiki ingest and CodeGraph indexing/sync, poll readiness, and report pending/failed/ready state without blocking local setup longer than the configured budget.
- [ ] **P24-T06** Make setup/repair reconstruct missing local asset IDs from Tencent metadata and repair only Baron-owned assets.
- [ ] **P24-T07** Enforce per-project/team/agent ownership for all assets and preserve a project’s memory/knowledge when the directory moves or is cloned.

### Mandatory tests

- [ ] **P24-TEST01** `baron setup` on a clean remote project creates/reuses exactly one agent, CodeGraph, Wiki, and registry mapping.
- [ ] **P24-TEST02** Rerun setup five times without duplicate knowledge assets or Wiki pages.
- [ ] **P24-TEST03** A Git branch/commit change triggers the correct CodeGraph sync and records the new commit/status.
- [ ] **P24-TEST04** Wiki seed excludes `.env`, secrets, ignored runtime files, and hidden credentials.
- [ ] **P24-TEST05** Removing local knowledge registry state reconstructs the same remote assets, not duplicates.
- [ ] **P24-TEST06** Two projects with identical names/basenames receive isolated agent, Wiki, CodeGraph, and memory assets.

## P25 — Full context orchestration for DSH and Codex `[ ]`

**Goal:** Automatically compile a bounded context packet from local continuity,
Tencent memory, Wiki, CodeGraph, Skill Memory, Git, and test evidence at the
right lifecycle points.  
**Primary files:** `internal/continuity/`, `internal/hooks/`, `internal/recovery/`,
`adapters/dsh/`, Codex hook integration, context tests.  
**Gate:** both agents receive useful, bounded, evidence-labelled context without
being flooded or given permissions by memory.

### Implementation tasks

- [ ] **P25-T01** Define context layers, source labels, trust/freshness labels, precedence, maximum characters/tokens, and historical-reference boundaries.
- [ ] **P25-T02** At session start, load L3/L2 profile/scenario, last checkpoint, interruption status, relevant Wiki pages, and CodeGraph project status.
- [ ] **P25-T03** At user prompt/pre-step, derive search terms from goal, prompt, files, symbols, errors, tests, and Git branch; query bounded memory/knowledge results.
- [ ] **P25-T04** After tools/tests, record changed files, commands, exit codes, diagnostics, symbol impact, and relevant memory/knowledge citations.
- [ ] **P25-T05** At checkpoint/stop/final/flush, update scenario/core summaries, next action, completion verification, and queued sync operations.
- [ ] **P25-T06** Use CodeGraph callers/callees/impact and Wiki graph/search only when relevant to the current task; never inject an entire repository or knowledge base.
- [ ] **P25-T07** Keep DSH and Codex adapters symmetric on canonical events while preserving each agent’s independent model/provider/skills.
- [ ] **P25-T08** Make all remote/context failures fail open to local continuity with diagnostics and no false completion claim.

### Mandatory tests

- [ ] **P25-TEST01** Session start returns a bounded packet with current local state, remote memory, Wiki, and CodeGraph citations.
- [ ] **P25-TEST02** A symbol-change task receives only relevant callers/callees/impact/files rather than the full graph.
- [ ] **P25-TEST03** DSH and Codex receive equivalent canonical state while retaining independent skill/config surfaces.
- [ ] **P25-TEST04** Stale/contested memory is labeled historical and cannot override newer Git/test evidence.
- [ ] **P25-TEST05** Context over-limit results are truncated by priority and remain valid JSON/agent hook output.
- [ ] **P25-TEST06** Tencent/Knowledge outage still delivers local checkpoint/recovery context within hook timeout.

## P26 — Full reliability, security, migration, and operations `[ ]`

**Goal:** Make the expanded Tencent/Knowledge integration safe under outages,
concurrency, updates, stale indexes, secrets, and machine migration.  
**Primary files:** `internal/storage/`, `internal/continuity/`, `internal/recovery/`,
`internal/app/backup.go`, `internal/doctor/`, `SECURITY.md`.  
**Gate:** every remote operation is durable, idempotent, isolated, bounded, and
repairable without a daemon.

### Implementation tasks

- [ ] **P26-T01** Add typed queue operations for memory capture, core/scenario update, Wiki ingest, CodeGraph sync, Skill update, and metadata repair.
- [ ] **P26-T02** Add operation-specific retry/backoff/dead-letter diagnostics, receipt IDs, and bounded flush budgets.
- [ ] **P26-T03** Track freshness, Git commit, Wiki ingest version, CodeGraph status, memory timestamps, conflicts, and supersession without silently overwriting newer truth.
- [ ] **P26-T04** Extend secret redaction/trust boundaries to Wiki raw sources, CodeGraph metadata, Skill resources, tool output, Docker logs, backups, and exports.
- [ ] **P26-T05** Extend backup/restore to knowledge registry, Wiki/CodeGraph asset metadata, sync queue, and Tencent deployment data policy without plaintext keys.
- [ ] **P26-T06** Expose operational status for Docker, services, asset readiness, queue depth, stale indexes, last successful sync, and next repair action.
- [ ] **P26-T07** Add concurrency/fault tests for simultaneous DSH/Codex events, Wiki ingest, CodeGraph sync, service restart, power loss, and update rollback.

### Mandatory tests

- [ ] **P26-TEST01** Queue outage operations and recover them exactly once after memory/knowledge services return.
- [ ] **P26-TEST02** Simultaneous CodeGraph sync and agent checkpoint leaves both durable and correctly ordered.
- [ ] **P26-TEST03** Stale CodeGraph/Wiki status is reported and never presented as current verified source evidence.
- [ ] **P26-TEST04** Full expanded secret corpus scan finds no raw admin/user/provider credentials in state, logs, assets, or backups.
- [ ] **P26-TEST05** Backup/restore reconstructs knowledge asset mappings without duplicate remote assets or plaintext credentials.
- [ ] **P26-TEST06** Service restart, abrupt hook kill, and update failure leave local continuity and queue recoverable.

## P27 — Cross-platform release, documentation, and final Nexus acceptance `[ ]`

**Goal:** Ship the invisible, one-command Ubuntu/Debian experience and the
truthful Windows guidance with complete Tencent knowledge functionality.  
**Primary files:** `README.md`, `SECURITY.md`, `docs/`, `.github/workflows/`,
release scripts/installers, final acceptance report.  
**Gate:** full Nexus release only after Linux auto-bootstrap, Tencent full-stack,
DSH/Codex handoff, isolation, recovery, and Windows guidance all have evidence.

### Implementation tasks

- [ ] **P27-T01** Update Linux/Windows quick start so the user-facing command sequence remains unchanged while the behind-the-scenes behavior is documented accurately.
- [ ] **P27-T02** Ship release artifacts/installers with Linux bootstrap prerequisites, Windows guidance, version/digest manifest, checksum, SBOM, and rollback evidence.
- [ ] **P27-T03** Run supported Ubuntu/Debian matrix: missing sudo, fresh Docker install, existing Docker, stopped daemon, reboot, rerun, repair, and uninstall-safe behavior.
- [ ] **P27-T04** Run real DSH/Codex/Tencent end-to-end scenarios with memory, Wiki, CodeGraph, Skill, checkpoints, recovery, and cross-agent handoff.
- [ ] **P27-T05** Run Windows release/guidance scenario and prove no false claim that Docker/WSL/Tencent were auto-installed.
- [ ] **P27-T06** Generate the final evidence report with candidate SHA, component versions/digests, phase checklist, blockers, artifacts, and release decision.
- [ ] **P27-T07** Publish only when all mandatory Nexus gates are checked; otherwise leave the final status explicitly `BLOCKED` with the exact missing dependency/evidence.

### Mandatory tests

- [ ] **P27-TEST01** One-command Ubuntu/Debian bootstrap passes from a clean supported machine after the user grants sudo.
- [ ] **P27-TEST02** A user without sudo is stopped before download and receives the exact remediation instruction.
- [ ] **P27-TEST03** The complete user command sequence provisions Tencent memory/knowledge and a project without manual Panel operations.
- [ ] **P27-TEST04** CodeGraph/Wiki/Skill retrieval is bounded, isolated, cited, and available to both DSH and Codex.
- [ ] **P27-TEST05** Cross-agent unfinished work, network loss, service restart, and power-loss recovery pass on a real release candidate.
- [ ] **P27-TEST06** Windows instructions are accurate, explicit, and do not claim unsupported automation.
- [ ] **P27-TEST07** Final release artifacts, checksums, SBOM, rollback, docs, and acceptance report all agree on the same candidate revision.

## User-visible completion contract

When the roadmap is complete, the user should only need to understand this:

```bash
baron deepseek-harness init
baron codex-cli init
baron tencent-memory init
baron test

cd /path/to/project
baron setup

# daily use
dsh web
codex
```

On Ubuntu/Debian, `baron tencent-memory init` performs the sudo preflight before
download, installs Docker when needed, deploys the full Tencent stack, starts
the services, provisions the Baron identity, registers project memory/knowledge,
and configures restart/repair behavior. On Windows, it prints the required
Docker Desktop/WSL/Ubuntu/Tencent actions instead of pretending to automate UI
installation.

## Global final checks

- [ ] Every checked implementation task has a corresponding code/doc/test artifact.
- [ ] Every unchecked item has an explicit blocker or remaining implementation path.
- [ ] No checkbox is checked solely because a mock exists when the task requires a live service/client.
- [ ] No release is called PASS while any R1/R3/R5/R7/R9/R10 or platform acceptance gate is unchecked.
- [ ] `baron test` remains read-only; `baron setup` remains the only required per-project provisioning command.
- [ ] Existing user-owned DSH/Codex settings and skills remain preserve-first.
- [ ] Tencent data is project/team/agent/user isolated, and Git/source/test evidence outranks stale memory.
- [ ] Final report contains the actual candidate revision, artifacts, checksums, test output, and known limitations.

## Evidence references

- [IMPLEMENT.pdf](</home/ty/Baron dsh-codex-tencent/IMPLEMENT.pdf>)
- [Baron Engine predecessor](https://github.com/thienty1207/Baron-Engine)
- [Current implementation progress](</home/ty/Baron dsh-codex-tencent/docs/implementation/IMPLEMENT_PROGRESS.md>)
- [Current acceptance report](</home/ty/Baron dsh-codex-tencent/docs/implementation/FINAL_ACCEPTANCE_REPORT.md>)
