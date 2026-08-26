# Baron Nexus credential validation and host bootstrap design

## Status

Approved in chat on 2026-08-26 for implementation.

## Goal

Make first-time Ubuntu/Debian onboarding genuinely automatic while keeping
provider credentials usable and diagnosable. A valid stored credential is
reused without prompting on later project opens. A missing or rejected
credential is collected through an explicit repair flow and is never persisted
until a live provider check succeeds.

## User-visible contract

The supported Linux flow is:

```bash
git clone https://github.com/thienty1207/Baron-Nexus.git
cd Baron-Nexus
./install.sh
cd /path/to/project
baron install
```

Before any installer download or package-manager network operation, Baron
invokes `sudo -v` through the user's terminal. Baron never reads, stores, or
prints the sudo password. If the cached sudo ticket expires or an operation
returns an authorization failure, Baron requests a fresh `sudo -v` and retries
the bounded operation once; a failed reauthentication stops with an actionable
message.

On Ubuntu/Debian, the host bootstrap installs or verifies Docker Engine and
Compose, a supported Node/npm/npx toolchain, pnpm, and uv/uvx. Docker remains
usable through sudo by default; Baron does not silently add the user to the
root-equivalent `docker` group. Unsupported Linux distributions and Windows
receive truthful manual-prerequisite guidance.

## Credential behavior

- DeepSeek provider keys are validated against the configured
  OpenAI-compatible base URL (`GET /models`). The default is
  `https://api.deepseek.com/v1`.
- Empty, newline-containing, whitespace-containing, or trivially short values
  such as `123` are rejected before a network request.
- HTTP 2xx means valid. HTTP 401/403 and malformed values mean rejected. HTTP
  429, 5xx, timeout, and transport failures mean provider unavailable; they do
  not cause a replacement key to overwrite an existing valid key.
- Existing valid DSH/Tencent provider credentials are reused and do not prompt
  again. Existing rejected credentials trigger at most three interactive
  replacement attempts. The previous value remains untouched until a
  replacement validates.
- `baron deepseek api_key` is the explicit rotation command. It
  validates first, atomically updates DSH's official credential store, and
  updates Baron-managed Tencent runtime key fields when that deployment exists.
  The older `baron credentials set deepseek` spelling remains a compatibility
  alias.
- DeepSeek API-key entry is intentionally visible because the user explicitly
  requested full terminal echo on a trusted personal machine. The prompt warns
  about this. The entered value is never copied into Baron logs, JSON
  diagnostics, receipts, project state, command arguments, or assistant output.
  Tencent admin keys continue to use hidden input.
- `baron test`, `status`, and `doctor` remain read-only. They may perform a
  bounded validation request, but never prompt or mutate credentials.

## Persistence and recovery

DSH credentials remain in the official DSH `$DSH_HOME/.credentials.yaml`
version-1 store with mode `0600`. Tencent runtime keys remain in the managed
deployment `.env` with mode `0600`; edits preserve unrelated upstream/user
settings and create the existing recoverable sibling backup. All writes are
atomic and symlink-safe. Environment variables continue to take precedence,
so users who want rotation to persist must remove an overriding
`DEEPSEEK_API_KEY` from the launching environment.

## Testing contract

The implementation must include deterministic tests for visible-vs-hidden
prompt behavior, weak-key rejection, 2xx/401/429/5xx/network validation
classification, no-overwrite-on-failure rotation, DSH/Tencent atomic writes,
sudo reauthentication, Ubuntu/Debian host dependency ordering, and the CLI
command. Existing local tests, vet, formatting, shell checks, and release
artifact checks must remain green. Live provider, clean-machine, and Windows
runtime evidence is reported separately and is not inferred from fixtures.
