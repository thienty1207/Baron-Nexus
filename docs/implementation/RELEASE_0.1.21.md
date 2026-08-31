# Baron Nexus 0.1.21

## Conversation continuity hardening

- Keep the newest conversation projection rows after the bounded SQLite scan;
  do not return the oldest rows from the selected window.
- Keep the newest suffix when rendering the bounded Codex/DSH context, so a
  long historical answer cannot evict the latest user requirements.
- If the newest turn alone exceeds the character budget, truncate that turn
  rather than returning an empty conversation context.

## Verification

- Regression coverage proves SQLite returns the newest rows after the bounded
  scan and the renderer retains the newest user and assistant turns.
- A replay against the real Ferris SQLite state returned all three reported
  requirements: `crawler/target`, public salary handling, and `moegirl`.
- The replay ran against a temporary copy of `.baron`; the Ferris working tree
  and its uncommitted changes were not modified.
