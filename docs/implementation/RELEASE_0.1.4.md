# Baron Nexus 0.1.4 Release Contract

Date: 2026-08-27

## Release intent

Baron Nexus `0.1.4` changes automatic dependency selection from historical
fixed upstream versions to a latest-at-run policy. The policy applies when an
explicit install or initializer actually performs dependency work; diagnostic
commands remain read-only, and a healthy service is not replaced merely by
`baron test`.

Each mutable source is resolved once for the operation and then used
consistently:

- Baron release installers resolve the GitHub `latest` tag through the API and
  download tag-scoped assets.
- Ubuntu/Debian host bootstrap resolves the newest supported Node major from
  the official Node release index, refreshes Node/npm/npx and pnpm, and
  resolves one uv release tag for both its archive and checksum. A complete uv
  archive/checksum pair is retried once after a digest mismatch.
- DSH, Superpowers, the DSH MCP client, and Codex use their official latest
  package selectors. Receipts record the version reported by the installed
  command.
- Tencent Agent Memory resolves the upstream default `HEAD` with
  `git ls-remote`, converts it to a 40-character commit SHA before checkout,
  pulls the latest managed images when deployment work is required, and
  records commit/image-digest evidence. A healthy managed stack is preserved
  by the idempotent initializer. Rollback still uses the prior immutable
  manifest commit.

No latest resolver silently downgrades or falls back to an old pinned version.
Existing project identity, checkpoints, user-owned agent settings, managed
credentials, Tencent `.env`, and Docker volumes remain outside the replacement
transaction. Secrets are not written to receipts, manifests, diagnostics, or
error messages.

## Platform boundary

Ubuntu and Debian are the supported automatic host-bootstrap targets. The
Linux path performs native sudo preflight before package/release/network work
and asks sudo itself for authentication; Baron never receives or stores the
password. Windows continues to require the user to install and start Docker
Desktop, WSL2/Ubuntu, and Tencent prerequisites. The PowerShell installer
resolves the latest Baron release tag but does not claim unsupported Windows UI
automation.

## User command contract

```text
git clone https://github.com/thienty1207/Baron-Nexus.git
cd Baron-Nexus
sudo -v                 # Ubuntu/Debian only; sudo prompts natively
./install.sh            # machine install + latest host dependencies
export PATH="$HOME/.local/bin:$PATH"
cd /path/to/project
baron install           # latest Baron/dependencies + DSH/Codex/Tencent/project setup
```

On Windows, use `install.ps1` from PowerShell after the documented manual
prerequisites. `baron update` remains a verified Baron-binary update; rerun
`baron install` or an explicit initializer to refresh external dependencies.

## Verification record

The source implementation must pass the full Go test/vet/format suite, shell
syntax and installer/platform checks, CGO-free Linux/Windows artifact builds,
and every generated SHA-256 entry before publication. Public-release checks
must download the `v0.1.4` assets, verify `SHA256SUMS`, and smoke-test the
Linux binary. Clean-machine, Windows-runtime, real provider-rotation, and
full Tencent/CodeGraph completion gates remain separate acceptance evidence;
they must not be inferred from fixtures or a healthy existing stack.

Final source revision and artifact hashes are recorded here after the release
commit and public asset verification.
