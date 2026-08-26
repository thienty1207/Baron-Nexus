# Baron Nexus automated credential bootstrap design

## Status

Approved in chat on 2026-08-24 for implementation. The 2026-08-26
`credential-and-host-bootstrap-design.md` supersedes this document where the
user-visible DeepSeek API-key prompt is concerned: provider-key entry is now
intentionally visible on the user's trusted personal terminal, while the
no-log/no-state/no-diagnostics secret boundary remains unchanged.

## Goal

Keep the existing Baron command surface while removing manual credential-file
editing from the Linux/DSH/Tencent onboarding path. When a required provider
credential is absent, Baron must ask for it once through a hidden terminal
prompt, write it only to the provider-owned or Baron-managed protected store,
validate the resulting runtime, and continue the same initialization command.

## User-visible flow

The supported sequence remains:

```bash
baron deepseek-harness init
baron codex-cli init
baron tencent-memory init
baron test
cd /path/to/project
baron setup
```

`baron test` stays read-only and never prompts. Initializers and repair may
prompt only when they have a terminal and a value is genuinely missing. A
non-interactive invocation fails before network/state mutation with the exact
environment variables needed to continue.

### DSH

- Detect an inherited `DEEPSEEK_API_KEY` first, then the DSH credential store.
- If no key exists, prompt with terminal echo disabled.
- Write the key to DSH's supported `$DSH_HOME/.credentials.yaml` version-1
  `refs` layout, mode `0600`, using an atomic preserve-first merge.
- Preserve unrelated DSH refs/records and reject an invalid existing document
  instead of overwriting it.
- Run a bounded real headless probe after initialization; never print the key
  or persist it in Baron project/global state, receipts, logs, or diagnostics.

### Tencent Agent Memory

- Resolve explicit `BARON_TENCENT_*` environment values first, then an
  existing managed deployment `.env`, then a reusable provider key already
  present in the DSH credential store or managed Tencent `.env`.
- For the normal DeepSeek path, default the OpenAI-compatible URL and model;
  prompt only for a missing API key. Custom providers remain configurable by
  environment variables or hidden prompts for URL/model when the defaults are
  not suitable.
- Ask for an external admin key only when the managed deployment cannot supply
  its `.admin-key`; keep that value in process memory and never persist it.
- Perform sudo/OS preflight before any download, then write the protected
  Tencent managed `.env`, install/update Docker on supported Ubuntu/Debian,
  fetch the pinned deployment, start the complete stack, verify every health
  surface, provision/reuse Baron identity, and apply restart policy.
- On failure, clean/rollback only newly created Baron-owned state and preserve
  existing Tencent data and user configuration.

### Codex and Windows boundary

Codex ChatGPT OAuth remains the supported Codex-owned sign-in flow; Baron can
detect its result and trust configuration but must not pretend to automate a
third-party browser login. Windows continues to report the required Docker
Desktop/WSL/Ubuntu/Tencent actions instead of silently modifying UI-managed
system components.

## Components

- `internal/credentials/`: hidden prompt, terminal/non-terminal policy,
  atomic secret input handling, and redacted diagnostics.
- `internal/install/dsh_credentials.go`: DSH credential-store discovery,
  version-1 YAML merge, permission checks, and provider-key reuse.
- `internal/install/tencent_env.go`: managed runtime configuration loading,
  precedence, defaults, and missing-value classification.
- `internal/install/linux.go` and `internal/app/app.go`: no-network sudo
  preflight, prompt-before-download ordering, initialization orchestration,
  and rollback boundaries.
- CLI wiring: injectable input for tests and safe non-interactive behavior;
  command names and machine-readable output remain compatible.

## Security invariants

1. No secret appears in command arguments, shell history, stdout/stderr,
   errors, receipts, project state, Git-tracked files, or test diagnostics.
2. DSH credentials use DSH's own protected store; Tencent provider credentials
   use the managed deployment `.env`; Baron user keys remain protected global
   or project bindings as already specified.
3. All secret files are owner-only on Unix and use best-effort restrictive ACL
   handling on Windows.
4. Existing user-owned configuration is preserved and invalid credential files
   fail closed.
5. `baron test` remains non-mutating and never asks for a credential.

## Verification and acceptance

The implementation requires red/green unit tests for prompt behavior, DSH YAML
merge, secret redaction, Tencent precedence/defaults, sudo-before-network
ordering, idempotent reruns, and rollback. After local tests pass, run the
available live DSH/Codex/Tencent checks and update the roadmap only with fresh
evidence. Windows and unavailable provider/clean-machine gates remain
explicitly blocked rather than being marked green by fixtures.
