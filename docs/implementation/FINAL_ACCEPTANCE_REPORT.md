# Baron Nexus — Final Acceptance Report

## Candidate

- Historical source revision: `3d9e76961e2faaa63ee3b2cfaa5d8c3df9231036`
  (published `v0.1.1` release candidate with the public-download timeout fix).
- Working tree: the `v0.1.4` release candidate plus the P28 credential flow and
  P29 latest-at-run dependency refresh;
  user-local
  `.baron/` state remains untracked and intentionally excluded from the
  release.
- Candidate version: `0.1.4`.
- The historical `v0.1.1` artifact hashes and directory remain in the dated
  evidence below; the tagged `v0.1.4` workflow rebuilds artifacts from this
  candidate commit.
- Toolchain: `go1.27.0 linux/amd64`
- Historical `v0.1.1` release directory: `/tmp/baron-release-0.1.1.fixed.gOuFUD`
- Historical `v0.1.1` Linux SHA-256: `5ff679186e5c8c5f11c2a0d5698c7732d9fdb8d6ed961bf36a1fdb6802752d88`
- Historical `v0.1.1` Windows SHA-256: `da843103489c0eadf53bb34b536b8deb82e956b9262f08808bbab6116cb23b58`
- Historical `v0.1.1` release manifest SHA-256: `357aa14268a4c540584e704fd15529b6958c689ac17555eb2146b365de8e398f`
- Historical `v0.1.1` toolchain file SHA-256: `76227025cc0bc2be7067aa45d11e09cacfd49c58f498f4c2e4f6a9872a607bf9`
- Historical `v0.1.1` SBOM SHA-256: `a3e949d6f9cb2c9e3cfd72e346db408050a0530f8953f5aa6e382e777747f84c`

## Implemented surface

- Native Go CLI with the frozen command surface, stable exit codes, JSON
  readiness output, atomic configuration writes, backup/restore, and binary
  rollback support.
- Project-local identity and state under `.baron/`, including restrictive
  `.env` permissions, Git-ignore merging, move-stable IDs, symlink/tamper
  rejection, SQLite WAL journaling, durable queue state, checkpoints, and
  recovery packets.
- Local-first hook processing for Codex and DSH with canonical events,
  bounded/redacted logs, idempotency, fail-open client responses, and strict
  project/team/agent/user isolation on remote requests.
- Latest-at-run DSH installation/profile integration, embedded Baron DSH adapter,
  DuckDuckGo MCP profile patch, official nested Codex hook shape, Tencent v3
  client, identity provisioning, managed deployment bootstrap code, and
  provider-credential bootstrap through the official DSH/Tencent stores. The
  DeepSeek provider key is intentionally visible only while actively entered
  in the trusted terminal; Tencent admin-key input remains hidden.
- Ubuntu/Debian sudo-first Docker bootstrap, immutable Tencent deployment
  manifests with container image digests, complete v3 Wiki/CodeGraph/Skill/
  metadata clients, automatic project knowledge registry/provisioning, and
  bounded cross-agent context retrieval.
- Durable typed repair queues, stale-lease recovery after interrupted
  processes, knowledge-aware backup/restore, expanded secret corpus checks,
  and separate Wiki/CodeGraph/tools operational diagnostics.
- Codex hook trust/configuration diagnostics with a tested CLI-only Desktop
  boundary, opaque-ID-only Tencent identity compensation, controlled
  child-process SIGKILL queue recovery, explicit safe restore replacement with
  retained rollback copy, Windows installer ACL adaptation, and an explicit
  Codex adapter bridge with a real interactive SessionStart/UserPromptSubmit
  runtime smoke.
- Linux amd64 and Windows amd64 CGO-free release artifacts, checksum manifest,
  module SBOM, shell/PowerShell installers, and update rollback logic.
- Stable `baron 0.1.4` version output, verified GitHub release metadata and
  SHA-256 download protocol, atomic Linux `baron install`/`baron update`,
  idempotent update behavior, and staged Windows restart guidance.
