# Baron Nexus Release Install and Update Design

**Status:** Approved by the requested command contract

**Goal:** Publish Baron Nexus `0.1.0` as a verified native release that users can install from GitHub, inspect with `baron --version`, and upgrade later with `baron update` without losing Baron, project, or Tencent state.

## User contract

The supported command surface is:

```text
baron --version
baron install
baron update
```

`baron --version` prints exactly `baron 0.1.0` for the initial release. `baron install` downloads the latest compatible Baron release for the current OS and architecture, verifies its release manifest and SHA-256 entry, and installs it atomically. `baron update` performs the same verified transaction but reports a no-op when the current binary already matches the latest release.

The first bootstrap still uses the signed/verified GitHub release installer because a user cannot invoke a binary that has not yet been downloaded. The shell and PowerShell installers therefore download the release candidate, verify it, and place it in the user installation directory. Once the command exists, `baron install` and `baron update` use the same release protocol.

Neither command changes project source, `.baron` project identity, local SQLite continuity, credentials, Tencent metadata, or Tencent Docker volumes. The existing binary is copied to a recoverable rollback artifact before replacement; a failed candidate validation restores the prior binary.

## Release protocol

The default release source is the public GitHub repository `thienty1207/Baron-Nexus`. The release client requests the latest GitHub release metadata, selects the exact native asset for `GOOS/GOARCH`, downloads `release-manifest.json`, `SHA256SUMS`, and the binary over HTTPS, and verifies:

1. The release manifest version matches the GitHub tag and requested candidate.
2. The SHA-256 entry names the exact selected binary and matches its bytes.
3. The candidate launches with `--version` and reports the manifest version.
4. The candidate is installed through a same-directory temporary file and atomic rename.

The HTTP client has bounded response sizes, a finite timeout, a GitHub user agent, and an injectable base URL for tests. Production defaults are fixed to GitHub release metadata/assets; tests use an in-process HTTPS test server. Credentials and response bodies are never printed.

## Platform behavior

Linux amd64 is the first live target. Windows amd64 keeps the PowerShell installer path and uses the same manifest/checksum contract; if the running executable cannot be replaced because Windows holds the file open, the command leaves a verified staged candidate and reports the exact restart action instead of corrupting the current binary. Unsupported OS/architecture combinations fail before mutation.

## Files

- `internal/version/`: single default/build-injected Baron version.
- `internal/release/`: GitHub release metadata, asset selection, bounded downloads, manifest/checksum verification, and update report.
- `internal/install/update.go`: existing atomic binary replacement and rollback primitive, reused by the release client.
- `internal/cli/`: version flag plus `install` and `update` commands.
- `internal/app/`: wires release operations without coupling them to project/Tencent state.
- `install.sh`, `install.ps1`: first-download installers with checksum verification and explicit overwrite protection.
- `scripts/build-release.sh`: emits versioned native artifacts and release metadata for `0.1.0`.
- `.github/workflows/release.yml`: builds and publishes tagged GitHub releases.
- `README.md`: public install/version/update instructions.

## Acceptance

- CLI tests cover exact version output, command registration, and error propagation.
- Release-client tests cover asset selection, checksum mismatch, manifest/tag mismatch, bounded downloads, unsupported platform, and successful verified installation with rollback.
- Shell/PowerShell syntax and release manifest/checksum tests pass.
- A `v0.1.0` artifact is built locally, verified, and pushed to the supplied GitHub repository; the public release workflow remains the source for future `baron update` candidates.
- Existing full Go tests, vet, CGO-free builds, branding/platform scans, and secret scans remain green.
