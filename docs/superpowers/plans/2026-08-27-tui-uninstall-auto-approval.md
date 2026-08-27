# Baron Nexus 0.1.7 Implementation Plan

## 1. Lock the command and ownership contracts

- Add CLI handlers for `permissions enable|disable|status` and
  `uninstall [--yes] [--purge-shared]`.
- Keep destructive behavior behind exact confirmation and path validation.
- Record launcher ownership with a Baron marker so cleanup is selective.

Tests:

```text
go test ./internal/cli ./internal/uninstall -run 'Permissions|Uninstall|Confirmation'
```

## 2. Add the shared loading UI

- Implement an ASCII TTY spinner with a line-mode fallback.
- Route initializer/setup/repair handlers through the same runner.
- Keep existing download progress and structured output behavior intact.

Tests:

```text
go test ./internal/install ./internal/cli -run 'Progress|Loading|Init'
```

## 3. Implement explicit auto-accept launchers

- Generate platform-appropriate Baron-owned launcher scripts in the Baron
  config directory.
- DSH gets `DSH_PERMISSION_MODE=danger-full-access`; Codex gets explicit
  documented approval/sandbox flags.
- Print usage and PATH instructions without modifying shell profiles.

Tests:

```text
go test ./internal/permissions ./internal/cli -run 'Permission|Launcher'
```

## 4. Implement idempotent uninstall

- Remove Baron state, project state, hooks, DSH credential entries/patch blocks,
  npm packages, Tencent deployment resources, and self-binary safely.
- Make `--purge-shared` explicit and retain unrelated user data by default.
- Return a readable removal report suitable for terminal and tests.

Tests:

```text
go test ./internal/uninstall ./internal/install ./internal/app ./internal/cli -run 'Uninstall|Cleanup|Remove'
```

## 5. Release and verify

- Bump all release metadata to `0.1.7`, update installation docs, and add release
  notes.
- Run focused tests, full tests, vet, cross-platform builds, and the existing
  release/checksum checks.
- Commit, tag `v0.1.7`, push, and publish the GitHub release only after all
  verification gates pass.
