# Baron Nexus

Baron Nexus is a native Go project continuity sidecar for DeepSeek Harness and Codex.
It records local work state first, routes bounded project memory to TencentDB
Agent Memory, and lets either agent recover unfinished work without a Baron
daemon.

## Install and update Baron Nexus

The first bootstrap downloads the verified native release. On Linux amd64:

```text
curl -fsSL https://github.com/thienty1207/Baron-Nexus/releases/latest/download/install.sh -o /tmp/baron-install.sh
sh /tmp/baron-install.sh
export PATH="$HOME/.local/bin:$PATH"
baron --version
```

The expected first release output is `baron 0.1.1`. The installer downloads the
release manifest, SHA-256 list, and matching native binary before writing to
`~/.local/bin/baron`; it refuses to replace an existing binary unless the
replacement is explicit. Windows users can download `install.ps1` from the
same release and run it with PowerShell.

After Baron is installed, the binary can refresh itself from the verified
GitHub release channel:

```text
baron install
baron update
```

`baron update` is idempotent when the installed version is already current.
Both commands validate the release manifest, SHA-256, and candidate
`baron --version` output before atomic replacement; a failed validation keeps
the prior binary recoverable. They never overwrite project source, `.baron`
identity, checkpoints, credentials, or Tencent state.

## Quick start

On Ubuntu/Debian, `baron tencent-memory init` checks sudo first and can install
Docker Engine, Compose, and the managed Tencent stack automatically. Windows
prints the Docker Desktop/WSL prerequisite guide without silently changing the
machine.

```text
baron deepseek-harness init
baron codex-cli init
baron tencent-memory init
baron test
cd /path/to/project
baron setup
```

The initializer commands are interactive only when a value is genuinely
missing. Baron asks for provider keys with terminal echo disabled, writes the
DeepSeek key to DSH's official `$DSH_HOME/.credentials.yaml`, and reuses it for
Tencent. `baron test` is always read-only and never prompts. For automation or
CI, provide the key through the environment instead:

```text
export DEEPSEEK_API_KEY=<your-provider-key>
baron deepseek-harness init
baron tencent-memory init
```

`baron tencent-memory init` performs the Linux sudo preflight, installs/starts
Docker on supported Ubuntu/Debian hosts, fetches the pinned Tencent deployment,
and starts MemoryCore, MemoryHub/Panel, Proxy, and the combined Knowledge
Service. It does not open the Panel for manual setup. Proxy values default to
the same provider; custom providers can use the `BARON_TENCENT_*` environment
overrides. If the managed deployment has no admin key, Baron asks for it
through the same hidden prompt and keeps it in process memory only. Baron never
prints provider, admin, or user keys and never stores them in project state.

If an initializer is launched without a terminal and a required value is
missing, it stops before Docker/Tencent downloads and reports the exact
environment variable to set. On Windows, install Docker Desktop, WSL2, and
Ubuntu manually first; Baron reports this prerequisite instead of claiming to
install Windows components silently.

Use `dsh web` or `codex` normally after setup. Baron hooks are short-lived and
invoke the Go binary on demand.

Codex Desktop boundary: Baron currently supports the Codex CLI hook contract
and does not claim a validated Codex Desktop lifecycle. If the CLI reports a
project-hook trust/enablement prompt, approve the Baron project hooks once in
that project and rerun `baron test`; the JSON hook file alone cannot prove an
interactive approval.

`baron codex-cli init` also materializes the Baron-owned Codex adapter bridge.
The normal hooks use the direct Go entrypoint for a path-safe, Node-independent
runtime; the bridge is available for Codex environments that require an
explicit adapter process. It forwards lifecycle payloads to Baron and never
owns Codex skills, provider settings, or authentication.

## Project files

`.baron/project.toml` is the commit-safe stable project identity.
`.baron/.env` contains project-local Tencent runtime values, is Git-ignored,
and is mode 0600 on Unix. `.baron/runtime/state.db` is the local SQLite WAL
authority; `checkpoint.json` is a readable materialized snapshot.

## Support operations

`baron status`, `baron doctor`, `baron repair`, `baron backup <destination>`,
and `baron restore <archive>` are support operations. A backup intentionally
excludes plaintext credentials; re-keying may be required after restore. If a
restore target already exists, Baron stops safely; choose
`baron restore <archive> --replace-existing` only when you want the current
state moved to a recoverable sibling backup first.

## Troubleshooting

`baron test --json` reports missing Docker, Node/npm/npx, uv/uvx, DSH provider
credentials, Codex authentication, Tencent services, and Baron-owned component
readiness without printing credentials. Codex ChatGPT OAuth remains the one
interactive sign-in owned by Codex; Baron only detects its result and tells the
user when Codex asks for project trust.

Baron Nexus cannot reconstruct uncommitted source bytes after disk destruction from
memory alone; use Git or a separate source backup for that failure mode.
