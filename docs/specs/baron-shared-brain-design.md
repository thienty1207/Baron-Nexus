# Baron Nexus design

Status: frozen implementation design derived from `IMPLEMENT.pdf` (2026-08-23).

## Product contract

Baron is a short-lived Go sidecar invoked by DeepSeek Harness (DSH) and OpenAI
Codex lifecycle hooks. It owns project identity, local durability, continuity,
memory routing, recovery, offline synchronization, and project isolation. It is
not a third model or an always-on daemon. DSH remains the DeepSeek model client;
Codex remains the OpenAI model client.

The following contracts are authoritative and are represented exactly once in
`acceptance/acceptance-contract.json`:

| ID | Frozen behavior |
| --- | --- |
| R1 | DSH and Codex share one project memory namespace and continuity state; both handoff directions preserve evidence needed to continue unfinished work. |
| R2 | Only durable project facts, events, decisions, commands, tests, errors, summaries, and checkpoints are shared; hidden reasoning and system prompts are excluded. |
| R3 | Each project has one stable Baron project ID and one Tencent Agent namespace; normal recall cannot cross projects. |
| R4 | `baron setup` provisions all Baron/Tencent project bindings and hook prerequisites without Tencent web-panel work. |
| R5 | Unclean stop, kill, power loss, network loss, and agent crash result in reconciliation, never false completion. |
| R6 | Local checkpoint/event writes are durable and first; remote failures become a retry queue. |
| R7 | DSH initialization installs/verifies DSH, DuckDuckGo search, DSH Superpowers, DSH Reverse Skill, and the Baron adapter. |
| R8 | Codex initialization installs/verifies Codex and Baron hooks without taking ownership of user skills/plugins. |
| R9 | Tencent initialization creates/reuses the Baron user, active user key, global `baron-projects` team, and services without creating Main Developer. |
| R10 | Project secrets live in `.baron/.env`, are ignored by Git, and have restrictive permissions; `project.toml` is non-secret and commit-safe. |
| R11 | The normal CLI is `init`, `test`, and `setup`; support commands are status, doctor, repair, backup, and restore. |
| R12 | No Baron daemon is required; hooks invoke the Go binary on demand. |
| R13 | Memory failure never reroutes model traffic; provider traffic remains independent. |
| R14 | `project_id` survives path changes and OS reinstall; path is secondary metadata. |
| R15 | Core, CLI, storage, Tencent client, routing, recovery, backup/restore, diagnostics, and hooks are native Go with CGO-free SQLite; TypeScript is limited to the DSH boundary. |

## Component boundaries

```text
cmd/baron -> internal/cli
internal/cli -> project, storage, continuity, recovery, memory/tencent, hooks, install, doctor
project -> config + storage
continuity -> storage + project/git evidence
recovery -> continuity + project/git evidence + memory receipts
hooks -> continuity + recovery + memory backend (never agent implementation details)
memory/tencent -> contracts.MemoryBackend + net/http + encoding/json
install -> bounded process/config helpers; no core state ownership
```

The `MemoryBackend` interface is defined in the core contracts package. The
Tencent implementation is an HTTP client with timeouts, project isolation
fields, redaction, and receipt IDs. Tests can use an in-memory backend or an
`httptest.Server`; production code never depends on upstream DSH/Codex types.

## Durable state model

Each project contains:

```text
.baron/project.toml        stable, non-secret identity
.baron/.env                project-local runtime secrets, mode 0600 on Unix
.baron/checkpoint.json     materialized readable WIP snapshot
.baron/runtime/state.db   authoritative SQLite WAL journal
.baron/runtime/logs/      bounded redacted diagnostics
```

SQLite migrations are transactional and use an explicit schema version. Events
are UUID-addressed and idempotent. A work-state update and its checkpoint are
materialized from the journal; the JSON file is never the sole authority.
Remote captures are queued before network delivery. Queue delivery is
idempotent and records a remote receipt. Remote failure is fail-open for agent
operation.

## Identity and isolation

`project.toml` is authoritative when present. A newly initialized project gets
a cryptographically random `prj-` ID. Tencent bindings are resolved by
project ID metadata, never by folder/display name. Every read/write/search
uses a centralized isolation context containing `project_id`, `team_id`,
`agent_id`, and `user_id` where available. A mismatched local environment is an
integrity failure before any data-plane request.

## Hook and recovery behavior

Hooks accept canonical `HookClient` and `EventType` values plus JSON on stdin.
They persist local evidence before remote operations, return bounded JSON, and
fail open when Baron/Tencent state is malformed or unavailable. Session stop
does not complete a task. A later session compares checkpoint Git metadata with
current Git state and emits a historical-reference-only recovery packet with
known evidence, uncertainty, changed files, failing/not-yet-run tests, and the
next safe action.

Retrieved memory is untrusted historical data. It is redacted, bounded,
deduplicated, provenance-labeled, and wrapped so it cannot grant tool
permissions or override current instructions.

## Compatibility and safety policy

Pinned compatibility metadata is kept for DSH, Codex, Tencent, DuckDuckGo MCP,
Superpowers, and Reverse Skill. Unknown upstream versions produce actionable
diagnostics. Baron modifies only Baron-owned config fragments and creates a
backup before editing user-owned DSH/Codex files. Legacy Baron binaries and
Tencent entities are detected/reused or left untouched; no implicit migration
or destructive overwrite is allowed.

## Testing and release policy

Every PDF phase has named tests and evidence in
`docs/implementation/IMPLEMENT_PROGRESS.md`. Unit tests use real SQLite,
filesystem, Git fixtures, HTTP test servers, and fault injection where
possible. External DSH/Codex/Tencent release-gate tests are classified as
blocked when the required user-installed service, credential, or interactive
login is unavailable; they are never marked PASS by a mock-only substitute.
