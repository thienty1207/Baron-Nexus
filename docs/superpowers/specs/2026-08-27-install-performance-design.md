# Baron Install Performance Design

**Status:** Approved in conversation on 2026-08-27

## Goal

Make repeated `baron install` runs substantially faster without weakening the
product contract that every run checks upstream latest versions and updates
only components whose verified local state differs from the current upstream
state.

## Scope

This change is limited to install, update, and host-bootstrap performance. It
does not add Rust, move CodeGraph locally, redesign Tencent Memory or knowledge
retrieval, or change unrelated product behavior.

## Invariants

- Every `baron install` performs a live latest-version check for Baron, DSH,
  Codex, pnpm, uv, Node, Docker, and relevant managed plugins where the
  upstream source supports a version query.
- A local component is not considered latest merely because it exists or meets
  a minimum version.
- Equal, successfully normalized versions skip downloads and mutation.
- An upstream version-check failure is reported as `latest unknown`; Baron
  never reports that component as latest.
- Required release security remains unchanged: HTTPS restrictions, manifests,
  SHA256 verification, candidate validation, atomic replacement, rollback,
  sudo boundaries, and credential redaction.
- Read-only discovery may run concurrently with bounded concurrency. Mutations
  remain deterministic and sequential, especially apt/dpkg, global npm
  installs, Docker package operations, and binary replacement.
- Tencent Memory, Tencent CodeGraph, project identity, adapters, receipts and
  user-owned configuration remain compatible.

## Architecture

`baron install` is organized into two phases:

1. **Discovery/planning:** resolve remote versions, inspect local versions,
   inspect health/configuration, and produce a component state/update plan.
2. **Mutation/validation:** execute only required changes in a deterministic
   order, then run expensive validation only for changed state.

The plan records at least component name, installed state, normalized local
version, normalized latest version, whether an update is needed, and whether
configuration or plugin state changed. Existing injectable command runners,
HTTP clients, progress reporters, and test seams remain the boundaries for
deterministic tests.

## Component Rules

### Baron

Always query GitHub latest release metadata. Compare it to the running Baron
version before downloading the manifest, checksum file, or binary. Equal
versions return an already-latest result without binary work. Different
versions use the existing secure release flow and exact resolved release.

### DSH, Codex, and pnpm

Always query the npm registry with `npm view <package> version` and compare the
normalized result to the local command version. Equal versions skip global npm
installation. Different versions install the exact resolved version, while
preserving adapters, hooks, profiles, receipts, credentials, and validation.

### uv/uvx

Always query the latest GitHub uv release tag and normalize an optional `v`
prefix before comparison with `uv --version`. Equal versions skip archive,
checksum, extraction, and installation. Different versions retain retry,
matching checksum verification, extraction, and atomic replacement.

### Node

Always resolve the latest supported Node channel/version and compare it with
`node --version`. An already-selected latest version skips reinstall; an older
version is updated. Repository preparation must not cause unnecessary repeated
work.

### Docker and apt

Health and latestness are separate checks. Repository configuration is prepared
first, apt metadata is refreshed once per install planning cycle where possible,
installed package versions are compared with apt candidates, and only stale
packages are updated. Existing unhealthy-Docker repair/start behavior remains.
Apt and dpkg operations never run concurrently.

### DSH plugins and startup probe

Inspect web/headless profile state and required plugin/adaptor state before
mutating. Add or update only missing/outdated Baron-managed entries. Run the
bounded DSH startup probe on first install or when DSH, plugins, adapter,
profile, or relevant configuration changed. Skip it when all DSH state is
unchanged.

## Progress and Failure Semantics

Progress output must show that latest checks are active, for example:

```text
[Baron] Checking latest versions...
[Baron] Baron 0.1.8 is latest.
[Baron] DSH 1.5.2 is latest.
[Baron] Updating Codex to 0.93.0...
```

It must not expose credentials or raw sensitive child-process output. A failed
upstream query is rendered as an actionable verification failure, not as a
skip or latest claim.

## Transport and I/O

Where downloader paths currently create separate clients/transports, use an
injected reusable HTTP transport with bounded idle connections and timeouts.
Preserve TLS >= 1.2 and host restrictions. Binary streaming is lower priority
than eliminating unnecessary downloads and may be added only if the remaining
timing data justifies it.

## Testing and Measurement

Add deterministic tests proving latest metadata is queried and unnecessary
mutation is skipped for Baron, DSH, Codex, pnpm, uv, Node, Docker, plugins, and
repeat install. Add outdated-component tests proving exact-version mutation,
verification, and rollback/security behavior remain intact. Add tests for
upstream-check failure and DSH probe change tracking.

Measure fresh install, repeat-all-latest install, one-component-update install,
and high-latency discovery. Report timings for discovery, apt refresh, each
component, validation, and total duration. Do not claim a speedup without
direct timing evidence.

## Non-goals

- No Rust helper or Rust rewrite.
- No Tencent Memory or CodeGraph redesign.
- No local Code Intelligence index.
- No stale long-lived latest-version cache.
- No concurrent system mutations.
- No removal of release or credential security checks.
