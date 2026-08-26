# Baron Nexus

Current release: `0.1.2` — validated provider credentials and automated
Ubuntu/Debian first-install bootstrap.

Baron Nexus is a native Go project continuity sidecar for DeepSeek Harness and Codex.
It records local work state first, routes bounded project memory to TencentDB
Agent Memory, and lets either agent recover unfinished work without a Baron
daemon.

## Which command should you use?

Baron has three different operations. `./install.sh` installs the command,
`baron install` initializes the current project, and `baron update` only updates
the installed command.

### 1. First time on Ubuntu/Debian: `./install.sh`

Run this once on a new Ubuntu or Debian machine. It downloads and verifies the
native Baron binary into `~/.local/bin` and automatically bootstraps Docker
Engine/Compose, supported Node/npm/npx, pnpm, and uv/uvx. It does not initialize
DSH, Codex, Tencent, or a project yet:

```text
git clone https://github.com/thienty1207/Baron-Nexus.git
cd Baron-Nexus
sudo -v
./install.sh
export PATH="$HOME/.local/bin:$PATH"
baron --version
```

The expected output is `baron 0.1.2`. `sudo -v` must succeed before the first
download. The installer verifies the release manifest and SHA-256 list before
writing the binary, verifies the uv release checksum, refuses to replace an
existing binary unless replacement is explicitly enabled, and never reads or
stores the sudo password. If the sudo ticket expires during a long bootstrap,
the installer asks sudo to authenticate again and retries the operation once.
Docker is managed through sudo; Baron does not add the user to the `docker`
group automatically.

### 1b. First time on Windows: `install.ps1`

Use PowerShell on Windows. Before installing Baron, install and start Docker
Desktop, enable WSL2 with an Ubuntu distribution, and prepare the required
Tencent service prerequisites. Baron does not silently install or control
these Windows UI components.

```powershell
git clone https://github.com/thienty1207/Baron-Nexus.git
Set-Location Baron-Nexus
Set-ExecutionPolicy -Scope Process Bypass
.\install.ps1
$env:Path = "$env:LOCALAPPDATA\Baron;$env:Path"
baron --version
```

The expected output is `baron 0.1.2`. The PowerShell installer downloads the
Windows amd64 release, verifies the release manifest and SHA-256 list, and
installs it at `$env:LOCALAPPDATA\Baron\baron.exe`. It does not require sudo.
If the current PowerShell session still cannot find `baron`, open a new
PowerShell window or run:

```powershell
& "$env:LOCALAPPDATA\Baron\baron.exe" --version
```

### 2. First time in a project: `baron install`

After the command exists, run this once for each project. It verifies or
refreshes the Baron binary, then bootstraps DSH, Codex, Tencent, and the
current project's Baron state. On Ubuntu/Debian it also verifies or installs
the host dependencies listed above before downloading the release or Tencent
services.

#### Ubuntu/Debian

```text
cd /path/to/project
sudo -v
baron install
```

#### Windows PowerShell

```powershell
Set-Location C:\path\to\project
baron install
```

Windows must have Docker Desktop, WSL2, Ubuntu, and the required Tencent
service prerequisites installed and started first. Baron does not silently
install or control those Windows UI components, and Windows does not use the
Linux `sudo -v` preflight.

The sequence is idempotent, so rerunning it repairs missing Baron-owned pieces
without replacing project identity or user-owned agent configuration. On
Ubuntu/Debian, Baron performs `sudo -v` before host/release/Tencent network
work, uses sudo only for system operations, and never receives, echoes, or
stores the password. If authorization expires, Baron requests sudo again once
and stops with a repair instruction if reauthentication fails.

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

The initializer commands remain available separately for advanced repair:

```text
baron deepseek-harness init
baron codex-cli init
baron tencent-memory init
baron test
```

Credentials are requested only when genuinely missing or rejected. Baron
validates a DeepSeek key with the provider's OpenAI-compatible `GET /models`
endpoint before saving it; a short value such as `123` is rejected locally,
and a provider/network outage does not overwrite an existing key. DeepSeek
API-key input is intentionally visible in the terminal, with a warning, as an
explicit trusted-machine choice. The terminal displays the characters while
you type, but Baron never copies the key into logs, JSON diagnostics, receipts,
project state, command arguments, or assistant output. Tencent admin values
still use hidden terminal input. Baron writes the validated DeepSeek key to
DSH's official `$DSH_HOME/.credentials.yaml`, and reuses it for Tencent.
Codex ChatGPT sign-in remains owned by Codex: if global Codex auth is absent,
Baron prints one action to run `codex` and complete sign-in; once completed,
that auth is reused across projects and later launches. Baron does not ask for
the ChatGPT password or copy OAuth secrets. `baron test` is always read-only
and never prompts. To rotate a key later, run this once; it validates before
updating the DSH store and any existing Baron-managed Tencent runtime env:

```text
baron deepseek api_key
```

For automation or CI, provide the provider key through the environment instead
of using the interactive prompt:

```text
export DEEPSEEK_API_KEY=<your-provider-key>
baron deepseek-harness init
baron tencent-memory init
```

An exported `DEEPSEEK_API_KEY` takes precedence over the protected DSH
credential file for that process and its child commands. To rotate the key
interactively, unset that environment override first, then run
`baron deepseek api_key`; the command validates the replacement before
updating the protected stores. The older `baron credentials set deepseek`
form remains available as a compatibility alias.

`baron tencent-memory init` performs the Linux sudo preflight, installs/starts
Docker on supported Ubuntu/Debian hosts, validates the provider key, fetches
the pinned Tencent deployment,
and starts MemoryCore, MemoryHub/Panel, Proxy, and the combined Knowledge
Service. It does not open the Panel for manual setup. Proxy values default to
the same provider; custom providers can use the `BARON_TENCENT_*` environment
overrides. If the managed deployment has no admin key, Baron asks for it
through a hidden prompt and keeps it in the managed protected store for future
idempotent runs. Baron never prints provider, admin, or user keys in logs,
diagnostics, or project state; the only intentional exception is the visible
DeepSeek key while a user is actively typing it in a trusted terminal.

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