- One-command `baron install` bootstrap coordinator: native host preflight,
  DSH, Codex, Tencent, and current-project setup in deterministic order, with
  visible validated DeepSeek input only when missing/rejected, hidden admin-key
  input, and reusable Codex auth.
- Canonical `baron deepseek api_key` credential-rotation command, with the
  older `baron credentials set deepseek` spelling retained as an alias.

## Expansion phase checklist

- P20 rebrand and compatibility lock: local gate PASS.
- P21 Ubuntu/Debian Docker bootstrap and sudo preflight: local gate PASS;
  Docker Engine/Compose and `hello-world` were verified on the development
  host. P28 extends the same boundary to Node/npm/npx, pnpm, and uv/uvx.
- P4 live DSH initialization/profile repair, safe startup, and DuckDuckGo MCP
  search: PASS. P28's interactive provider flow intentionally shows the
  DeepSeek key while it is typed on the trusted terminal, validates it against
  `/models`, and writes only the validated value to DSH's official mode-0600
  store; admin keys remain hidden.
- P22 full Tencent deployment: managed health endpoints and live disposable
  Agent/Wiki setup PASS after the real `owner_user_id` payload correction;
  clean-machine sudo bootstrap, persistent-volume/restart, CodeGraph
  completion, and authenticated release acceptance remain pending.
- P23 full Tencent API/knowledge client: local gate PASS.
- P24 automatic project knowledge provisioning: local gate PASS.
- P25 DSH/Codex context orchestration: local gate PASS.
- P26 reliability/security/migration: local queue, recovery, backup, secret,
  concurrency, SIGKILL, and rollback evidence PASS; live Tencent outage
  recovery also passed on a disposable project, while service restart and
  clean-machine power-loss cases remain pending.
- P27 release/documentation: local artifact and guidance evidence PASS; final
  publication gate is implemented and correctly returns BLOCKED while the
  external acceptance gates below remain unchecked.
- P28 validated credentials and Ubuntu/Debian first install: local
  implementation, provider/rotation fixtures, sudo ordering, checksum,
  full Go/vet/race-container, shell, installer-preflight, and guidance checks
  PASS; clean-machine and published-provider replacement/outage acceptance
  remain pending.
- P29 latest-at-run dependency refresh and `0.1.4` release: local resolver,
  host/Docker refresh, version, documentation, and focused fixture work pass;
  artifact/public-release verification and clean Ubuntu/Debian, Windows, and
  full live dependency acceptance remain pending until recorded.

## Verification evidence

The following commands passed from the repository root:

- `GOTOOLCHAIN=local /usr/local/go/bin/go test -count=1 ./...`
- `GOTOOLCHAIN=local /usr/local/go/bin/go vet ./...`
- `GOTOOLCHAIN=local CGO_ENABLED=0 /usr/local/go/bin/go test -count=1 ./...`
- `git diff --check`
- `sh -n install.sh scripts/build-release.sh`
- Release `SHA256SUMS` verification for both binaries, manifest, toolchain,
  and SBOM files.
- Linux artifact `--help` smoke test.
- Isolated temporary-project smoke: `setup`, JSON `status`, Codex hook input,
  project-local state creation, and mode `0600` `.env`.
- Isolated `baron codex-cli init` smoke reused the existing host
  `codex-cli 0.149.0` without npm and materialized the Baron-owned adapter,
  receipts, and official nested hook entries.
- The Codex hook installer now targets the official `$CODEX_HOME/hooks.json`
  path (default `~/.codex/hooks.json`); a real authenticated Codex v0.149.0
  interactive PTY session executed `SessionStart` and `UserPromptSubmit` and
  returned `CODEX_INTERACTIVE_CANONICAL_HOOK_SEEN` from a seeded Baron memory
  sentinel. The prior `~/.config/codex/hooks.json` path was the cause of the
  earlier false “hooks ready” result.
- Installer SHA verification and refusal to overwrite an existing binary
  without `BARON_ALLOW_REPLACE=1` (exit 20).
- `baron --version` exact-output smoke, local HTTP-fixture `baron install` and
  `baron update` verification, checksum/manifest/version rejection before
  mutation, and app-level proof that release operations do not touch project
  or Tencent state.
