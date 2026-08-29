# Local-First Task Ledger Implementation Plan

> **Execution note:** Implement this plan with the TDD skill. Each phase must
> add or update a failing test before the production change, then run the
> narrowest relevant package tests before proceeding.

## Goal

Implement the approved local-first Task Ledger and conditional Tencent recovery
design without changing the existing authority boundaries:

- Git/working tree remains current source truth.
- SQLite remains canonical local event/task/checkpoint state.
- Tencent remains historical/recovery reference only.
- Same-session and same-machine handoff remains local-only when evidence is
  sufficient.

## Phase 1: Contracts, schema, and projection

### Tests first

- Extend `internal/contracts/contract_test.go` to cover canonical task event
  names, `interrupted` status, verification kinds, and completion policies.
- Add storage tests in `internal/storage/task_ledger_test.go` for migration from
  the current schema, idempotent event insertion, and atomic event-plus-task
  projection.
- Add transition tests for task creation/resume, planned starts, partial
  updates, unknown `task_updated` rejection, failure/block/interruption,
  verification-kind separation, completion policy, stale Git/diff evidence,
  duplicate starts, and completed-task reopen rejection.

### Implementation

- Add canonical task event constants and validation types to
  `internal/contracts/contracts.go`.
- Add `TaskState`/task-event payload types and bounded normalization helpers in
  the contracts or storage boundary without coupling adapters to SQLite.
- Add an idempotent SQLite migration in `internal/storage/storage.go` for task
  projections, task path/module/dependency evidence, verification evidence,
  active task context, and local remote-recall cache metadata. Preserve all
  existing rows and support `interrupted`.
- Add `TaskRecord`, `TaskVerification`, `TaskScope`, and timeline record types
  plus indexed list/get methods in `internal/storage`.
- Add one write method that inserts the canonical event and projects its task
  mutation in one transaction. Reuse the existing event idempotency key and
  ensure a rejected `task_updated` never creates a task row.
- Add deterministic transition validation and projection rules. Only
  `task_started` creates/resumes; `task_updated` requires an existing task;
  non-completion verification never promotes completion; completion requires a
  policy-compatible, current verification.
- Add `ActiveTaskID`, completion policy, and latest verification metadata to the
  local continuity model while preserving legacy WorkState/checkpoint decoding.

### Verification

Run:

```text
go test ./internal/contracts ./internal/storage ./internal/continuity
go vet ./internal/contracts ./internal/storage ./internal/continuity
```

## Phase 2: Deterministic Resume Gate and local context

### Tests first

- Add `internal/continuity/resume_gate_test.go` with table-driven cases for
  local-sufficient, stale/missing local state, explicit remote recovery,
  strong source-file overlap, same-module overlap, dependency overlap,
  unrelated work, ambiguous scope, and weak-only README/lockfile/generated or
  metadata intersections.
- Add hooks tests proving local handoff does not call Tencent when local
  evidence is sufficient, and that repeated same-session prompts do not issue
  repeated remote queries.
- Add cache tests for unchanged and changed recovery fingerprints.

### Implementation

- Add a deterministic `ResumeGate` API in `internal/continuity` that consumes
  Git evidence, WorkState, Task Ledger rows, structured new-task scope, and
  session/client identity. It must never call an LLM.
- Classify strong versus weak file evidence. Weak-only intersections must not
  return `overlap_requires_resume`.
- Add bounded local task-ledger context construction and a stable recovery
  fingerprint.
- Add SQLite-backed remote recall cache/receipt accessors. Gate remote recall
  by local sufficiency, fingerprint, explicit historical request, and the
  approved recovery conditions.
- Update `internal/hooks/hooks.go` to evaluate local context before Tencent,
  use one bounded merged packet, and query Tencent only when the gate requires
  it. Keep remote calls asynchronous/fail-open.

### Verification

Run:

```text
go test ./internal/continuity ./internal/hooks ./internal/storage
go vet ./internal/continuity ./internal/hooks ./internal/storage
```

## Phase 3: Adapter and handoff task identity

### Tests first

