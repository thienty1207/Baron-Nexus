# Plan: credential validation and Ubuntu/Debian host bootstrap

> **Execution:** inline in the current `implementation/baron-shared-brain` checkout, preserving the user's untracked `.baron/` state.

## Objective

Implement the approved 2026-08-26 design so first-time Ubuntu/Debian
installation bootstraps the required host tools, sudo authorization is
revalidated safely, and DeepSeek credentials are live-validated before DSH or
Tencent uses them. Add the explicit key-rotation command and keep all
diagnostics secret-safe.

## Task 1 — Freeze the failing contracts first

1. Add tests for `Prompter.VisibleSecret`: injected test readers remain
   deterministic, visible mode uses ordinary line input, hidden `Secret`
   remains unchanged, and the prompt states that input is visible.
2. Add HTTP fixture tests for provider validation: valid 2xx, weak input,
   401/403 rejection, 429/5xx/transport unavailability, bounded URL building,
   and no key value in returned errors.
3. Add app tests proving an existing key is validated and reused, rejected keys
   do not overwrite the old store, a valid replacement is persisted, and the
   third failed attempt stops cleanly.
4. Add CLI tests for `baron deepseek api_key` and its usage errors; retain
   `baron credentials set deepseek` as a compatibility alias.
5. Add install tests for sudo reauthentication and Ubuntu/Debian toolchain
   ordering without running real package changes.
6. Run the focused tests and confirm they fail for the missing behavior.

## Task 2 — Implement provider validation and visible API-key input

1. Add a small credentials validator with typed invalid/unavailable outcomes,
   strict input-shape checks, HTTPS/HTTP URL parsing, bounded `GET /models`,
   and redacted errors.
2. Add `VisibleSecret` to the credentials prompter. Use normal terminal line
   reading only for the explicitly requested DeepSeek API-key path; retain
   hidden input for admin credentials.
3. Add an injectable app validator with the live validator as the production
   default, so tests never contact a provider.

## Task 3 — Wire validation into DSH, Tencent, readiness, and rotation

1. Validate existing DSH keys before reuse; prompt visibly and retry only for a
   rejected/missing key. Persist only after validation succeeds.
2. Validate the resolved Tencent provider key before returning runtime config;
   preserve the old managed `.env` until the candidate is valid.
3. Add readiness classification for rejected versus unavailable DSH provider
   credentials without printing key material. Keep `baron test` read-only.
4. Add `baron deepseek api_key`, atomically update the DSH store and
   managed Tencent API-key fields, and document environment override behavior.
5. Add a safe replacement helper for existing Tencent managed env fields and
   ensure its backup/permission behavior is covered by tests.

## Task 4 — Implement sudo-safe Ubuntu/Debian host bootstrap

1. Add a shared sudo command wrapper that performs one interactive `sudo -v`
   reauthentication and one retry after a cached-ticket authorization failure,
   without receiving the password.
2. Add an injectable Ubuntu/Debian host bootstrap for supported Node/npm/npx,
   pnpm, and uv/uvx. Use signed package/release paths, checksum verification
   for uv, and explicit version checks. Reject unsupported distributions before
   network work.
3. Make `baron install` run host bootstrap before DSH/Codex/Tencent setup; keep
   Windows on the existing manual Docker Desktop/WSL/Ubuntu boundary.
4. Upgrade `install.sh` so it performs the same Linux preflight and host
   bootstrap on a fresh clone before installing/using the Baron binary. Keep
   destination safety, release checksum verification, and no-password-capture
   checks intact.
5. Add shell contract coverage proving sudo preflight precedes every first
   download and that no password variable or echo is introduced.

## Task 5 — Documentation, roadmap, and verification

1. Update README Ubuntu/Debian and Windows flows with the new one-time
   credential prompts, visible-key warning, rotation command, and dependency
   bootstrap behavior.
2. Add the new work to `Baron-Nexus Implement Roadmap.md`; mark only locally
   evidenced tasks `[X]` and leave live/clean-machine/Windows gates truthful.
3. Run `gofmt`, `go test -count=1 ./...`, `go vet ./...`, shell syntax and
   repository checks, and build/check release artifacts.
4. Run a real local binary smoke for version/help/read-only diagnostics where
   the environment allows it. Record provider/Docker/sudo limitations without
   claiming external acceptance.

## Completion rule

Do not claim 100% external acceptance merely because local fixtures pass. The
feature is implementation-complete only when all requested local code and
installer tests pass; live provider, clean-machine, and Windows evidence must
remain separately labeled.