- `scripts/check-branding_test.sh`, `scripts/check-platform-guidance_test.sh`,
  and `scripts/check-release-gate_test.sh`.
- `scripts/check-install-preflight_test.sh`, which proves Linux sudo
  authorization is checked before the first release download and no password
  variable or echo path exists in `install.sh`.
- Provider validation fixtures cover visible DeepSeek input, weak-key rejection,
  HTTP invalid/unavailable classification, Authorization forwarding, and
  secret-safe errors. DSH replacement and Tencent managed `.env` rotation
  fixtures prove no-overwrite-on-failure, atomic writes, backups, and mode
  `0600` permissions.
- Ubuntu/Debian host-bootstrap fixtures prove unsupported distributions stop
  before work, native sudo reauthorization is bounded, package/download work
  follows preflight, and uv/uvx installation requires a matching checksum.
- Fresh 2026-08-26 verification also passed the full race suite in
  `golang:1.27-bookworm` with GCC. A current-source `baron 0.1.1` smoke passed
  Docker, DSH, Codex, provider validation, and Tencent health/knowledge checks;
  only uncached sudo made the read-only report return exit `11`.
- Focused and full tests for the ordered `baron install` bootstrap plus
  state-aware Codex login notice: an authenticated global Codex store produces
  no repeat sign-in notice, while missing auth produces one actionable notice.
- Fresh 2026-08-25 direct `scripts/check-dsh-adapter_test.sh` execution after
  correcting its missing executable bit; the adapter test passed.
- Fresh `docker compose --env-file docker/env.example config --quiet` passed
  for the upstream Knowledge compose contract. Running that upstream file
  without its documented env file correctly fails on required `PUBLIC_URL`;
  this is not counted as a managed-stack pass. Direct Docker daemon access was
  blocked by the current user's socket permissions and both non-interactive
  sudo checks required authentication.
- A fresh `baron tencent-memory init` rerun exited `0` through the designed
  health-first idempotent path because all four endpoints were already healthy;
  this is rerun evidence only and does not count as clean-machine bootstrap.
- Fresh readiness output was intentionally incomplete in this terminal:
  `baron test --json` returned `ready:false` and exit `12` for Docker daemon and
  sudo, while the user-level DSH/Codex/Tencent surface checks remained ready.
- A current-source regression test and real rebuilt-binary run proved that
  `baron repair` skips Docker bootstrap/deployment when all four Tencent health
  endpoints are already healthy, allowing Baron-owned queue repair without a
  second sudo authorization. The current Wiki remains honestly
  `processing`/local `pending` at Tencent, with ten raw seed entries, so no
  asynchronous Wiki completion is claimed.
- A fresh real DSH `0.1.1-rc.2` profile dump contained Superpowers, Reverse
  Skill, Baron adapter, and DuckDuckGo markers; `dsh web --no-open` emitted a
  local URL before its bounded timeout. A fresh race attempt failed at
  `runtime/cgo` because `gcc` is absent.
- Fresh DSH headless and bounded web-start probes passed, and a fresh Codex CLI
  `exec` probe returned its exact sentinel in a disposable Baron project. The
  project checkpoint ended `clean_closed`; the output itself was kept out of
  this report to avoid copying session contents.
- Fresh live disposable setup reached Wiki `ready` with zero pending/dead-letter
  queue items when no Git remote was present. A separate Git-backed disposable
  setup retained CodeGraph `processing`/`pending` after Tencent returned `409
  busy`; no asynchronous operation was falsely marked ready.
- The current candidate was rebuilt from this worktree after the canonical
  Codex hook-path, legacy Tencent nested-source fallback, Knowledge batching,
  queue-schema normalization, and automatic lifecycle retry fixes; its Linux
  and Windows SHA-256 values are recorded above and its release checksum file
  verifies both binaries, both installers, the manifest, toolchain metadata,
  and the SBOM.
