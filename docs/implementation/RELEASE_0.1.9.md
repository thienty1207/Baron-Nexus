# Baron Nexus 0.1.9 Release Contract

Baron Nexus `0.1.9` publishes the full-purge uninstall implementation that
was present only in the local working tree during the `0.1.8` release.

## Changes

- Publish `baron uninstall` with full purge enabled by default.
- Remove Baron, DSH, Codex, Tencent, Docker, known host dependencies, caches,
  launchers, source checkouts, and known credential assignments within the
  documented safety boundaries.
- Keep `--purge-all` as the explicit full-purge flag and `--purge-shared` as a
  compatibility alias.
- Keep the loading UI and latest-at-run dependency refresh changes from the
  unreleased working tree.

## Verification

```text
go test ./...
go vet ./...
gofmt -l .
git diff --check
sh -n install.sh scripts/build-release.sh
```

Release artifacts are built with `BARON_VERSION=0.1.9`, verified against their
SHA-256 manifest, and smoke-tested with `baron --version`.
