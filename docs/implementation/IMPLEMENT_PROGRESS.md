# Baron Nexus implementation progress

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
- Docker: initially unavailable; subsequently installed through the supported
  Ubuntu/Debian path with sudo preflight. Docker Engine/Compose and the
  `hello-world` smoke test now pass on the development host.
- Node/npm/npx/pnpm/uv/uvx: available on the current host through the user's
  NVM/uv installation; the implementation now bootstraps these tools on a
  fresh supported Ubuntu/Debian host when they are missing.
- Git: `git version 2.53.0`; repository initialized in place because the supplied workspace was not a Git checkout.
- PDF: 36 pages, extracted and visually checked at representative contract, phase, data-contract, and final pages.

## Current verification checkpoint — 2026-08-25

- Roadmap accounting is 399/428 checked (93.22%); 263/269 implementation
  tasks and 128/151 mandatory phase tests are checked. This percentage is a
  checklist measure, not a release approval.
- Fresh `go test -count=1 ./...`, `CGO_ENABLED=0 go test -count=1 ./...`,
  `go vet ./...`, `gofmt -l .`, `git diff --check`, shell syntax, all four
  repository acceptance scripts, and the release checksum audit passed.
- `GOTOOLCHAIN=local CGO_ENABLED=1 go test -race -count=1 ./...` was attempted
  and failed only because the host has no `gcc`; no C compiler alternative is
  installed. The race gate remains BLOCKED rather than being inferred green.
- Host checks requested for this run both returned
  `sudo: interactive authentication is required`. Docker CLI is installed but
  the daemon socket is inaccessible to this terminal.
- The requested `/home/ty/.local/bin/baron test --json` and the rebuilt
  current-source release artifact both returned `ready:false`, exit `12`, with
  only Docker daemon and sudo incomplete/unavailable. Four direct Tencent
  `/health` probes returned HTTP `200`.
- The installed `/home/ty/.local/bin/baron` hash differs from the fresh
  worktree artifact; the requested installed-binary command was still run, and
  all source-change validation used the rebuilt artifact rather than silently
  treating the older installed binary as current.
- The current source added a health-first repair path. Its regression test
  passed, and the rebuilt binary ran real `baron tencent-memory init`, `setup`,
  and `repair` without sudo when the managed services were healthy. Tencent
  still reports the current Wiki as `processing`; Baron keeps the local status
  `pending` and does not claim asynchronous completion.

## Phase ledger

