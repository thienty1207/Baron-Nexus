# Baron Nexus 0.1.17

## Hook reliability

- Keep `PreToolUse` and `PostToolUse` local-first and fail-open under bursts.
- Persist high-frequency tool state atomically in SQLite without waiting for
  the cross-process checkpoint materialization lock.
- Defer remote queue retries to session boundaries and handoff events instead
  of performing a remote retry on every tool event.
- Compact lifecycle payloads before credential redaction so large tool output
  is not scanned repeatedly when only bounded evidence is persisted.
- Remove the redundant app-level JSON marshal/decode round trip before runtime
  handling.

## Verification

- Full Linux test suite passes with the CGO-free Go toolchain.
- `go vet ./...` passes.
- A 32-process burst with 1.9 MB `PostToolUse` payloads completed without a
  process exceeding the configured three-second Codex hook timeout in the
  Ubuntu/WSL smoke test.

