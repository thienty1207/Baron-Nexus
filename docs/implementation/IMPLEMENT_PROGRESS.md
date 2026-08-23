# Baron Shared Brain implementation progress

Authoritative source: `/home/ty/Baron dsh-codex-tencent/IMPLEMENT.pdf`.

This file records evidence, not intentions. A phase is only marked PASS when
its mandatory tests have fresh, recorded evidence. External release-gate cases
that require Docker, credentials, or interactive login are marked BLOCKED with
the exact dependency; they are not converted to PASS by mocks.

## Baseline

- Starting repository: fresh directory containing `IMPLEMENT.pdf`; no Git metadata or source tree existed.
- Worktree: in place because no Git repository existed to create a linked worktree from.
- OS/architecture: Ubuntu Linux, `linux/amd64` (`Linux Ferris 7.0.0-30-generic`).
- Go: `/usr/local/go/bin/go version go1.27.0 linux/amd64` (usable; not initially on `PATH`).
- Docker: not installed/available in the execution environment.
- Node/npm/npx/pnpm/uv/uvx: not installed/available in the execution environment.
- Git: `git version 2.53.0`; repository initialized in place because the supplied workspace was not a Git checkout.
- PDF: 36 pages, extracted and visually checked at representative contract, phase, data-contract, and final pages.

## Phase ledger