| Phase | Status | Tasks completed | Files changed | Commands/tests | Failures/fixes | Remaining limitations |
| --- | --- | --- | --- | --- | --- | --- |
| 0 | PASS | P0-T01..P0-T12; acceptance and package skeleton created; Go contract layer added | `cmd/baron/`, `internal/contracts/`, package docs, `go.mod`, `go.sum`, docs, acceptance contract, `.gitignore` | `go test ./...`, `go vet ./...`, CGO-free Linux amd64 and Windows amd64 builds, no `target/` | Go was absent from PATH; used pinned `/usr/local/go/bin/go1.27.0` | No external runtime services are required by Phase 0; live integrations are later gates |
| 1 | PASS | P1-T01..P1-T10; Cobra command surface, exit codes, JSON flags, atomic writes, backups, redaction, global state | `internal/cli/`, `internal/config/`, `internal/install/` | CLI/config/install package tests; native binary `--help` and setup smoke | Initial Cobra method mismatch fixed; dependency versions resolved by Go module tooling | Real upstream installers remain environment-gated |
| 2 | PASS | P2-T01..P2-T10; SQLite WAL schema, idempotent events, checkpoint engine, hook stdin/stdout runtime, bounded logs foundation | `internal/storage/`, `internal/continuity/`, `internal/hooks/` | SQLite concurrency/idempotency tests, 10,000-event multi-process test, checkpoint/Git drift tests, duplicate/malformed hook tests | SQLite per-connection busy behavior fixed with WAL busy timeout plus in-process writer serialization | Cgo race run is blocked by the host toolchain; CGO-free tests pass |
| 3 | PASS | P3-T01..P3-T10; readiness matrix and secret-safe diagnostics | `internal/doctor/`, CLI app wiring | Docker missing/stopped, Codex auth incomplete, all-fixture-green doctor tests; `baron test --json` smoke | None after probe fixture corrections | Live Tencent and interactive auth remain unavailable; Docker is now installed on the development host |
| 4 | PASS | P4-T01..P4-T12; pinned DSH install, plugin/profile merge, live safe startup, profile repair, DuckDuckGo MCP row, embedded Baron adapter bundle, compatibility metadata, receipts, and automated credential bootstrap | `internal/install/`, `internal/credentials/`, `adapters/dsh/`, `internal/install/assets/`, `configs/compatibility.json` | Real DSH init/probe/profile repair and DuckDuckGo MCP smoke pass; the current P28 flow validates a missing/rejected DeepSeek key before writing DSH's official mode-0600 store, with intentional visible entry on the trusted terminal; no-key noninteractive mode fails before install | DSH's upstream no-key response is `MISSING_CREDENTIAL`; P4-TEST06 now treats that canonical result as equivalent to the actionable readiness contract | No manual credential-file editing is required; provider rotation is tracked by P28 |
| 5 | PASS | P5-T01..P5-T09; official nested Codex hook merge, explicit adapter bridge, trust diagnostic, CLI-only Desktop boundary, fail-open response, runtime secret redaction, global state/repair | `adapters/codex/`, `internal/install/`, `internal/app/`, `internal/hooks/` | Hook shape, pinned-version fixture, existing-host Codex reuse, real `baron codex-cli init` materialization, adapter runtime smoke, trust/config inspection, preservation/idempotency, malformed-input, tamper, and redaction tests, and authenticated interactive Codex hook readback pass | Desktop itself remains outside the supported CLI-only boundary; paired-client acceptance is tracked by P12/P13 | No additional P5 runtime dependency |
| 6 | BLOCKED | P6-T01..P6-T12; native Tencent v3 client, layered reads, identity/agent provisioning, managed deployment bootstrap, safe metadata compensation, and proxy permission recovery | `internal/memory/tencent/`, `internal/install/tencent.go`, `internal/memory/tencent/identity_rollback.go`, `internal/app/` | Strict HTTP fixtures pass; live MemoryCore/Hub/Knowledge/Proxy health and disposable project Agent/Wiki setup pass; `owner_user_id` is sent for live agent creation and stopped proxy config ownership is repaired automatically | Clean-machine init, persistent-volume/restart matrix, and full live service acceptance still require a sudo-authorized runtime | Live P6/P22 acceptance is not marked PASS from one host run |
| 7 | PASS | P7-T01..P7-T11; stable project identity, setup, move behavior, Unicode/space paths, Git-ignore and permission protections | `internal/project/`, `internal/app/`, `internal/config/` | Setup idempotence, move, Unicode, root rejection, symlink/tamper, and permission tests | None in the fresh Go test run | Live multi-agent clients are covered by later blocked gates |
| 8 | BLOCKED | P8-T01..P8-T08; automatic project provisioning and strict Tencent request boundary | `internal/app/`, `internal/project/`, `internal/memory/tencent/` | Strict-isolation fixtures pass; live disposable setup created/reused the Tencent Agent and Wiki, and Git-backed setup created an asynchronous CodeGraph without claiming readiness; the live status probe now uses Tencent's narrow `code_graph_id`-only schema | Five-run identity/isolation and complete CodeGraph readiness still require the clean acceptance matrix | Pending state is queued for repair/retry rather than treated as success |
| 9 | PASS | P9-T01..P9-T10; local-first event journal, checkpoints, recovery packet, interruption/close semantics, offline queue | `internal/storage/`, `internal/continuity/`, `internal/recovery/`, `internal/hooks/` | WAL, checkpoint, interrupted-session, drift, queue, recovery, and redaction tests pass | None in the fresh Go test run | Cross-process race evidence remains toolchain-gated |
| 10 | BLOCKED | P10-T01..P10-T10; native Tencent layered context, local-first sync, bounded remote context, failure fallback | `internal/memory/tencent/`, `internal/continuity/`, `internal/hooks/` | Layered endpoint/isolation and offline queue fixtures pass | Live memory read/write and outage recovery cannot run | Requires Docker/Tencent service and credentials |
| 11 | PASS | P11-T01..P11-T11; Codex hook integration, event mapping, recovery context, fail-open behavior | `internal/hooks/`, `internal/install/`, `internal/app/` | Synthetic Codex hook payload, malformed payload, recovery-context, fail-open tests, and real authenticated interactive Codex hook readback pass | Official Codex hook path was corrected from `~/.config/codex/hooks.json` to `$CODEX_HOME/hooks.json`/`~/.codex/hooks.json`; full DSH handoff remains the separate P12/P13 gate | No additional P11 runtime dependency |
| 12 | PASS | P12-T01..P12-T10; DSH lifecycle adapter, canonical event mapping, profile bundle, MCP rows, and live failure checkpoint | `adapters/dsh/`, `internal/install/assets/dsh-adapter/`, `internal/app/` | Source/embedded/installed adapter checks, profile composition, isolated startup/search probes, authenticated real DSH sentinel retrieval, and a real exit-23 command checkpoint pass | The paired Codex↔DSH takeover remains the separate P13 gate; local outage behavior is fail-open by contract | No additional P12 dependency |
| 13 | PASS | P13-T01..P13-T09; cross-agent continuity contracts, DSH/Codex handoff receipts, context bounds, and verified-completion ordering | `internal/contracts/`, `internal/hooks/`, `internal/recovery/`, `internal/continuity/` | Contract, handoff, recovery, bounded-context, canonical-event tests, live killed-Codex→DSH recovery, live DSH-unverified→Codex handoff, and real clean-close/final-ordering probe pass | DSH can emit `session_clean_closed` before its trailing `assistant_final`; runtime now preserves the closed state for that same session | No P13-specific acceptance remains; full web/clean-machine release matrix remains tracked by P19/P22/P27 |
| 14 | BLOCKED | P14-T01..P14-T09; concurrency, duplicate delivery, abrupt interruption/SIGKILL recovery, bounded logs | `internal/storage/`, `internal/continuity/`, `internal/hooks/`, `internal/recovery/` | Concurrent claims, duplicate delivery, multi-process journal, child-process SIGKILL recovery, live outage queue recovery, real simultaneous DSH/Codex state-integrity evidence, and health-first repair regression pass | Race detector remains unavailable because gcc/cgo is absent | Requires gcc/cgo race execution |
| 15 | BLOCKED | P15-T01..P15-T07; project isolation, identity binding, credential separation, redaction | `internal/project/`, `internal/memory/tencent/`, `internal/config/`, `internal/app/` | Ten-project identity, ambiguous-agent, tamper, symlink, secret-persistence, and HTTP-isolation tests pass | Legacy/live Tencent namespace separation cannot run | Requires Tencent deployment and multiple authenticated project environments |
| 16 | BLOCKED | P16-T01..P16-T09; backup/restore, explicit safe replacement, checksum validation, secret exclusion, update rollback, Windows ACL adaptation | `internal/app/`, `internal/install/`, `install.ps1` | Backup secret exclusion, checksum-corruption rejection, conflict-safe restore with retained rollback copy, and binary rollback tests pass | Docker-volume and cross-OS restore tests unavailable | Requires Docker volume and Windows runtime validation |
| 17 | PASS | P17-T01..P17-T10; path/symlink hardening, input validation, secret redaction, bounded output, safe failure paths | `internal/project/`, `internal/config/`, `internal/hooks/`, `internal/memory/tencent/`, `internal/install/` | Security/permission, tamper, redaction, malformed input, bounded log, and contract tests pass; `go vet ./...` passes | None in local static/test evidence | External dependency security review remains outside this offline run |
| 18 | BLOCKED | P18-T01..P18-T13; release build, platform artifacts, checksums, `0.1.1` version contract, verified `baron install/update`, installer/update rollback, mapping preservation, and publication gate | `scripts/`, `install.sh`, `install.ps1`, `.github/workflows/release.yml`, `internal/release/`, `internal/version/`, `internal/cli/` | CGO-free Linux amd64 and Windows amd64 builds, local GitHub-release fixture, public Linux asset download/checksum/version validation, atomic rollback, and idempotent update pass; first shell installer and Windows runtime remain unverified | Requires Windows execution, first-installer sudo acceptance, and networked installer test |
| 19 | BLOCKED | P19-T01..P19-T16; final acceptance ledger, release candidate, all client/service gates | docs, release scripts, all implementation packages | Local acceptance contract, full Go tests, vet, builds, checksums, release-gate refusal, live 10-project isolation, live outage queue recovery, killed-Codex recovery, reverse unverified handoff, verified-completion ordering, and real concurrent DSH/Codex integrity pass | External gates remain unavailable: clean-machine Tencent lifecycle, backup/restore clean OS, Windows runtime, full CodeGraph/Skill release journey, and cgo race | Final status cannot be PASS until those gates are executed |
| 28 | BLOCKED | P28-T01..P28-T10; provider validation, visible DeepSeek input, protected credential rotation, sudo reauthorization, and Ubuntu/Debian host dependency bootstrap | `internal/credentials/`, `internal/install/host.go`, `internal/app/`, `internal/doctor/`, `install.sh`, docs | Provider, rotation, sudo-ordering, checksum, Go, vet, race-container, shell syntax, installer-preflight, and platform-guidance evidence pass locally | Clean Ubuntu/Debian first-install and real published-provider wrong-key/replacement/outage acceptance remain unavailable | Keep P28-TEST05..06 unchecked until those external runs are recorded |

