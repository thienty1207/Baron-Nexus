# Baron Nexus

Current release target: `0.1.22` — adds an immutable Baron-managed runtime bundle
for UV, compatible Python, Strix, Bun, Go, Node/npm/pnpm, DSH, and Codex while
preserving the local-first continuity and Tencent recovery contracts. It keeps
the DSH profile verification fix, idempotent initialization, progress feedback,
explicit opt-in auto-accept launchers, path-checked ownership cleanup, DeepSeek
credential fan-out, and automated Ubuntu/Debian host bootstrap.

Baron Nexus is a native Go project continuity sidecar for DeepSeek Harness and Codex.
It records local work state first, routes bounded project memory to TencentDB
Agent Memory, and lets either agent recover unfinished work without a Baron
daemon.

## Which command should you use?

Baron has three different operations. `./install.sh` or `install.ps1` installs
the command, `baron install` activates the complete managed bundle for the
current project, and `baron update` refreshes that bundle atomically.

### 1. First time on Ubuntu/Debian: `./install.sh`

Run this once on a new Ubuntu or Debian machine. It downloads and verifies the
native Baron binary into `~/.local/bin` and bootstraps supported host
Docker/Compose, Node/npm/npx, pnpm, and uv/uvx prerequisites. It does not
initialize DSH, Codex, Tencent, the managed runtime generation, or a project
yet:

```text
git clone https://github.com/thienty1207/Baron-Nexus.git
cd Baron-Nexus
sudo -v
./install.sh
export PATH="$HOME/.local/bin:$PATH"
baron --version
```

The expected output is `baron 0.1.22`. `sudo -v` must succeed before the first
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
Desktop, or make Docker Engine reachable inside an Ubuntu WSL2 distribution;
also prepare the required Tencent service prerequisites. Baron does not
silently install or control these Windows UI components.

```powershell
git clone https://github.com/thienty1207/Baron-Nexus.git
Set-Location Baron-Nexus
Set-ExecutionPolicy -Scope Process Bypass
.\install.ps1
$env:Path = "$env:LOCALAPPDATA\Baron;$env:Path"
baron --version
```

The expected output is `baron 0.1.22`. The PowerShell installer resolves the
latest Baron release tag, downloads the Windows amd64 release, verifies the
release manifest and SHA-256 list, and installs it at
`$env:LOCALAPPDATA\Baron\baron.exe`. It does not require sudo.
If the current PowerShell session still cannot find `baron`, open a new
PowerShell window or run:

```powershell
& "$env:LOCALAPPDATA\Baron\baron.exe" --version
```

### 2. First time in a project: `baron install`

After the command exists, run this once for each project. It resolves one
immutable plan, stages and verifies the complete Baron-managed runtime bundle,
then bootstraps DSH, Codex, Tencent, and the current project's Baron state. On
Ubuntu/Debian it also verifies or installs the host prerequisites listed above.

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

Windows must have either Docker Desktop or Docker Engine reachable through
Ubuntu WSL2, plus the required Tencent service prerequisites. Baron does not
silently install or control those Windows UI components, and Windows does not
use the Linux `sudo -v` preflight.

The sequence is idempotent, so rerunning it resolves the latest compatible
stable candidates, skips verified-equal Baron-owned components, and repairs only
missing or stale managed pieces without replacing project identity or user-owned
agent configuration. On Ubuntu/Debian, Baron performs
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

The managed bundle is described by the release catalog at
`configs/managed-runtime-catalog.json` (copied into release artifacts). Archive
components are extracted only after checksum verification. Package-backed
components use their catalog-declared method: npm packages are installed into
the Baron generation with lifecycle scripts disabled, and Strix is installed
as the catalog-verified `strix-agent` package through the managed UV/Python
pair. The managed npm runtime itself is archive-provided; PNPM, DSH, and Codex
package installation runs only after that verified npm runtime is active. Every
component is resolved for the exact current platform and
architecture; a release without a complete Linux amd64 and Windows amd64
catalog is not releasable.

### 3. Later version refresh: `baron update`

Use this when a newer Baron release is available. The managed path resolves and
activates one complete compatible bundle for Baron, UV, Python, Strix, Bun, Go,
Node/npm/pnpm, DSH, and Codex, then refreshes Tencent/project contracts for the
current project. It reuses verified components and rolls back the managed
generation if activation or bootstrap fails:

```text
baron update
```

`baron update` is idempotent when the installed bundle is already current. It
does not scan or rewrite other registered projects; those are refreshed at their
next managed launch or explicit setup. A legacy installation without the
managed-runtime coordinator may use the binary-only compatibility fallback and
reports that state instead of claiming a full bundle update.
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
DSH's official `$DSH_HOME/.credentials.yaml`, reuses it for Tencent, and fans
it out to the Baron-managed Strix environment when the managed runtime is
active. During a full managed bootstrap, the one key entered for DSH is
therefore enough; the Tencent deployment `.env` is filled during its own
checkout step.
Codex ChatGPT sign-in remains owned by Codex: if global Codex auth is absent,
Baron prints one action to run `codex` and complete sign-in; once completed,
that auth is reused across projects and later launches. Baron does not ask for
the ChatGPT password or copy OAuth secrets. `baron test` is always read-only
and never prompts. To rotate a key later, run this from the project you want to
repair; it validates the key, updates the DSH store and any existing
Baron-managed Tencent runtime environment, recreates the Tencent containers so
they receive the new key, and automatically retries the current project's
Baron setup. No `.env` edit or separate `baron setup` command is required:

