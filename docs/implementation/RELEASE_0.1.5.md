# Baron Nexus 0.1.5 Release Contract

Baron Nexus `0.1.5` fixes Ubuntu/Debian host bootstrap when installing `uv`.
The previous HTTP downloader silently capped every response at 2 MiB. The
`uv-x86_64-unknown-linux-gnu.tar.gz` release archive is larger than that cap,
so checksum verification compared the official digest with a digest of a
truncated archive.

## Scope

- Stream complete official bootstrap downloads before checksum verification.
- Keep a 128 MiB downloader bound and reject responses that exceed it instead
  of silently accepting truncated data.
- Preserve the resolved uv release tag, archive/checksum pairing, retry logic,
  and atomic installation of `uv` and `uvx`.
- Report the source-built and release-built Baron version as `0.1.5`.

## Verification Contract

The release must pass the following checks before publication:

```text
go test ./...
go vet ./...
gofmt -l .
git diff --check
sh -n install.sh scripts/build-release.sh
```

The release artifacts must be built with `BARON_VERSION=0.1.5`, verified with
their generated `SHA256SUMS`, and smoke-tested so the Linux and Windows
binaries report `baron 0.1.5`.

The clean Ubuntu/Debian acceptance flow must run `baron install` with the
published binary and confirm that the complete uv archive passes checksum
verification and installs both `uv` and `uvx`.