## Fresh local verification

Run from the repository root with Go 1.27.0:

- `GOTOOLCHAIN=local /usr/local/go/bin/go test -count=1 ./...` — PASS.
- `GOTOOLCHAIN=local /usr/local/go/bin/go vet ./...` — PASS.
- `GOTOOLCHAIN=local CGO_ENABLED=0 /usr/local/go/bin/go test -count=1 ./...` — PASS.
- The same fresh run also passed `gofmt -l .`, `git diff --check`, both
  platform/branding scans, release checksum comparison, and the release gate
  remained correctly `BLOCKED` only because external P19/P22/P27 gates are
  still unchecked.
- `git diff --check` — PASS.
- Existing host `codex-cli 0.149.0` was reused without npm in an isolated `baron codex-cli init` smoke; the Baron-owned explicit adapter, receipts, and official nested hooks were materialized successfully.
- Real authenticated Codex v0.149.0 interactive PTY evidence passed after the hook-path correction: `SessionStart` and `UserPromptSubmit` executed and the seeded Baron sentinel was returned by the model.
- Real verified-completion ordering evidence passed: Codex emitted clean-close
  before its trailing final event, DSH headless took over without an interruption
  packet, returned the live sentinel, and the checkpoint remained
  `clean_closed`/`completed`/`completion_verified=true`.

