# Baron Nexus

Baron Nexus is a native Go project continuity sidecar for DeepSeek Harness and Codex.
It records local work state first, routes bounded project memory to TencentDB
Agent Memory, and lets either agent recover unfinished work without a Baron
daemon.

## Which command should you use?

Baron has three different operations. `./install.sh` installs the command,
`baron install` initializes the current project, and `baron update` only updates
the installed command.

### 1. First time on a machine: `./install.sh`

Run this once on a new machine. It downloads and verifies the native Baron
binary into `~/.local/bin`; it does not initialize DSH, Codex, Tencent, or a
project:

```text
git clone https://github.com/thienty1207/Baron-Nexus.git
cd Baron-Nexus
sudo -v
./install.sh
export PATH="$HOME/.local/bin:$PATH"
baron --version
```

The expected output is `baron 0.1.1`. The installer verifies the release
manifest and SHA-256 list before writing the binary. It refuses to replace an
existing binary unless replacement is explicitly enabled. Windows users use
the release's `install.ps1` with PowerShell after installing the documented
Windows prerequisites.

### 2. First time in a project: `baron install`

After the command exists, run this once for each project. It verifies or
refreshes the Baron binary, then bootstraps Docker (supported Ubuntu/Debian),
DSH, Codex, Tencent, and the current project's Baron state:

```text
cd /path/to/project
sudo -v
baron install
```

The sequence is idempotent, so rerunning it repairs missing Baron-owned pieces
without replacing project identity or user-owned agent configuration.

### 3. Later version refresh: `baron update`

Use this when a newer Baron release is available. It is binary-only: it does
not run Docker, DSH, Codex, Tencent, or project setup, and the default
`~/.local/bin` installation does not require sudo:

```text
baron update
```

`baron update` is idempotent when the installed version is already current.
Both `baron install` and `baron update` validate the release manifest, SHA-256,
and candidate `baron --version` output before atomic replacement; a failed
validation keeps the prior binary recoverable. They never overwrite project
source, `.baron` identity, checkpoints, credentials, or Tencent state.

## Quick start (new Ubuntu/Debian machine)

Quick start is only the combined example of the two first-time operations
above; it is not a fourth command:

```text
git clone https://github.com/thienty1207/Baron-Nexus.git
cd Baron-Nexus
sudo -v
./install.sh
export PATH="$HOME/.local/bin:$PATH"
cd /path/to/project
sudo -v
baron install
```

`./install.sh` asks the native `sudo` program to authorize the operation before
it downloads release metadata or the binary. Baron never receives, echoes, or
stores the sudo password. If sudo is unavailable, the installer stops before
network activity and prints the remediation. After the binary exists, the
second `sudo -v` in the same terminal gives `baron install` an active sudo
authorization; Baron verifies it non-interactively and never reads the
password. `baron install` then performs the project bootstrap described above.

The initializer commands remain available separately for advanced repair:

```text
baron deepseek-harness init
baron codex-cli init
baron tencent-memory init
baron test
```

Credentials are requested only when genuinely missing. Baron asks for DSH and
Tencent provider/admin values with terminal echo disabled, writes the DeepSeek
key to DSH's official `$DSH_HOME/.credentials.yaml`, and reuses it for Tencent.
Codex ChatGPT sign-in remains owned by Codex: if global Codex auth is absent,
Baron prints one action to run `codex` and complete sign-in; once completed,
that auth is reused across projects and later launches. Baron does not ask for
the ChatGPT password or copy OAuth secrets. `baron test` is always read-only and
never prompts. For automation or CI, provide the provider key through the
environment instead:

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
through the same hidden prompt and keeps it in the managed protected store for
future idempotent runs. Baron never prints provider, admin, or user keys and
never stores them in project state.

If an initializer is launched without a terminal and a required value is
missing, it stops before Docker/Tencent downloads and reports the exact
environment variable to set. On Windows, install Docker Desktop, WSL2, Ubuntu,
and the required Tencent service prerequisites manually first; Baron reports
this prerequisite without silently claiming to install Windows components.

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
