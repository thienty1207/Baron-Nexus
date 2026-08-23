# Baron Shared Brain

Baron is a native Go project continuity sidecar for DeepSeek Harness and Codex.
It records local work state first, routes bounded project memory to TencentDB
Agent Memory, and lets either agent recover unfinished work without a Baron
daemon.

## Quick start

Docker is a user-installed prerequisite for TencentDB Agent Memory.

```text
baron deepseek-harness init
baron codex-cli init
baron tencent-memory init
baron test
cd /path/to/project
baron setup
```

Use `dsh web` or `codex` normally after setup. Baron hooks are short-lived and
invoke the Go binary on demand.

## Project files

`.baron/project.toml` is the commit-safe stable project identity.
`.baron/.env` contains project-local Tencent runtime values, is Git-ignored,
and is mode 0600 on Unix. `.baron/runtime/state.db` is the local SQLite WAL
authority; `checkpoint.json` is a readable materialized snapshot.

## Support operations

`baron status`, `baron doctor`, `baron repair`, `baron backup <destination>`,
and `baron restore <archive>` are support operations. A backup intentionally
excludes plaintext credentials; re-keying may be required after restore.

## Troubleshooting

`baron test --json` reports missing Docker, Node/npm/npx, uv/uvx, DSH, Codex
authentication, Tencent services, and Baron-owned component readiness without
printing credentials. Docker, ChatGPT sign-in, and DeepSeek credential setup
remain user-owned actions.

Baron cannot reconstruct uncommitted source bytes after disk destruction from
memory alone; use Git or a separate source backup for that failure mode.