| Phase | Status | Tasks completed | Files changed | Commands/tests | Failures/fixes | Remaining limitations |
| --- | --- | --- | --- | --- | --- | --- |
| 0 | PASS | P0-T01..P0-T12; acceptance and package skeleton created; Go contract layer added | `cmd/baron/`, `internal/contracts/`, package docs, `go.mod`, `go.sum`, docs, acceptance contract, `.gitignore` | `go test ./...`, `go vet ./...`, CGO-free Linux amd64 and Windows amd64 builds, no `target/` | Go was absent from PATH; used pinned `/usr/local/go/bin/go1.27.0` | No external runtime services are required by Phase 0; live integrations are later gates |
| 1 | PASS | P1-T01..P1-T10; Cobra command surface, exit codes, JSON flags, atomic writes, backups, redaction, global state | `internal/cli/`, `internal/config/`, `internal/install/` | CLI/config/install package tests; native binary `--help` and setup smoke | Initial Cobra method mismatch fixed; dependency versions resolved by Go module tooling | Real upstream installers remain environment-gated |
| 2 | PASS | P2-T01..P2-T10; SQLite WAL schema, idempotent events, checkpoint engine, hook stdin/stdout runtime, bounded logs foundation | `internal/storage/`, `internal/continuity/`, `internal/hooks/` | SQLite concurrency/idempotency tests, 10,000-event multi-process test, checkpoint/Git drift tests, duplicate/malformed hook tests | SQLite per-connection busy behavior fixed with WAL busy timeout plus in-process writer serialization | Cgo race run is blocked by the host toolchain; CGO-free tests pass |
| 3 | PASS | P3-T01..P3-T10; readiness matrix and secret-safe diagnostics | `internal/doctor/`, CLI app wiring | Docker missing/stopped, Codex auth incomplete, all-fixture-green doctor tests; `baron test --json` smoke | None after probe fixture corrections | Docker/Node/uv/Tencent/interactive auth are unavailable in this environment |
| 4 | BLOCKED | P4-T01..P4-T12; pinned DSH install, plugin/profile merge, DuckDuckGo MCP row, embedded Baron adapter bundle, compatibility metadata, receipts | `internal/install/`, `adapters/dsh/`, `internal/install/assets/`, `configs/compatibility.json` | DSH merge/idempotency, pinned-command, profile-patch, embedded-adapter, and plugin-command tests | Local implementation and fixtures pass; real DSH/Node/uvx and network smoke cannot run | Requires Node 22.19+/24+, npm, pnpm, uvx, DSH, and network access |
| 5 | BLOCKED | P5-T01..P5-T08; official nested Codex hook merge, fail-open response, runtime secret redaction, global state/repair | `internal/install/`, `internal/app/`, `internal/hooks/` | Hook shape, preservation/idempotency, malformed-input, tamper, and redaction tests | Local synthetic hook tests pass; installed Codex execution/auth cannot run | Requires installed Codex CLI and interactive ChatGPT sign-in |
| 6 | BLOCKED | P6-T01..P6-T12; native Tencent v3 client, layered reads, identity/agent provisioning, managed deployment bootstrap | `internal/memory/tencent/`, `internal/install/tencent.go`, `internal/app/` | Strict httptest endpoint/isolation/identity tests, deployment fixture, admin-key permission tests | Local implementation and HTTP fixtures pass; Docker-backed Tencent stack cannot run | Requires Docker and Tencent memory services; no live credential was used |
| 7 | PASS | P7-T01..P7-T11; stable project identity, setup, move behavior, Unicode/space paths, Git-ignore and permission protections | `internal/project/`, `internal/app/`, `internal/config/` | Setup idempotence, move, Unicode, root rejection, symlink/tamper, and permission tests | None in the fresh Go test run | Live multi-agent clients are covered by later blocked gates |
| 8 | BLOCKED | P8-T01..P8-T08; automatic project provisioning and strict Tencent request boundary | `internal/app/`, `internal/project/`, `internal/memory/tencent/` | Provisioning and strict-isolation HTTP fixtures pass | Real Tencent provisioning cannot be exercised | Requires Docker-backed Tencent services and credentials |
| 9 | PASS | P9-T01..P9-T10; local-first event journal, checkpoints, recovery packet, interruption/close semantics, offline queue | `internal/storage/`, `internal/continuity/`, `internal/recovery/`, `internal/hooks/` | WAL, checkpoint, interrupted-session, drift, queue, recovery, and redaction tests pass | None in the fresh Go test run | Cross-process race evidence remains toolchain-gated |
| 10 | BLOCKED | P10-T01..P10-T10; native Tencent layered context, local-first sync, bounded remote context, failure fallback | `internal/memory/tencent/`, `internal/continuity/`, `internal/hooks/` | Layered endpoint/isolation and offline queue fixtures pass | Live memory read/write and outage recovery cannot run | Requires Docker/Tencent service and credentials |
| 11 | BLOCKED | P11-T01..P11-T11; Codex hook integration, event mapping, recovery context, fail-open behavior | `internal/hooks/`, `internal/install/`, `internal/app/` | Synthetic Codex hook payload, malformed payload, recovery-context, and fail-open tests pass | Codex process/auth integration unavailable | Requires installed/authenticated Codex CLI |
| 12 | BLOCKED | P12-T01..P12-T10; DSH lifecycle adapter, canonical event mapping, profile bundle, MCP rows | `adapters/dsh/`, `internal/install/assets/dsh-adapter/`, `internal/app/` | TypeScript/runtime adapter source checks, embedded bundle, profile patch, and lifecycle tests pass | DSH runtime and Node package install unavailable | Requires Node, DSH, pnpm, uvx, and DeepSeek credentials |
| 13 | BLOCKED | P13-T01..P13-T09; cross-agent continuity contracts, DSH/Codex handoff receipts, context bounds | `internal/contracts/`, `internal/hooks/`, `internal/recovery/`, `internal/continuity/` | Contract, handoff, recovery, bounded-context, and canonical-event tests pass | Real two-client handoff cannot run | Requires authenticated Codex and DSH sessions plus Tencent backend |
| 14 | BLOCKED | P14-T01..P14-T09; concurrency, duplicate delivery, abrupt interruption recovery, bounded logs | `internal/storage/`, `internal/continuity/`, `internal/hooks/`, `internal/recovery/` | Concurrent claims, duplicate delivery, multi-process journal, stale-lock recovery, and log-bound tests pass | Race detector cannot run because cgo compiler is absent; SIGKILL/live-client gates unavailable | Requires gcc/cgo, Docker, and client runtimes |
| 15 | BLOCKED | P15-T01..P15-T07; project isolation, identity binding, credential separation, redaction | `internal/project/`, `internal/memory/tencent/`, `internal/config/`, `internal/app/` | Ten-project identity, ambiguous-agent, tamper, symlink, secret-persistence, and HTTP-isolation tests pass | Legacy/live Tencent namespace separation cannot run | Requires Tencent deployment and multiple authenticated project environments |
| 16 | BLOCKED | P16-T01..P16-T09; backup/restore, checksum validation, secret exclusion, update rollback | `internal/app/`, `internal/install/` | Backup secret exclusion, checksum-corruption rejection, and binary rollback tests pass | Docker-volume and cross-OS restore tests unavailable | Requires Docker volume and Windows runtime validation |
| 17 | PASS | P17-T01..P17-T10; path/symlink hardening, input validation, secret redaction, bounded output, safe failure paths | `internal/project/`, `internal/config/`, `internal/hooks/`, `internal/memory/tencent/`, `internal/install/` | Security/permission, tamper, redaction, malformed input, bounded log, and contract tests pass; `go vet ./...` passes | None in local static/test evidence | External dependency security review remains outside this offline run |
| 18 | BLOCKED | P18-T01..P18-T10; release build, platform artifacts, checksums, installer/update rollback and smoke scripts | `scripts/`, `install.sh`, `install.ps1`, `internal/install/` | CGO-free Linux amd64 and Windows amd64 builds plus release-script smoke pass locally | Windows runtime, PowerShell, and external installer download path unavailable | Requires Windows execution and networked installer test |
| 19 | BLOCKED | P19-T01..P19-T16; final acceptance ledger, release candidate, all client/service gates | docs, release scripts, all implementation packages | Local acceptance contract, full Go tests, vet, builds, checksums, and isolated CLI smoke pass | External gates remain unavailable: Docker, Node/npm/npx/pnpm, uv/uvx, Codex auth, DSH auth, Tencent services, Windows runtime, cgo race | Final status cannot be PASS until those gates are executed |

## Fresh local verification

Run from the repository root with Go 1.27.0:

- `GOTOOLCHAIN=local /usr/local/go/bin/go test -count=1 ./...` — PASS.
- `GOTOOLCHAIN=local /usr/local/go/bin/go vet ./...` — PASS.
- `GOTOOLCHAIN=local CGO_ENABLED=0 /usr/local/go/bin/go test -count=1 ./...` — PASS.
- `git diff --check` — PASS.

Candidate release evidence is recorded in
`docs/implementation/FINAL_ACCEPTANCE_REPORT.md`. The final status remains
BLOCKED until the external gates above are available.