- Live Tencent evidence covered two disposable projects. Repeated setup on
  each reused its project binding; Project A and Project B had different
  project IDs and Tencent Agent IDs, while both remained under the same Baron
  team. Wiki status reached `ready` on both setups.
- A real authenticated Codex/DSH CLI acceptance completed in Project A. The
  installed Codex hook path produced a handoff context containing the prior
  client, unfinished goal, failing test, task status, and safe next action; a
  controlled Codex kill after `tool_finished` was recovered and rerun by DSH,
  while a DSH `status=not_yet_verified` checkpoint was returned exactly by
  real Codex. This is live CLI hook/data-plane evidence; the full interactive
  DSH web workspace selection remains the external UI gate.
- A real verified-completion ordering probe passed after the lifecycle fix:
  Codex marked the task verified and cleanly closed, DSH headless took over,
  returned the exact live sentinel without an interruption packet, and the
  final local checkpoint remained `clean_closed`, `completed`, and
  `completion_verified=true` even though DSH emitted its trailing final event
  after clean-close.
- A live capture/readback probe confirmed a Baron capture is readable through
  Tencent L0 conversation query. The decoder now consumes Tencent's
  `data.messages` conversation envelope. Baron also now treats Tencent
  `scenario/read` as path-addressed and only sends it when `ScenarioPath` is
  supplied, preventing a generic recall from becoming a false outage.
- A live current-project setup reproduced Tencent Knowledge's legacy nested
  raw-source `ENOENT`; Baron retried only that known failure with flat aliases,
  and `wiki/raw/ls` then confirmed all ten bounded seed files were uploaded.
  The rebuilt client also batches the service's ten-file limit, and a real
  lifecycle hook drained the previously pending Wiki/core/scenario queue
  without manual SQLite edits: `pending_queue=0`, `dead_letter_queue=0`, Wiki
  and ingest `ready`. A missing L2 Scenario file now falls back to L0 instead
  of retrying permanently. This repository has no verified Git remote, so its
  local CodeGraph state remains correctly deferred.
- A real Git-backed disposable project then exercised the Tencent CodeGraph
  status endpoint. The first request was rejected because that upstream route
  accepts only `code_graph_id`; Baron now sends the narrow schema, the live
  status check succeeds, and the actual asynchronous index remains visibly
  `processing`/`pending` until Tencent completes it. No pending CodeGraph is
  falsely reported as ready.
- Live isolation evidence covered ten disposable projects: all ten project IDs
  and all ten Tencent Agent IDs were unique, and a direct Project B query for a
  Project A sentinel returned zero cross-project matches.
- Live outage evidence disabled Tencent for a disposable project, captured a
  Codex hook into the local queue, restored the endpoint, and flushed four
  receipts with four unique idempotency keys. The final pending and
  dead-letter counts were both zero; replaying the original event did not add a
  second delivery.
- Fault/concurrency evidence passed the controlled child-process SIGKILL and
  abrupt-restart recovery fixtures. A live DSH tool was deliberately kept
  running while a real Codex prompt interleaved on the same project; both
  lifecycle streams were recorded, SQLite integrity remained `ok`, duplicate
  idempotency keys were 0, and pending/dead-letter queue items were 0. A
  separate watched Codex process was killed after its exit-23 tool result and
  DSH recovered/reran the exact checkpoint command with exit 0.

Post-publication verification on 2026-08-26 downloaded the public `v0.1.1`
manifest, checksum list, and Linux binary from GitHub; `sha256sum -c` passed
for the downloaded artifacts and the manifest embeds the candidate source
revision above. The same fixed binary completed the release portion of a real
`baron install` before the current terminal's interactive sudo boundary, and
`baron update` reported the version already current.

The release directory contains a static Linux ELF and a Windows PE32+ binary;
the manifest embeds the candidate source revision above.

## Blocked acceptance gates

These are environment limitations, not converted mock passes:

- Docker is installed and the daemon/Compose smoke test passed on this Linux
  host. The managed Tencent stack is healthy and a live disposable project
  setup has completed; the current agent terminal has no cached sudo ticket,
  so a fresh clean-machine `tencent-memory init`, volume/restart matrix, and
  full remote continuity run were not forced past preflight.
