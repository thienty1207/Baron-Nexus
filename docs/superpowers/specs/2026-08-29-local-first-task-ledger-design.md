# Local-First Task Ledger and Conditional Remote Recovery

## Status

Approved for implementation. This document extends the existing Baron Nexus
architecture; it authorizes only the implementation sequence and scope defined
below, not a rewrite of the existing system.

## Goal

Upgrade Baron Nexus into a local-first continuity system that can preserve and
resume multiple Codex and DeepSeek Harness (DSH) tasks without treating
Tencent as current-state authority or querying it on every prompt.

The target behavior is:

    Git/working tree + SQLite event/task state = current truth
    Tencent = historical and recovery reference
    same-machine handoff = local-first, normally local-only
    remote recall = conditional, bounded, cached, and asynchronous where possible

## Scope

This change includes:

- A durable SQLite-backed Task Ledger projected from canonical structured task
  events.
- A deterministic Resume Gate for unresolved-task recovery and overlap checks.
- Local-first context construction with conditional Tencent recall.
- Meaningful-transition Tencent summaries and existing local retry semantics.
- Human-readable local baron status evidence and a local baron timeline view.
- Codex and DSH adapter changes needed to carry task IDs and structured task
  evidence.
- Migration, privacy, unit, integration, Linux lifecycle, and release-fixture
  coverage.

This change does not include:

- A full raw Codex or DSH transcript archive.
- Hidden reasoning, system prompts, API keys, or unrestricted tool output.
- An always-on Baron daemon or terminal/process surveillance.
- A Tencent cloud deployment or off-machine disaster-recovery system.
- Automatic overwrite of local source, Git state, SQLite state, or checkpoints
  from Tencent.
- An LLM call to decide task boundaries, dependency overlap, or remote-recall
  eligibility.

## Authority and conflict policy

The system has separate authorities for separate facts:

| Fact | Authority | Rule |
| --- | --- | --- |
| Current source and working-tree state | Git and the repository filesystem | Inspect before trusting historical claims. |
| Current lifecycle, task, event, and checkpoint state | SQLite WAL journal and its projections | Durable local writes happen before remote work. |
| Readable emergency snapshot | .baron/checkpoint.json | Materialized fallback only; never the sole authority. |
| Historical summaries and recovery references | Tencent Memory/Knowledge services | Read-only reference for local decisions; never an automatic overwrite source. |

When sources disagree, Baron keeps the local state, records the conflict as
evidence, and labels the Tencent result historical. Reconciliation may import a
remote record as an untrusted historical fact, but may not mutate current Git,
the working tree, or the local task projection without a new local structured
event and local verification evidence.

## Existing architecture to preserve

The implementation must extend these existing boundaries rather than create a
second memory system:

- internal/storage remains the SQLite WAL journal, migration, event, queue,
  and receipt owner.
- internal/continuity remains the WorkState, checkpoint, Git evidence, and
  context-packet owner.
- internal/hooks remains the local-first lifecycle boundary for Codex and DSH.
- internal/recovery remains responsible for interruption and handoff packets.
- internal/memory/tencent remains an HTTP MemoryBackend implementation.
- internal/install remains responsible for adapter/configuration material.
- internal/cli owns evidence-view commands without requiring an LLM.

The current event journal, checkpoint materialization, redaction, idempotency,
project isolation, remote queue, and handoff mechanisms must be reused.

## Canonical task events

Task boundaries must be explicit structured events. Prose in a user prompt or
assistant response may provide descriptive text, but it must not be the primary
source for creating or splitting tasks.

The canonical event vocabulary must include:

    task_started
    task_updated
    task_failed
    task_blocked
    task_verified
    task_completed
    task_interrupted

Every canonical task event requires a non-empty task_id. The task event payload
should support these bounded fields:

    task_id
    goal
    status
    current_step
    next_action
    source_client
    session_id
    git_head
    changed_files
    module_paths
    dependencies
    verification_ref
    verification_kind
    verification_scope
    latest_error_ref
    completion_verified
    completion_policy

Large command output and transcript content remain event-journal evidence and
are referenced by bounded IDs or summaries from the ledger. They are not copied
into every task row.

Non-task events may attach to an active task only through an explicit task_id or
active_task_id supplied by the adapter or runtime. If no task ID is available,
Baron records the event as unassigned evidence and does not infer multiple task
boundaries from prose. A compatibility path may create one session-scoped
legacy task for pre-ledger state, but it must mark that task as legacy and must
not split it heuristically.

