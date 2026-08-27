# Baron Nexus 0.1.8 Release Contract

Baron Nexus `0.1.8` fixes the interactive feedback and launcher activation
issues found after `0.1.7`.

## Changes

- Keep a durable start line before the TTY spinner so fast `baron update` and
  other operations visibly start before their completion line replaces the
  spinner.
- Make `baron permissions enable` prefer the installed Baron directory or
  another existing writable `PATH` directory. Record the selected directory in
  global state so uninstall can remove its two marked launchers later.
- Change interactive uninstall confirmation to `Enter`, `y`, or `Y` for the
  default-confirmed path; `n` or `N` cancels normally, and all other input is
  rejected.
- Allow uninstall to clean marked launchers outside the Baron config directory
  without recursively removing the containing shared `PATH` directory.

## Verification

```text
go test ./...
go vet ./...
gofmt -l .
git diff --check
sh -n install.sh scripts/build-release.sh
```

Release artifacts are built with `BARON_VERSION=0.1.8`, verified against their
SHA-256 manifest, and smoke-tested with `baron --version`.