## Fresh verification on 2026-08-25

- Fresh full Go test, vet, CGO-free test, formatting, diff, no-`target/`, shell
  syntax, branding, platform, DSH adapter, and release-gate fixture checks
  passed.
- The DSH adapter test script had a missing executable bit (`0664`); it was
  corrected to `0755`, and direct execution now passes. This was a test-harness
  file-mode defect, not a production adapter failure.
- A CGO race run was attempted and remains host-blocked: `go test -race` needs
  cgo, while `CGO_ENABLED=1 go test -race ./...` reports that `gcc` is absent.
- Native release verification passed in
  `/tmp/baron-release-0.1.1.final.akD6Ay`: Linux ELF and Windows PE32+ artifacts,
  manifest, SBOM, toolchain metadata, installers, and all seven checksum entries verified;
  the Linux artifact also passed isolated setup/status/hook smoke.
- Four live Tencent health endpoints returned HTTP 200 and the managed Compose
  configuration parsed successfully. The current agent terminal still cannot
  access `/var/run/docker.sock`, and non-interactive sudo is unauthorized.
- A fresh `baron tencent-memory init` rerun exited `0` through the intended
  health-first idempotent path without requesting sudo because those endpoints
  were already healthy; this is not clean-machine bootstrap evidence.
- A live disposable setup rerun reached Wiki `ready`, drained its local queue
  to zero pending/dead-letter items, and retained CodeGraph `local_only` when no
  Git remote was verified. A Git-backed disposable project retained an honest
  CodeGraph `processing`/`pending` state after Tencent returned `409 busy`.
