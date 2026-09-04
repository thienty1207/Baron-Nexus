# Baron Nexus 0.1.22 Managed Runtime Checkpoint

Status: source candidate. Local implementation and artifact checks pass, but
the stable release gate remains blocked by external acceptance phases P19, P22,
and P27.

## Implemented locally

- Added immutable managed-runtime plans, exact platform/architecture matching,
  generation activation, ownership receipts, resumable staging, rollback, and
  receipt-backed purge targets.
- Added archive, npm, and uv-tool staging paths. Package installers use managed
  dependencies, an allowlisted environment, and disabled npm lifecycle scripts.
- Added exact executable probes, Windows command-shim resolution, and a managed
  Strix executable path for pentest jobs.
- Wired the full-bundle coordinator into install/update without removing the
  existing DSH, Codex, Tencent, project, or legacy fallback contracts.
- Added DeepSeek fan-out to Baron-managed DSH, Tencent, and Strix stores without
  changing Codex authentication ownership.
- Full managed bootstrap now applies the validated DSH key to the protected
  Strix environment in the same flow; Tencent fills its deployment environment
  from the DSH store during its later checkout step.
- Added a real hash-pinned catalog for stable releases and both Linux amd64 and
  Windows amd64 assets, plus legacy compatibility fixture/harness wiring.
- Documented the report-only Strix boundary: only the active Codex or DSH
  session may remediate the real working tree, and no automatic push occurs.
- Managed DSH/Codex launchers now carry a validated non-secret `BARON_CLIENT`
  identity, and generation verification rejects launcher tampering.
- Windows native Strix execution is explicitly fail-closed; the release still
  requires a verified Ubuntu WSL2 + Docker bridge before Windows pentest is
  advertised as ready.
- Pentest ROE loopback detection uses parsed host identity, and snapshot cleanup
  is restricted to Baron-created directories under the OS temp root.
- Strix SARIF output is normalized into the canonical finding schema, including
  rule, severity, confidence, target, source path, and evidence references.
- Strix reports and artifacts are restricted to the job-owned workspace; report
  symlinks, external artifact paths, and unmanaged local snapshot roots are
  rejected before they can enter the evidence ledger.
- Job workspaces reject symlink escapes, and artifact hashing rechecks the
  opened file against the bounded size limit instead of trusting one metadata
  lookup. Windows bootstrap now verifies either an existing Docker Desktop
  daemon or a Docker engine inside verified Ubuntu WSL2 without attempting to
  mutate Windows UI components or requesting sudo.
- Lifecycle hooks receive a provider-safe execution deadline and SQLite setup
  honors that context, so lock contention fails open with a valid response
  instead of waiting past the provider timeout.
- Platform detection verifies an Ubuntu WSL2 distro and Docker inside that
  distro separately from host Docker; detection is not presented as bridge
  execution support.
- Shell portability is enforced with LF attributes; release-gate status matching
  remains correct when a Windows checkout supplies CRLF Markdown.

## Verification

The focused managed-runtime, installer, probe, Strix, catalog, compatibility,
and source/embedded adapter-parity tests pass using the current Windows/Ubuntu
WSL test environment. The real catalog validator reports ten releases and ten
required components. The opt-in real-catalog acceptance stages the complete
Windows amd64 bundle, verifies receipts and generated launchers, and passes.

Fresh `go test ./... -count=1`, `go vet ./...`, `gofmt -l .`,
`git diff --check`, adapter checks, catalog validation, release-build checks,
legacy compatibility, installer preflight, platform guidance, branding, and
Linux/Windows cross-build checks also pass. The full race suite passes in the
official `golang:1.27-bookworm` container with GCC; the equivalent native
Windows host run is unavailable because that host has no `gcc`/cgo compiler.

The release gate correctly refuses stable publication while P19, P22, and P27
still contain unchecked external acceptance items. Native Windows Strix is
fail-closed until a verified Ubuntu WSL2 + Docker bridge is production-wired.
`baron pentest stop` records a stopped job but does not yet claim to terminate
an external Strix process or container.

## Release blockers

- Roadmap P19, P22, and P27 still contain unchecked clean-machine, live
  Tencent/DSH/Codex, Windows bridge, CodeGraph/Wiki/Skill, and release
  acceptance cases. The gate intentionally remains `BLOCKED`.
- Native Windows Strix execution is disabled until the verified Ubuntu WSL2 +
  Docker bridge is implemented and accepted; WSL race/adapters are test
  evidence only.
- The native Windows race invocation remains unavailable because the host has no
  `gcc`; the same full race suite passes in the official Go container.