The adapter contract must provide an explicit task-start handshake. If an
upstream client does not provide a task ID, the adapter or Baron runtime may
generate one only while handling an explicit structured task_started event and
must attach that ID to later events in the same selected task scope. It must
not generate a new task ID merely because a prompt contains a new paragraph or
because a session emits another assistant response.

The local project/session context stores one explicit active_task_id for routing
non-task evidence. Changing that selection requires a structured task event;
the runtime must not switch active tasks based only on prose or event arrival.

## Task Ledger projection

The Task Ledger is a transactional projection of the append-only event journal.
Inserting a new task event and updating its task projection must occur in the
same SQLite transaction, with the existing idempotency key preventing duplicate
projection updates.

Each task record must contain at least:

    task_id
    project_id
    goal
    status
    current_step
    next_action
    source_client
    last_session_id
    created_at
    updated_at
    git_head
    changed_files
    module_paths
    dependencies
    completion_verified
    completion_policy
    verification_event_id or verification_ref
    latest_verification_kind
    latest_verification_scope
    latest_error_event_id or latest_error_ref

The implementation may normalize repeated path/module/dependency values into
child tables instead of JSON columns. The schema choice must preserve indexed
lookup by project, status, task ID, path, module, and dependency.

Required status values are:

    planned
    in_progress
    completed
    failed
    blocked
    interrupted

The existing task enum does not currently include interrupted; the migration
must add it without invalidating existing records.

Transition rules:

- task_started creates a new task as in_progress by default, or as planned when
  the structured event explicitly declares status=planned. It resumes an
  existing unresolved task with the same task_id. Repeating an idempotent
  task_started event does not create a duplicate. A completed task cannot be
  reopened by a task_started event; a future reopen transition must be
  explicit and separately specified.
- task_updated updates an existing task only. An unknown task_id is a
  validation failure and must not create a task, including when the payload
  contains status=planned. It changes only explicitly supplied fields and
  preserves prior evidence when the update is partial. Planned work must be
  represented by task_started status=planned.
- task_failed, task_blocked, and task_interrupted preserve the task as
  unresolved and record the latest evidence reference.
- task_verified requires a verification reference to local evidence with
  exit_code=0 where the evidence is command-based, a declared
  verification_kind, and observations against the task's current Git head and
  diff state. It records verification evidence and updates the latest
  verification fields, but it does not set completion_verified unless the
  verification kind satisfies the task's completion policy.
- task_completed is accepted as completed only when a verification event with a
  kind allowed by the task's completion policy is present, the evidence is
  current, and the Git/diff identity still matches the current task state. An
  unverified or stale completion event is still retained in the journal but
  cannot promote the projection to completed.
- Session close, process stop, Ctrl+C, power loss, or missing hook events never
  promote a task to completed.

Supported verification kinds are unit, integration, build, acceptance, and
completion. The default completion policy requires kind=completion. A task may
explicitly declare an acceptance completion policy at task_started; that
policy must be stored on the task and evaluated deterministically. A unit,
integration, or build verification can be successful evidence without making
the whole task completion_verified=true.

## Local-first write path

For every lifecycle event:

1. Redact secrets and normalize the adapter payload.
2. Insert the event into SQLite with project/session/task identity and an
   idempotency key.
3. Project task and WorkState changes in the same SQLite transaction as the
   event insertion, or atomically recover/replay the projection before the
   transaction is acknowledged.
4. Inspect Git evidence for events that can change repository state.
5. Materialize the readable checkpoint.
6. Return the local handoff/resume response without waiting on Tencent.

Only meaningful transitions should be queued for Tencent. The default remote
summary set is:

- verified task completion;
- task failure or block;
- important test/build evidence;
- task checkpoint/update;
- clean session close;
- interrupted session;
- explicit handoff.

Raw file reads, minor tool calls, prompts, and long tool results remain local.
Critical failure summaries may be queued immediately; other summaries may be
batched. Remote delivery remains asynchronous, idempotent, bounded, and
fail-open. A Tencent outage must not prevent the local agent session from
continuing.

## Deterministic Resume Gate

The Resume Gate runs before remote recall and must not call an LLM. It consumes:

- current Git HEAD, branch, dirty/clean state, and diff evidence;
- local WorkState and latest checkpoint;
- unresolved Task Ledger rows;
- changed files, module/package paths, and declared dependencies;
- latest test/error evidence;
- current client and session identity;
- the new task's explicit task ID and structured scope when available.