- DSH headless/web probes and a Codex CLI exec probe returned their exact
  sentinels; the Codex project checkpoint ended `clean_closed`. These are fresh
  CLI/data-plane probes and do not close the clean-machine, Windows, race,
  restart/volume, or publication gates.

- Live queue repair evidence: a real DSH output over Tencent's 8,192-character
  conversation limit was reproduced by a client test, fixed at the Tencent
  boundary, and covered by a selective dead-letter migration. The rebuilt
  binary replayed six historical size-limit captures through a real hook; the
  project now has zero pending and zero dead-letter queue items.

## Fresh live DSH verification

- Real DSH v0.1.1-rc.2 received a Codex-written sentinel through the Baron
  local-authoritative handoff context. The sentinel was absent from the new
  prompt, and the authenticated web model returned the exact stored value in
  session `session-614ecb57-44eb-4765-a254-b161803aae30`. After the headless
  profile installer repair, a real headless run also returned the exact
  `CODEX_TO_DSH_HEADLESS_SENTINEL_20260824` value from a live Baron Codex
  checkpoint, proving the adapter is active outside the web UI as well.
- A real DSH bash tool invocation ran
  `bash -lc 'printf DSH_CANONICAL_FAILURE_SENTINEL_20260824 >&2; exit 23'`.
  Session `session-65be473e-4cc6-43d3-b679-05450fb8420b` produced a durable
  `assistant_final` checkpoint with the actual command, stderr marker,
  `status=failed`, `class=tool_error`, and `exit_code=23`.
- Live paired-client recovery now covers both directions. A real Codex
  `tool_response`/`last_assistant_message` payload was normalized into the
  canonical failure fields; a controlled process watcher killed Codex after
  the exit-23 `tool_finished` event and before final/clean-close, and real DSH
  recovered the checkpoint and reran the command with exit code 0. In the
  reverse direction, DSH emitted `status=not_yet_verified` and real Codex
  returned only the exact sentinel without inventing pass/failure. A separate
  live concurrent DSH/Codex run recorded interleaved events with SQLite
  integrity `ok`, no duplicate idempotency keys, and zero pending/dead-letter
  queue items.

Candidate release evidence is recorded in
`docs/implementation/FINAL_ACCEPTANCE_REPORT.md`. The final status remains
BLOCKED until the external gates above are available.

## Fresh verification on 2026-08-26

- The public GitHub `v0.1.1` manifest, checksum list, and Linux amd64 binary
  were downloaded and verified with `sha256sum -c`; the manifest source
  revision is `3d9e76961e2faaa63ee3b2cfaa5d8c3df9231036`.
- The release client now clones the shared HTTP client and gives release
  metadata/artifact downloads a two-minute timeout instead of inheriting the
  three-second Tencent/API timeout. The regression test was observed failing
  before the fix and passing after it; full `go test ./...` and `go vet ./...`
  also pass.
- A real `/home/ty/project-test` `baron install` using the public fixed binary
  passed release download and stopped at the expected interactive sudo
  boundary. A real `baron update` reported `0.1.1` already current. The clean
  first-installer and Docker/Tencent bootstrap gates remain unchecked.

## Fresh verification on 2026-08-26 — credential and host bootstrap

- Current-source local verification passed: `GOTOOLCHAIN=local
  /usr/local/go/bin/go test -count=1 ./...`, `go vet ./...`, `gofmt -l .`,
  `git diff --check`, `sh -n install.sh`, the installer-preflight script, the
  platform-guidance scripts, the branding script, the release-gate fixture,
  and the executable Bash DSH-adapter test.
- The full race suite passed in the official `golang:1.27-bookworm` container
  with GCC: `GOTOOLCHAIN=local /usr/local/go/bin/go test -race -count=1 ./...`.
  The host itself still has no `gcc`, so the container is the recorded race
  environment.
