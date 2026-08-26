# Automated onboarding evidence

Date: 2026-08-24

This record covers the credential/bootstrap changes made after the original
roadmap evidence. It contains no provider, admin, or Tencent user key values.

## DSH

- Built `/home/ty/.local/bin/baron` from the current Go worktree.
- Ran `baron deepseek-harness init` with the existing managed provider source
  available to the current user. The command exited `0`, installed/verified
  the pinned DSH baseline, ran the bounded `dsh web --no-open` probe, and did
  not print an action requiring manual credential-file editing.
- The official DSH credential file exists at `$DSH_HOME/.credentials.yaml` and
  its observed Unix mode is `0600`.
- A disposable non-interactive run with no key exited `11`, reported
  `DEEPSEEK_API_KEY`, and left the disposable credential file absent before any
  npm/DSH installation call.

## Tencent-backed project setup

- `GET /health` returned HTTP `200` for MemoryCore (`8420`), MemoryHub
  (`8125`), Proxy (`8096`), and Knowledge (`8424`) on the current host.
- The first live `baron setup` against a disposable project exposed a real
  Tencent API mismatch: `/v3/meta/agent/create` required `owner_user_id`.
  Baron now sends that owner field and the rerun exited `0`.
- The successful live result reused/created a project Agent and Wiki under the
  Baron team. A Git-backed project also created a CodeGraph and reported its
  asynchronous `processing`/`pending` state instead of claiming it was ready;
  Wiki ingest reached `ready` on a subsequent setup rerun.
- A live CodeGraph sync returned Tencent `409 busy` while cloning. Baron kept
  the operation queued and exposed the pending state for repair/retry; no
  false completion was recorded.
- The user then ran the complete Linux readiness sequence with cached sudo:
  `sudo -v`, a sudo Docker probe, and `/home/ty/.local/bin/baron test --json`.
  The reported result was `ready:true`, `exit_code:0`, with DSH credentials,
  Codex auth/hooks, and MemoryCore/MemoryHub/Knowledge all green.
- A real authenticated Codex CLI command completed in Project A. The Baron
  hook path produced a recovery context for an unfinished failing test, and a
  DSH takeover hook consumed that context. The evidence proves the local
  adapter/data-plane boundary; the full interactive DSH web workspace test is
  still an external gate.

## Local verification

- `go test ./... -count=1` — PASS.
- `go vet ./...` — PASS.
- `CGO_ENABLED=0 go test ./... -count=1` — PASS.
- `gofmt -l .` — clean.
- `git diff --check` — PASS.
- `scripts/check-platform-guidance.sh` — PASS.

## Remaining external gates

The current agent terminal has no cached sudo authorization, so the clean
machine Docker/Tencent init path was intentionally not forced past its
preflight. Windows runtime/installer execution, authenticated real Codex↔DSH
handoff, legacy Tencent snapshots, Docker-volume migration, and final release
publication remain blocked until those environments are available.

## Fresh verification rerun — 2026-08-25

- `/home/ty/.local/bin/baron test --json` returned `ready:false`, exit `12`;
  only Docker daemon and sudo were incomplete/unavailable. The rebuilt
  current-source release artifact produced the same truthful result.
- `baron tencent-memory init`, `baron setup`, and the fixed `baron repair`
  were run against the current healthy managed endpoints. Repair no longer
  requests sudo or redeploys a healthy stack; its regression test and real
  binary run passed.
- The current Tencent Wiki remains explicitly `processing`/local `pending`
  after read-only polling, so this rerun does not mark the asynchronous Wiki
  gate complete. Ten raw seed entries are present.
- Fresh release artifacts were built as `verify-20260825-fix` for Linux amd64
  and Windows amd64 with Go 1.27.0, CGO disabled, SBOM, manifest, and verified
  SHA256SUMS. The race command was attempted but is blocked by missing `gcc`.
- DSH `0.1.1-rc.2` profile markers and a real bounded `dsh web --no-open`
  startup probe passed. Windows runtime, clean Ubuntu bootstrap, Docker
  restart/volume, authenticated full release acceptance, and publication
  remain blocked.

## Fresh credential and host-bootstrap verification — 2026-08-26

- P28 adds visible DeepSeek provider-key entry only while the user is actively
  typing in a trusted terminal; provider validation uses the configured
  OpenAI-compatible `/models` endpoint before DSH/Tencent persistence. Tencent
  admin-key input remains hidden, and diagnostics never include key material.
- Rejected or unavailable candidates do not replace the existing DSH
  credential or managed Tencent `.env`; `baron credentials set deepseek`
  performs explicit validated rotation and preserves unrelated managed values.
- Ubuntu/Debian host bootstrap now installs/verifies Docker Engine/Compose,
  Node/npm/npx, pnpm, and checksum-verified uv/uvx after native sudo
  preflight. Sudo reauthentication is bounded and Baron never receives the
  password. Windows remains an explicit manual Docker Desktop/WSL2/Ubuntu/
  Tencent prerequisite boundary.
- Full Go tests, vet, formatting/diff, shell, installer-preflight, platform,
  branding, release-gate fixture, executable DSH adapter test, and the race
  suite in `golang:1.27-bookworm` passed. A current-source `baron test --json`
  smoke reached DSH/provider/Tencent surfaces; only uncached sudo returned
  incomplete. Clean-machine and published-provider rotation evidence remain
  blocked and are not claimed here.
