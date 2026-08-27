# Baron Nexus 0.1.7 Release Contract

Baron Nexus `0.1.7` adds visible loading for long-running commands, explicit
opt-in auto-accept launchers for DSH/Codex CLI, and guarded idempotent cleanup.

## Scope

- Render an ASCII spinner on TTY output and stable lifecycle lines for pipes or
  CI output across initializer, setup, repair, install, update, and uninstall
  operations.
- Add `baron permissions enable|disable|status` with Baron-owned launchers that
  never overwrite `dsh`/`codex` or edit shell profiles.
- Add `baron uninstall [--yes] [--purge-shared]` with exact confirmation,
  ownership/path guards, selective hook/credential/DSH patch cleanup, project
  `.gitignore` cleanup, Tencent container cleanup, and self-binary removal.
- Preserve unrelated Codex hooks, DSH credential refs, project files, and shared
  host dependencies unless the user explicitly selects shared-home purge.
- Report source-built and release-built version `0.1.7`.

## Verification Contract

```text
go test ./...
go vet ./...
gofmt -l .
git diff --check
sh -n install.sh scripts/build-release.sh
```

Build release artifacts with `BARON_VERSION=0.1.7`, verify `SHA256SUMS`, and
smoke-test both binaries with `--version`. Verify the published Linux artifact
on Ubuntu/WSL before release publication.