The gate returns a bounded decision with one of these outcomes:

    local_sufficient
    remote_recovery_required
    overlap_requires_resume
    unrelated_work_allowed
    insufficient_structured_task_scope

Local evidence is sufficient only when the local project identity, SQLite
schema, current WorkState, and relevant Task Ledger rows are readable; Git
inspection has completed for the current repository; and the requested task
has enough explicit scope for the overlap check. A local checkpoint is stale
when its Git head or diff identity differs from the inspected repository, when
the active session is interrupted, when required task evidence is missing, or
when the local schema/projection cannot be read. A wall-clock age alone must not
force a remote query.

Overlap is determined in this order, using strong evidence before weak evidence:

1. Exact intersection of strong changed source/module files.
2. Same strong Go package/module, frontend package, database migration area, or
   crawler/backend/frontend module path.
3. Shared declared dependency or lockfile/module dependency.
4. Relevant overlap in Git diff/repository state.

Source files, module/package paths, migration areas, and explicit dependency
references are strong evidence. README/docs, lockfiles by themselves,
generated files, and shared metadata/configuration are weak evidence. A
weak-only intersection must not produce overlap_requires_resume without
additional strong file, module, dependency, or diff evidence. If only weak
evidence is available, the gate should return unrelated_work_allowed or
insufficient_structured_task_scope according to whether the requested scope is
otherwise explicit; it must not auto-block the new task.

The initial deterministic implementation must use repository-native evidence
available without a model, including Go module/package paths, frontend
workspace/package paths, database migration directories, crawler/backend/
frontend roots, dependency manifests, and lockfiles. If a dependency graph
cannot be resolved safely, the gate returns
insufficient_structured_task_scope instead of guessing.

An unresolved task affecting the new task's files, module, or dependency must
be resumed and verified first. An unrelated unresolved task remains visible in
the backlog but does not block new work. Ambiguous scope produces an explicit
warning and asks the agent to identify the task scope; it must not silently
claim either completion or overlap.

The Resume Gate must never mark a task complete. It only determines which
evidence and unresolved work the next agent must see first.

## Recall policy and token budget

Local context is built first from SQLite, WorkState, Task Ledger, latest test
evidence, and current Git inspection. Same-machine handoff and normal work use
local-only context when this evidence is sufficient.

Tencent may be queried only when:

- local continuity is missing;
- local evidence is stale or incomplete;
- unresolved work needs historical evidence not present locally;
- cross-machine recovery is explicitly requested;
- the user explicitly requests historical recall/search;
- a major client/session transition cannot be resolved locally.

The following conditions must hold:

- No normal remote query for every user_prompt.
- At most one remote recovery query per session and recovery fingerprint,
  unless the fingerprint materially changes.
- Cache by project_id, session_id, git_head, normalized query hash, and remote
  snapshot/version metadata.
- Persist the last remote snapshot, receipt, and recall fingerprint locally so
  status views do not need to contact Tencent.
- Deduplicate local and Tencent records by event ID, task ID, content hash, and
  Git head where available.
- Produce one bounded final context packet, prioritizing current local task
  state and using Tencent only for historical additions.
- Label all Tencent records as historical/reference-only.

The remote-query decision is a deterministic local function. No model call is
used to decide whether another model call or Tencent search is needed.

## Resume context contract

At session start and handoff, the agent receives a compact structured packet:

    Project: <name/id>
    Repository: <branch/head/dirty state>

    Verified completed:
    - <task id>: <goal>; verification=<evidence>

    Unresolved:
    - <task id>: status=<failed|blocked|interrupted|in_progress>
      current_step=<step>
      next_action=<action>
      latest_test=<command>; exit_code=<code>
      changed_files=<bounded list>

    Resume decision: <local-only|remote-recovery|overlap-warning>
    Next safe action: <deterministic summary>

The packet must remain within a fixed character/token budget. Local current
state is listed before any historical Tencent record. The packet must not
contain secrets, hidden reasoning, system prompts, or unrestricted output.

## Human evidence views

baron status must expose the current local evidence without an LLM or Tencent
request, including:

- project identity and repository path;
- Git branch, HEAD, dirty/clean state, and diff summary;
- active task and unresolved task counts;
- unresolved task IDs, statuses, current steps, and next actions;
- latest client/session state;
- latest test/build command, status, exit code, and evidence timestamp;
- changed files and latest error reference;
- completion verification state;
- last Tencent sync state and pending queue count.

Add baron timeline [--limit N] as a local-only chronological view of bounded
event metadata. It may show event time, client, event type, task ID, session ID,
status, command/test name, exit code, and short summary, but never secrets or
unrestricted tool output. A JSON form may be added if it uses the existing
structured-output conventions.

