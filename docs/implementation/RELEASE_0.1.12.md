# Baron Nexus 0.1.12 Release Contract

Baron Nexus `0.1.12` closes cleanup gaps found by a real Ubuntu/WSL uninstall
run of `0.1.11`.

## Changes

- Run full host cleanup before the final filesystem pass so npm or pnpm cannot
  recreate a cache after Baron has removed it.
- Remove Baron update backups for both `.baron-backup-*` and
  `.baron-update-backup-*` naming schemes.
- Remove known user-level DSH, Codex, pnpm, and pnpx launchers from
  `~/.local/bin`; known symlinks are unlinked without following their targets.
- Keep full purge path validation restricted to the current user's home and
  known launcher paths.

## Verification

```text
go test ./...
go vet ./...
gofmt -l internal/uninstall/full_purge.go internal/uninstall/uninstall.go internal/uninstall/uninstall_test.go
git diff --check
sh -n install.sh scripts/build-release.sh
```

The patched binary was executed in Ubuntu/WSL against the existing cleanup
residue. Baron, Node/npm, DSH, Codex, Docker, caches, the Codex launcher
symlink, and Baron update backups were absent after uninstall; Debian package
records and Docker data directories were also absent.
