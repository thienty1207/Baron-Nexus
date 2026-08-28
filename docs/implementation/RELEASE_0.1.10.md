# Baron Nexus 0.1.10 Release Contract

Baron Nexus `0.1.10` fixes interactive credential prompts being written into
the live loading spinner line during `baron install` and initializer commands.

## Changes

- Stop and clear the interactive spinner before every credential or visible
  value prompt.
- Apply the terminal boundary to DeepSeek, Tencent provider, and Tencent admin
  prompts through the shared credentials prompter.
- Keep spinner progress disabled only for the input interval; later bootstrap
  progress remains line-oriented and the operation completion status is still
  reported.

## Verification

```text
go test ./...
go vet ./...
gofmt -l .
git diff --check
sh -n install.sh scripts/build-release.sh
```

Release artifacts are built with `BARON_VERSION=0.1.10`, verified against their
SHA-256 manifest, and smoke-tested with `baron --version`.
