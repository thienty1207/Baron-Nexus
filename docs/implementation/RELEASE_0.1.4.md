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

## Verified publication

- Source revision: `b1fb796c0c45bacc0ef6560acaa7fc2927828845`
- GitHub Actions release run: `33043799542`
- Public release: <https://github.com/thienty1207/Baron-Nexus/releases/tag/v0.1.4>
- Public `SHA256SUMS` entries, independently downloaded and verified on
  2026-08-27:
  - `baron-linux-amd64`: `62679c0bc1fb44669a1f6051e049b175285898b67b07b9bee1ada02488cef105`
  - `baron-windows-amd64.exe`: `d4d5a0b69b44f6b09c35ebc26a1a32bc42f7f06220f1feb60b02646bf6e4ac83`
  - `install.sh`: `940acb6b68805046010232076ba0d34b76e6b33eb10de77b65ff6991f9d987d0`
  - `install.ps1`: `60a810ca5f1f1fdac5f0922fcdeaefd96e7a400d25481b55c480621ffc52c7c1`
  - `release-manifest.json`: `d33c3b346a6cfaebdc499ac80d28321c14707140d4261c18d790b53cbd7afcdf`
  - `GO_TOOLCHAIN.txt`: `76227025cc0bc2be7067aa45d11e09cacfd49c58f498f4c2e4f6a9872a607bf9`
  - `SBOM_MODULES.txt`: `a3e949d6f9cb2c9e3cfd72e346db408050a0530f8953f5aa6e382e777747f84c`
- The downloaded Linux artifact reports `baron 0.1.4`; the Linux and Windows
  files identify as ELF x86-64 and PE32+ x86-64 respectively.
