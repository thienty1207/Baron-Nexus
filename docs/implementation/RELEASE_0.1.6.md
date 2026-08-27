# Baron Nexus 0.1.6 Release Contract

Baron Nexus `0.1.6` makes long-running install and update operations visible
without exposing credentials or raw command output. The CLI reports each
bootstrap phase and reports download size/progress when the HTTP response
provides a content length.

## Scope

- Report sudo authorization before the interactive prompt and confirm when it
  has been accepted.
- Report Node.js/npm/npx, pnpm, uv/uvx, Docker, Tencent, DSH, Codex, and project
  bootstrap phases with start, completion, and failure messages.
- Show bounded download progress for Node, Docker, uv, and Baron release
  downloads while retaining the existing download safety limits.
- Keep `FileDownloader` and `CommandRunner` test seams compatible when no
  progress reporter is supplied.
- Report the source-built and release-built Baron version as `0.1.6`.

## Verification Contract

The release must pass the following checks before publication:

```text
go test ./...
go vet ./...
gofmt -l .
git diff --check
sh -n install.sh scripts/build-release.sh
```

The release artifacts must be built with `BARON_VERSION=0.1.6`, verified with
their generated `SHA256SUMS`, and smoke-tested so the Linux and Windows
binaries report `baron 0.1.6`.

The clean Ubuntu/Debian acceptance flow must run `baron install` with the
published binary and show progress while sudo, apt, official dependency
downloads, and the remaining bootstrap phases are running.