## Adapter and compatibility contract

Codex and DSH adapters must map their native lifecycle payloads into the
canonical Baron task event schema while preserving unknown upstream fields only
within the bounded/redacted event evidence boundary.

The adapters must carry task_id through session, tool, test, checkpoint,
failure, handoff, and close events. If an upstream lifecycle event has no task
ID, the adapter may attach an explicitly selected active task ID, but may not
invent task boundaries from assistant prose.

Existing non-task hooks remain accepted for backward compatibility. They update
local evidence and the legacy current task projection only when an explicit
task association is available.

The current Codex CLI hook support remains the validated boundary. Codex Desktop
lifecycle support is not added by this specification.

## Migration and failure behavior

The SQLite migration must be transactional and idempotent. It must:

- preserve all existing events, checkpoints, queues, receipts, and project IDs;
- add task tables, indexes, status support, and recall-cache metadata;
- project one clearly marked legacy task from existing current WorkState when
  necessary, without heuristic splitting;
- leave historical events unclassified unless an explicit association exists;
- preserve existing remote idempotency keys and redaction behavior.

If SQLite is unavailable or malformed, Baron must fail with an actionable local
diagnostic and must not replace it with Tencent data. If Tencent is unavailable,
the local event/task/checkpoint path continues and remote work remains queued.

## Privacy and recovery boundaries

The existing secret-redaction boundary remains mandatory. The system must not
store or sync API keys, credentials, hidden reasoning, system prompts, or raw
unbounded transcripts. Long tool output is retained only as bounded local
evidence when needed for a test/error reference.

Tencent containers running on the same machine are not an off-machine disaster
backup. Cross-machine or off-machine backup/export is explicitly deferred to a
later phase.

## Verification plan

The implementation is not complete until these behaviors are covered:

- multiple tasks project correctly from structured task events;
- completed, failed, blocked, interrupted, and in-progress tasks coexist;
- unverified completion cannot promote a task to completed;
- unit, integration, and build verification can pass without setting
  completion_verified when the task completion policy requires completion;
- task_updated with an unknown task_id fails validation and never creates a
  task; planned work is created only by task_started status=planned;
- interrupted sessions preserve unresolved tasks;
- Codex-to-DSH local handoff works without Tencent when local evidence is
  sufficient;
- DSH-to-Codex local handoff works without Tencent when local evidence is
  sufficient;
- overlapping unresolved work blocks/resumes before a related new task;
- unrelated unresolved work remains in backlog while independent work proceeds;
- weak-only file intersections do not automatically block a new task;
- normal same-session prompts do not repeat Tencent recall;
- missing task IDs do not cause task boundaries to be inferred from prose;
- verification evidence with a mismatched Git head or diff cannot complete a
  task;
- duplicate task_started events do not duplicate a task or reopen a completed
  task;
- materially changed recovery fingerprints permit a new remote recall;
- local and Tencent duplicate records appear once in the final context;
- Tencent outage preserves local progress and queues remote summaries;
- queued remote summaries flush after Tencent recovery;
- baron status and baron timeline use local state only;
- baron status reports Tencent sync from local receipts/queue metadata and does
  not perform a Tencent request;
- release/version update fixtures remain ahead of the current version without
  hard-coded bump regressions;
- Linux lifecycle/integration coverage passes where the required runtime is
  available;
- go test ./... and go vet ./... pass in the supported CI environment;
- Linux and Windows release builds pass.

The acceptance scenario is Codex completing Task A, failing Task B, starting
Task C, being interrupted, and DSH later receiving verified Task A plus the
failed and interrupted evidence for Tasks B and C. Task B must be resumed first
only when its deterministic scope overlaps the requested work.

## Implementation sequence after approval

1. Add contracts, SQLite migration, Task Ledger projection, and transition
   tests.
2. Add deterministic Resume Gate, local context prioritization, remote recall
   cache, and no-repeat tests.
3. Update Codex/DSH adapters and handoff payloads with structured task IDs and
   evidence.
4. Extend status, add timeline, and add local-only output tests.
5. Change remote sync to meaningful summaries while preserving queue/retry and
   redaction tests.
6. Fix the dynamic release fixture and run the full Linux/Windows verification
   matrix.

Production implementation follows this specification's sequence and TDD
verification plan. Scope remains limited to local-first reliability/evidence;
Tencent cloud/off-machine backup and Codex Desktop integration remain deferred.
