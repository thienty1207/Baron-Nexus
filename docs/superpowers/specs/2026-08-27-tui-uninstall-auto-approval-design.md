# Baron Nexus 0.1.7: Loading, Uninstall, and Explicit Auto-Accept

## Goal

Make long-running Baron initialization visibly active, provide a reversible and
auditable uninstall path, and give users an explicit way to launch DSH or Codex
without interactive approval prompts. Do not silently weaken the safety model of
third-party tools or delete unrelated system software.

## Scope

### Loading UI

Use one progress UI for bootstrap, update, setup, repair, and all initializer
commands. On an interactive terminal it renders a lightweight ASCII spinner;
when stdout is redirected it emits line-oriented start/completion/failure logs.
The UI is disabled for structured JSON output and never writes secrets.

### Explicit auto-accept

Add `baron permissions` commands that manage Baron-owned launcher scripts:

- `baron permissions enable` writes `~/.config/baron/bin/dsh-auto` and
  `~/.config/baron/bin/codex-auto` (or the platform equivalent) and prints the
  exact PATH/export instructions instead of mutating shell startup files.
- `baron permissions disable` removes only those Baron-owned launchers.
- `baron permissions status` reports the current state.

The DSH launcher opts into `DSH_PERMISSION_MODE=danger-full-access`. The Codex
launcher uses the documented `--sandbox danger-full-access` and
`--ask-for-approval never` flags. The launchers are opt-in, visibly named, and
never replace the user's existing `dsh` or `codex` binaries. The uninstall path
removes them.

### Uninstall

Add `baron uninstall [--yes] [--purge-shared]`.

- Without `--yes`, show a plan and require the exact phrase `UNINSTALL BARON`.
- The default removes Baron global state, Baron adapters/receipts, Baron-owned
  hooks and DSH patch blocks, project `.baron` state known to Baron, the
  registered DSH/Codex npm packages, Tencent containers/deployment, and the
  Baron binary when safe.
- `--purge-shared` additionally removes shared DSH/Codex homes and host tools
  that Baron can identify, but still refuses filesystem roots and unrelated
  project directories. It requires the same exact confirmation.
- The command is idempotent and reports skipped resources instead of failing on
  an already-removed optional resource.

Uninstall must never recursively remove a home directory, filesystem root, or a
path that is not validated as Baron-managed. It preserves non-Baron Codex hooks,
DSH credential references, and unrelated host packages by default.

### Ctrl+C behavior

Direct `dsh web` is an upstream process and is not launched by Baron during
normal use. Repeated Ctrl+C signals can therefore produce DSH/dependency
shutdown noise without proving a Baron defect. Baron startup probing remains
bounded and treats parent cancellation as cancellation, not liveness success.

## Non-goals

- Silently enabling full access for existing `dsh` or `codex` commands.
- Editing `.bashrc`, PowerShell profiles, or other shell startup files.
- Removing a user's manually installed Node, Docker Desktop, WSL, or unrelated
  npm packages.
- Rewriting or deleting arbitrary user configuration.

## Verification

Unit tests cover loader fallback/spinner lifecycle, launcher content and path
guards, hook/credential/patch cleanup, confirmation behavior, and idempotency.
The release gate runs `go test ./...`, `go vet ./...`, Linux and Windows builds,
and release asset/checksum verification.
