# Baron Shared Brain Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Implement the standalone Go Baron Shared Brain described by `IMPLEMENT.pdf`, including local continuity, Tencent memory routing, DSH/Codex integrations, recovery, isolation, and release evidence.

**Architecture:** A Cobra CLI and short-lived hook runtime sit over a Go core. SQLite WAL is the local authority; a `MemoryBackend` interface isolates Tencent HTTP behavior; project identity and isolation are centralized in `internal/project`; continuity and recovery are derived from event evidence and Git inspection.

**Tech Stack:** Go 1.27.0, Cobra, `net/http`, `encoding/json`, `modernc.org/sqlite`, TOML, `os/exec`, minimal TypeScript only for the DSH adapter boundary, Go test/httptest.

**Spec:** `docs/specs/baron-shared-brain-design.md` and the authoritative `IMPLEMENT.pdf`.

## Global Constraints

- Core must be Go; no Rust/Cargo dependency or `target/` build tree.
- SQLite must use `modernc.org/sqlite` with CGO disabled where supported.
- Local continuity is written before any Tencent operation; remote failure is queued and fail-open.
- `project_id` is stable and authoritative; every Tencent operation is project-isolated.
- No raw API keys, auth files, admin keys, system prompts, or hidden reasoning may be persisted or logged.
- User-owned DSH/Codex configuration and skills must survive Baron initialization byte-for-byte except Baron-owned additions.
- No process stop or hook silence may mark an unfinished task complete.
- Every phase test and every final F01-F24 case must have truthful recorded evidence.

## Traceability map

| Phase | Name | Primary implementation/tests |
| --- | --- | --- |
| P0 | Baseline, contracts, Go repository bootstrap | docs, `acceptance/`, module/package skeleton, contract tests |
| P1 | CLI/configuration | `internal/cli`, `internal/config`, atomic writes, redaction tests |
| P2 | Local state/hooks | `internal/storage`, `internal/hooks`, SQLite/concurrency/fault tests |
| P3 | Readiness | `internal/install`, `internal/doctor`, command matrix tests |
| P4 | DSH baseline | installer receipts/config merge, DSH fixture tests, TypeScript adapter placeholder |
| P5 | Codex baseline | hook merge/auth detection, Codex fixture tests |
| P6 | Tencent identity | `internal/memory/tencent`, metadata provisioning tests against httptest fixtures |
| P7 | Project setup | `internal/project`, identity/path/permissions/Gitignore tests |
| P8 | Project namespace | Tencent agent binding/isolation-context tests |
| P9 | Checkpoint engine | `internal/continuity`, Git evidence and interruption tests |
| P10 | Memory/recall | redaction, bounded packets, backend/queue integration tests |
| P11 | Codex hooks | hook event adapters and fail-open tests |
| P12 | DSH hooks | adapter contract/version tests and shared-state tests |
| P13 | Handoff/recovery | `internal/recovery`, both-direction evidence tests |
| P14 | Offline/concurrency | retry/idempotency/fault-injection/race tests |
| P15 | Isolation/migration safety | negative ten-project and legacy snapshot tests |
| P16 | Backup/restore | manifest/checksum/secret policy/cross-path tests |
| P17 | Security | threat model, path/symlink/injection/fuzz/secret corpus tests |
| P18 | Release engineering | native builds, installers, repair/doctor/rollback, artifact audit |
| P19 | Final acceptance | exact release candidate suite and report |

## Ordered task checklist

Implementation proceeds phase-by-phase. Each phase is red/green tested before
the next begins and maps directly to the PDF identifiers.

- [ ] P0-T01..P0-T12 and P0-TEST01..P0-TEST04
- [ ] P1-T01..P1-T10 and P1-TEST01..P1-TEST04
- [ ] P2-T01..P2-T10 and P2-TEST01..P2-TEST04
- [ ] P3-T01..P3-T10 and P3-TEST01..P3-TEST04
- [ ] P4-T01..P4-T12 and P4-TEST01..P4-TEST06
- [ ] P5-T01..P5-T08 and P5-TEST01..P5-TEST04
- [ ] P6-T01..P6-T12 and P6-TEST01..P6-TEST05
- [ ] P7-T01..P7-T11 and P7-TEST01..P7-TEST05
- [ ] P8-T01..P8-T08 and P8-TEST01..P8-TEST04
- [ ] P9-T01..P9-T10 and P9-TEST01..P9-TEST04
- [ ] P10-T01..P10-T10 and P10-TEST01..P10-TEST05
- [ ] P11-T01..P11-T11 and P11-TEST01..P11-TEST05
- [ ] P12-T01..P12-T10 and P12-TEST01..P12-TEST05
- [ ] P13-T01..P13-T09 and P13-TEST01..P13-TEST04
- [ ] P14-T01..P14-T09 and P14-TEST01..P14-TEST04
- [ ] P15-T01..P15-T07 and P15-TEST01..P15-TEST04
- [ ] P16-T01..P16-T09 and P16-TEST01..P16-TEST04
- [ ] P17-T01..P17-T10 and P17-TEST01..P17-TEST05
- [ ] P18-T01..P18-T10 and P18-TEST01..P18-TEST06
- [ ] P19-T01..P19-T16 and P19-TEST01..P19-TEST12