```text
baron deepseek api_key
```

When run outside an initialized Baron project, the command updates the global
credential and Tencent runtime only; the next project setup uses that key.

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
updating the protected stores and automatically re-syncing the current project.
The older `baron credentials set deepseek` form remains available as a
compatibility alias.

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
environment variable to set. On Windows, install Docker Desktop or prepare
Docker Engine in Ubuntu WSL2, along with the required Tencent service
prerequisites, manually first; Baron reports this prerequisite without
silently claiming to install Windows components.

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

## Pentest workflow

Run an authorized assessment from the project directory:

```text
baron pentest --normal
baron pentest --deep
baron pentest --target <https-url> --normal
baron pentest --target <https-url> --deep
```

Local scans use a Baron-owned snapshot, not the real working tree. Strix is
report-only; in a managed Codex or DSH session the active agent may validate
findings, apply source fixes, run tests, and retest. No commit, push, deploy, or
publish is performed automatically. Use `baron pentest status <job-id>`,
`baron pentest report <job-id>`, or `baron pentest stop <job-id>` for a job.
URL-only targets remain scan/report-only because there is no mapped local
source tree to remediate. Managed DSH/Codex launchers set the non-secret
`BARON_CLIENT` identity so Baron can distinguish an active agent session from a
direct human CLI invocation; restart the managed launcher after an update if
the agent process was already running. On native Windows, Baron does not run a
native Strix executable: pentest execution remains fail-closed until the
verified Ubuntu WSL2 + Docker bridge is available.

## Project files

`AGENTS.md` is a Baron-managed project contract. `baron setup` creates it when
missing and updates only the marked Baron block, preserving project-specific
instructions outside that block. It contains no credentials or runtime secrets.

`.baron/project.toml` is the commit-safe stable project identity.
`.baron/.env` contains project-local Tencent runtime values, is Git-ignored,
and is mode 0600 on Unix. `.baron/runtime/state.db` is the local SQLite WAL
authority; `checkpoint.json` is a readable materialized snapshot.

### Local continuity evidence

Baron keeps authority boundaries explicit: Git and the working tree describe
current source, SQLite owns canonical lifecycle/task/checkpoint events, and
Tencent is historical/recovery reference only. `task_started` creates or
resumes a structured task; `task_updated` only updates an existing `task_id`.
Verification is scoped (`unit`, `integration`, `build`, `acceptance`, or
`completion`), and only evidence allowed by the task completion policy can
promote a task to completed.

Use these local-only views when checking what Codex or DSH last did:

```text
baron status
baron timeline --limit 50
baron timeline --limit 100 --json
```

They read Git and SQLite without an LLM or Tencent request. Bounded user prompts
and assistant final responses are retained as conversation turns in SQLite and
durably queued locally and flushed to Tencent at lifecycle boundaries for
cross-session recovery; raw tool arguments, minor tool events, and long raw
tool output stay local. Only bounded summaries for task
failure/block, verified completion, important test/build evidence, interruption,
clean close, or explicit handoff are queued as continuity memory. If local
evidence is complete, same-session and Codex/DSH handoff remain local-only.
Tencent recall is conditional, cached by the local recovery fingerprint, and
never overwrites the local task projection.

## Support operations

`baron status`, `baron doctor`, `baron repair`, `baron backup <destination>`,
and `baron restore <archive>` are support operations. A backup intentionally
excludes plaintext credentials; re-keying may be required after restore. If a
restore target already exists, Baron stops safely; choose
`baron restore <archive> --replace-existing` only when you want the current
state moved to a recoverable sibling backup first.

To remove Baron and the Baron-owned runtime footprint, run one command:

```text
baron uninstall
```

The default is the receipt-backed purge. It removes Baron global/project state,
Baron-managed hooks and launchers, the DeepSeek key from DSH's provider store,
Baron-managed runtime generations, Strix credentials, caches, receipts, and
the verified Tencent deployment files and owned Docker containers. It preserves
the user's DSH/Codex homes and authentication, system Python/Node/Go/Bun/uv
installations, Docker Desktop/Engine installation and unrelated Docker data,
unrelated projects, and credentials that Baron cannot prove it owns. The
operation cannot truthfully promise a factory-reset machine or remove every
dependency installed by another tool.

Press Enter, `y`, or `Y` at the confirmation prompt, or pass `--yes` for an
explicit scripted run. `--purge-all` is the explicit default; the older
`--purge-shared` flag remains as a compatibility alias. A verified checkout at
`$HOME/Baron-Nexus` is removed only when its `.git/config` points to
`thienty1207/Baron-Nexus`; arbitrary directories are never scanned or deleted.
The command cannot change the parent shell's environment, so any exported key
is reported as requiring `unset` or a new shell after uninstall.

## Troubleshooting

`baron test --json` reports missing Docker, Node/npm/npx, uv/uvx, DSH provider
credentials, Codex authentication, Tencent services, and Baron-owned component
readiness without printing credentials. Codex ChatGPT OAuth remains the one
interactive sign-in owned by Codex; Baron only detects its result and tells the
user when Codex asks for project trust.

Baron Nexus cannot reconstruct uncommitted source bytes after disk destruction from
memory alone; use Git or a separate source backup for that failure mode.