- A temporary binary built directly from this worktree reports `baron 0.1.1`.
  Its live `baron test --json` reached Docker, Node/npm/npx, pnpm, uv/uvx, DSH,
  Codex, DSH provider validation, and all Tencent health/knowledge surfaces;
  it returned `ready:false`, exit `11`, only because sudo authorization was
  not cached in the agent terminal. No key value appeared in the diagnostic.
- A current-source `baron install` probe stopped at the native sudo preflight
  before the release download when the terminal could not authenticate. This
  confirms the ordering contract but is not clean-machine bootstrap evidence.
- The official uv latest Linux archive/checksum path was downloaded and the
  archive contained both regular `uv` and `uvx` binaries; the host bootstrap
  keeps checksum verification before installation. The public release-gate
  script still correctly exits `21` while P19/P22/P27 external gates remain
  unchecked.

## Current Baron Nexus expansion (P20-P28)

| Phase | Status | Fresh local evidence | Remaining external evidence |
| --- | --- | --- | --- |
| 20 | PASS | Nexus rebrand, compatibility lock, public-text scan, command/help smoke | None for the local rebrand contract |
| 21 | PASS | Ubuntu/Debian detection, sudo-before-network, official Docker apt plan, idempotent repair, restart policy, and Windows guidance fixtures | Clean-machine Ubuntu/Debian matrix and user-specific sudo scenarios |
| 22 | BLOCKED | Pinned Tencent checkout, safe runtime env, complete stack orchestration, health surfaces, identity, immutable deployment manifest, update rollback fixtures, and automated credential reuse/prompting | Pinned checkout and deployment scripts pass local checks; all four managed health endpoints returned HTTP 200; live Project A/B setup reused distinct Agent bindings and Wiki reached `ready` after the `owner_user_id` payload fix; the live CodeGraph status call now passes Tencent's narrow schema and retains an actual asynchronous `pending` index honestly; provider/admin credentials are loaded, reused, or collected without manual `.env` editing; current-project raw Wiki source listing returns 10 files while Tencent still reports Wiki `processing`; health-first repair regression and real repair pass | Clean-machine bootstrap, five-run identity/stack acceptance, service restart/volume checks, and CodeGraph completion remain open |
| 23 | PASS | Strict isolated v3 Memory/Knowledge/Skill/metadata route fixtures and capability diagnostics | Live Tencent API compatibility |
| 24 | PASS | Stable registry, five-run reuse, remote reconstruction, same-name isolation, bounded Wiki seed and CodeGraph sync fixtures | Live Tencent asset provisioning |
| 25 | PASS | Bounded context packets, relevant CodeGraph slices, DSH/Codex symmetry, stale-knowledge boundary, outage fail-open fixtures | Authenticated DSH/Codex handoff |
| 26 | PASS | Typed queues, retries/dead-letter, freshness, secret corpus, backup/restore, stale-lease recovery, SIGKILL, concurrency and rollback fixtures; live disposable-project outage recovery and live lifecycle queue drain also passed | Real service restart/volume and clean-machine power-loss acceptance |
| 27 | BLOCKED | Linux/Windows quick-start guidance, native artifacts, SBOM/checksums, rollback metadata, final acceptance report, and release/report hash agreement | Historical public Linux `v0.1.1` download/update and release checksum verification pass; the `v0.1.2` candidate is prepared for tagged rebuild; live user command sequence, first-installer sudo flow, clean Ubuntu, Windows runtime, and full DSH/Codex/Tencent scenarios remain open |
| 28 | BLOCKED | Validated provider credentials, visible DeepSeek prompt policy, atomic DSH/Tencent rotation, bounded sudo reauthorization, and Ubuntu/Debian Docker/Node/pnpm/uv bootstrap | Local Go/fixture/race-container and shell evidence pass; clean first-install and published-provider replacement/outage evidence remain open |

P20, P21, P23, P24, P25, and P26 are local-contract PASS only. P22, P27, and
P28 remain BLOCKED by the explicitly listed live dependencies; no mock fixture
is counted as a live Tencent, provider, or authenticated-agent acceptance pass.
