# Baron Nexus

Current release: `0.1.14` — installs a managed project `AGENTS.md` contract.
It also includes full Ubuntu/WSL uninstall cleanup for Docker volume and network
removal, recreated npm caches, Codex launchers, and Baron update backups. It keeps
the DSH `0.1.1-rc.2` profile verification fix, idempotent
initialization, latest-at-run dependency refresh, loading feedback, explicit opt-in
auto-accept launchers, path-checked full uninstall, concise DeepSeek key rotation,
and automated Ubuntu/Debian first-install bootstrap.

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

The expected output is `baron 0.1.14`. `sudo -v` must succeed before the first
download. The installer resolves the latest Baron release tag, verifies the
release manifest and SHA-256 list before writing the binary, resolves the
latest Node/uv releases at run time, and checks Docker Engine/Compose,
Node/npm/npx, pnpm, and uv/uvx against their current official candidates. A
verified equal version skips its download and mutation; an outdated component
is updated to the exact resolved version. A uv archive and checksum are always
taken from the same resolved release tag and the complete pair is retried once
after a digest mismatch. The installer refuses to replace
an existing binary unless replacement is explicitly enabled, and never reads
or stores the sudo password. If the sudo ticket expires during a long
bootstrap, the installer asks sudo to authenticate again and retries the
operation once. Docker is managed through sudo; Baron does not add the user to
the `docker` group automatically.

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

The expected output is `baron 0.1.14`. The PowerShell installer resolves the
latest Baron release tag, downloads the Windows amd64 release, verifies the
release manifest and SHA-256 list, and installs it at
`$env:LOCALAPPDATA\Baron\baron.exe`. It does not require sudo.
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

The sequence is idempotent, so rerunning it checks every live latest dependency
candidate, skips verified-equal components, and repairs only missing or stale
Baron-owned pieces without replacing project identity or user-owned agent
configuration. On Ubuntu/Debian, Baron performs
`sudo -v` before host/release/Tencent network work, uses sudo only for system
operations, and never receives, echoes, or stores the password. If
authorization expires, Baron requests sudo again once and stops with a repair
instruction if reauthentication fails.

During `baron install` and `baron update`, Baron prints human-readable progress
for each bootstrap phase. Network downloads show the transferred size and
percentage when the source provides a size; package operations show their
start and completion so a long `sudo` or apt operation is not mistaken for a
hung process. Passwords, provider keys, and command output are never included
in these progress lines.

Example:

```text
[Baron] Requesting sudo authorization for host dependencies...
[Baron] sudo authorization accepted
[Baron] Checking latest dependency versions...
[Baron] Node.js 26.8.1, npm, and npx are already latest.
[Baron] DSH 0.2.0 is already latest.
[Baron] Downloading uv archive (attempt 1)...
[Baron]   uv archive (attempt 1): 12.0 MiB/19.3 MiB (62%)
[Baron] uv archive (attempt 1) downloaded.
[Baron] Installing verified uv and uvx...
```

All long-running initializer, setup, repair, install, update, and uninstall
commands also show an ASCII loading indicator on an interactive terminal and
stable start/completion lines when output is redirected. The UI never prints
credentials or raw child-process output.

Set `BARON_INSTALL_TIMINGS=1` to include phase timing lines for discovery,
dependency mutation, validation, and total bootstrap duration when diagnosing a
slow machine. The timing output contains no command output or credentials.

### 3. Later version refresh: `baron update`

Use this when a newer Baron release is available. It is binary-only: it does
not run Docker, DSH, Codex, Tencent, or project setup, and the default
`~/.local/bin` installation does not require sudo:

```text
baron update
```

`baron update` is idempotent when the installed Baron version is already
current. It updates only the verified Baron binary; to refresh external DSH,
Codex, plugin, Node, pnpm, uv, Docker, or Tencent dependencies, rerun
`baron install` or the relevant initializer.
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

To opt into explicit no-prompt launchers for DSH and Codex, run:

```text
baron permissions enable
```

Baron places `dsh-auto` and `codex-auto` in the installed binary directory or
another existing writable directory already present in `PATH`, so no `export`
or profile edit is required in the normal install. Use `dsh-auto` and
`codex-auto` explicitly when full access is intended; normal `dsh` and `codex`
commands are never replaced. Run `baron permissions disable` to remove only
these Baron-owned launchers.

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
Docker on supported Ubuntu/Debian hosts, validates the provider key, resolves
the latest Tencent deployment HEAD to an immutable commit, and fetches that
revision when deployment work is required, then starts MemoryCore,
MemoryHub/Panel, Proxy, and the combined Knowledge Service. A healthy managed
stack is left in place on an idempotent rerun. It does not open the Panel for
manual setup. Proxy values default to
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

To explicitly run DSH and Codex without their approval prompts, create
Baron-owned launchers:

```text
baron permissions enable
```

Then add the printed Baron `bin` directory to `PATH` and run `dsh-auto` or
`codex-auto`. DSH receives `DSH_PERMISSION_MODE=danger-full-access`; Codex
receives `--sandbox danger-full-access --ask-for-approval never`. These launchers
are opt-in and do not replace the normal `dsh`/`codex` commands or edit shell
profiles. `baron permissions disable` removes only these launchers, and
`baron permissions status` reports their state. The full-access mode is unsafe
for untrusted repositories.

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

`AGENTS.md` is a Baron-managed project contract. `baron setup` creates it when
missing and updates only the marked Baron block, preserving project-specific
instructions outside that block. It contains no credentials or runtime secrets.

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

To remove Baron and its known runtime footprint, run one command:

```text
baron uninstall
```

The default is the full purge. It removes Baron global state, project state,
managed hooks and launchers, DSH and Codex homes, DeepSeek/Codex/pnpm packages,
Tencent deployment state, Docker objects and known Docker data, Baron-managed
Node/npm/pnpm/uv caches and binaries, and known API-key assignments in the
standard shell profiles. On Ubuntu/Debian it also purges the known Docker and
Node/npm packages, services, repository entries, and host data through the
existing sudo ticket. On Windows it uses `winget` when available for Docker
Desktop and Node.js; otherwise the report includes a warning instead of
pretending the host component was removed.

Press Enter, `y`, or `Y` at the confirmation prompt, or pass `--yes` for an
explicit scripted run. `--purge-all` is the explicit default; the older
`--purge-shared` flag remains as a compatibility alias. A verified checkout at
`$HOME/Baron-Nexus` is removed only when its `.git/config` points to
`thienty1207/Baron-Nexus`; arbitrary directories are never scanned or deleted.
The command cannot change the parent shell's environment, so any exported key
is reported as requiring `unset` or a new shell after uninstall. It also does
not claim to erase unrelated applications or make the operating system
literally factory-new.

## Troubleshooting

`baron test --json` reports missing Docker, Node/npm/npx, uv/uvx, DSH provider
credentials, Codex authentication, Tencent services, and Baron-owned component
readiness without printing credentials. Codex ChatGPT OAuth remains the one
interactive sign-in owned by Codex; Baron only detects its result and tells the
user when Codex asks for project trust.

Baron Nexus cannot reconstruct uncommitted source bytes after disk destruction from
memory alone; use Git or a separate source backup for that failure mode.
