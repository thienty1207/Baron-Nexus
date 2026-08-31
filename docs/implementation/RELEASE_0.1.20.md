# Baron Nexus 0.1.20

## Conversation continuity

- Persist bounded user prompts and assistant final responses in the canonical
  SQLite event ledger instead of relying on prose or an in-memory transcript.
- Capture the same conversation turns to Tencent as historical recovery memory,
  while keeping raw tool arguments and long tool output local.
- Inject the previous conversation into a new Codex session through
  `SessionStart.additionalContext`, or into the first DSH pre-step when DSH is
  the active adapter.
- Preserve scalar prompts and latest user messages in the DSH adapter instead of
  serializing the whole message list or accidentally replaying Baron recall
  messages as new user prompts.
- Keep local recovery deterministic and bounded: SQLite is queried first,
  Tencent is not queried for normal same-session continuity, and repeated DSH
  checkpoints do not repeat the recovered transcript.

## Verification

- Conversation projection, prompt/assistant capture, Codex hook output, and
  DSH adapter tests pass.
- Full Linux `go test ./...` and `go vet ./...` pass with the CGO-free Go
  toolchain.
- The DSH adapter context injection test passes in Ubuntu WSL.
- Windows full-suite runs still report the repository's pre-existing
  permission-bit and symlink-privilege test limitations; the changed packages
  and end-to-end conversation tests pass on Windows.