- In the 2026-08-25 agent terminal, `sudo -n docker info` and `sudo -n -v`
  both returned `sudo: interactive authentication is required`; direct Docker
  client access consequently returned a socket permission error. This is the
  current external gate, not a release pass.
- The same fresh run of `/home/ty/.local/bin/baron test --json` returned
  `ready:false` and exit `12`; the rebuilt current-source artifact returned the
  same result. Docker CLI, Node/npm/npx/uv/uvx, DSH, Codex auth/hooks, and the
  four Tencent health surfaces were ready, while Docker daemon and sudo were
  not.
- The installed `/home/ty/.local/bin/baron` now matches the public fixed Linux
  release artifact (`5ff679186e5c8c5f11c2a0d5698c7732d9fdb8d6ed961bf36a1fdb6802752d88`)
  and reports `baron 0.1.1`. The public manifest, checksum list, and binary
  were downloaded from GitHub and verified successfully.
- From `/home/ty/project-test`, the real `baron install` command using that
  fixed binary passed the release download stage and then stopped at the
  expected native host boundary: `sudo: A terminal is required to
  authenticate`. It no longer fails with the former three-second release
  timeout. A subsequent real `baron update` returned `Baron is already up to
  date at version 0.1.1.` without touching project state.
- In disposable `/home/ty/project-test`, real DSH, Codex, and Tencent init plus
  `baron setup` and `baron repair` completed. The project status ended with
  Wiki `ready`, zero pending/dead-letter queue items, and honest CodeGraph
  `local_only` because no verified Git remote exists. A real Codex CLI exec
  returned `CODEX_NEXUS_PROJECT_TEST_OK`; the eight Baron hooks were migrated
  to the Codex-supported three-second timeout without the prior clamp warning.
- The current project Wiki has ten raw seed entries but Tencent still returns
  `status=processing` (version `6`); Baron keeps the local registry at
  `pending`. This is an external asynchronous gate and is not marked complete.
- The user-provided Linux terminal acceptance returned `ready:true` and
  `exit_code:0` for `baron test --json`; Docker/sudo, DSH credentials, Codex
  auth/hooks, and Tencent MemoryCore/Hub/Knowledge were all reported ready.
- The user-installed Node/npm/npx/uv/uvx/DSH toolchain is available through
  the NVM path. A real DSH init, profile repair, safe startup, and DuckDuckGo
  MCP search completed; missing credentials are now collected or reported by
  Baron without manual file editing. Headless DSH and authenticated Codex
  command smokes plus both live CLI handoff directions pass; only the full web
  workspace selection remains external.
- Windows execution and PowerShell installer runtime checks are unavailable;
  the repository only verifies that Windows guidance does not claim silent
  Docker Desktop, WSL2, Ubuntu, or Tencent UI installation.
- `go test -race ./...` cannot run: the default invocation requires cgo, and
  `CGO_ENABLED=1` fails because `gcc` is absent.
- P28-TEST05 remains open because this agent terminal cannot provide the
  user's interactive sudo authorization for a clean first-install machine.
  P28-TEST06 remains open because no published-release wrong-key/replacement
  matrix was claimed from local fixtures.
- P29-TEST03 remains open because no clean Ubuntu/Debian machine was available
  for a fresh published-installer run. P29-TEST04 remains open because no
  Windows runtime is available. P29-TEST05 remains open because full live
  latest-dependency/provider/CodeGraph/Wiki/Skill acceptance is not inferred
  from local fixtures.

## Final status

FINAL STATUS: BLOCKED

Roadmap checklist completion is 416/447 (93.06%); implementation tasks are
276/282 (97.87%) and mandatory phase tests are 132/157 (84.08%). These are
transparent checklist percentages, not a release-readiness override.

The local implementation and release evidence are ready for external
acceptance, but this is not a public release decision until the remaining
Tencent, authenticated DSH/Codex, clean-machine Ubuntu, and Windows runtime
scenarios are executed and recorded in the roadmap.