- Extend `internal/hooks/hooks_test.go` and adapter/install tests with explicit
  `task_id`, `active_task_id`, verification kind/scope, and structured task
  event payloads from Codex and DSH.
- Add tests proving prose-only prompts cannot create/split tasks and that task
  identity survives session, tool, test, checkpoint, error, handoff, and close
  events.

### Implementation

- Preserve and normalize explicit task fields in the Codex and DSH lifecycle
  adapters under `internal/install/assets` and the Go hook boundary.
- Add the explicit task-start handshake and active-task routing. Generate an ID
  only for an explicit structured `task_started` event; never infer a boundary
  from prose.
- Include task ledger and bounded verification evidence in local handoff
  packets, with local authority labels and no secrets.
- Keep legacy unassociated events as unassigned evidence or one marked legacy
  task; do not heuristically split historical prose.

### Verification

Run:

```text
go test ./internal/hooks ./internal/install ./internal/recovery
go vet ./internal/hooks ./internal/install ./internal/recovery
```

## Phase 4: Local status and timeline

### Tests first

- Extend `internal/cli/cli_test.go` for `baron timeline`, `--limit`, JSON
  output if supported, unknown command arguments, and local-only behavior.
- Add app-level output tests for active/unresolved task counts, task state,
  verification state, and local Tencent queue/receipt diagnostics.

### Implementation

- Add timeline query methods in `internal/storage` with bounded metadata only.
- Add `Timeline` options and command wiring in `internal/cli/cli.go`.
- Extend app status output to report task ledger and local sync evidence without
  contacting Tencent.
- Keep status and timeline safe when the database is empty, legacy, or missing
  optional evidence.

### Verification

Run:

```text
go test ./internal/cli ./internal/app ./internal/storage
go vet ./internal/cli ./internal/app ./internal/storage
```

## Phase 5: Meaningful remote summaries

### Tests first

- Add hooks/sync tests proving prompts, minor tool events, and long raw outputs
  remain local while task transitions, important test/build evidence, clean
  close, interruption, and explicit handoff produce bounded summaries.
- Preserve redaction, queue idempotency, retry, outage, and deduplication tests.

### Implementation

- Separate local event capture from remote summary eligibility in
  `internal/hooks/hooks.go`.
- Include task ID, status, current step, next action, verification kind/scope,
  completion policy, bounded evidence references, and Git identity in remote
  summaries.
- Keep Tencent records historical/reference-only; never import them as current
  task projections or overwrite local state.
- Reuse the existing queue/retry/receipt path and persist recall fingerprints
  locally.

### Verification

Run:

```text
go test ./internal/hooks ./internal/continuity ./internal/memory/tencent ./internal/storage
go vet ./internal/hooks ./internal/continuity ./internal/memory/tencent ./internal/storage
```

## Phase 6: Release fixture and full verification

### Tests first

- Update the release/update fixture test to derive an ahead-of-current fixture
  version instead of hard-coding the current release.
- Add migration, Linux lifecycle, Windows lifecycle, and release smoke tests
  required by the accepted verification matrix.

### Implementation

- Fix only the dynamic fixture and compatibility gaps exposed by the new tests.
- Update user-facing docs/help for task events, local-only status/timeline,
  conditional Tencent recall, and the explicit task-start handshake.
- Do not expand scope into Tencent cloud/off-machine backup or Codex Desktop.

### Verification

Run the supported matrix:

```text
go test ./...
go vet ./...
go build ./cmd/baron
GOOS=linux GOARCH=amd64 go build ./cmd/baron
GOOS=windows GOARCH=amd64 go build ./cmd/baron
```

Where the Linux runtime is available, run the lifecycle/integration suite on
Linux rather than treating a cross-compiled Windows test binary as Linux test
evidence. Record any environment-only limitation explicitly.

## Commit checkpoints

Use focused commits in this order:

1. `feat: add transactional task ledger`
2. `feat: add deterministic resume gate`
3. `feat: carry task identity through adapters`
4. `feat: add local task timeline`
5. `feat: reduce remote sync to meaningful summaries`
6. `test: make release fixture version dynamic`

Do not amend the two existing spec commits. Do not push unless explicitly
requested.
