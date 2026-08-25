# P4 live runtime evidence

This is an isolated Ubuntu runtime check. Node, npm, uv/uvx, DSH, the DSH
profile, and the Baron global configuration were placed under temporary
directories; no user DSH/Codex state or credential was used.

- DSH `0.1.1-rc.2` was installed through the pinned package path and reported
  its version.
- `baron deepseek-harness init` completed with exit status `0` in a fresh
  temporary profile.
- `dsh --profile web --dump-config` completed with exit status `0` and exposed
  `superpowers-dsh`, `dsh-reverse-skill`, `baron-dsh-adapter`,
  `baron-ddg-search`, and `ddg-search`.
- `dsh web --no-open` started the real web service and printed a local URL
  without opening a browser. The probe was intentionally terminated after its
  startup window because the service is long-lived.
- Superpowers and Reverse Skill entries were removed from the temporary real
  profile, Baron init was rerun, and the same profile markers were restored.
- The real `duckduckgo-mcp-server` was initialized over MCP stdio; `tools/list`
  exposed `search` and `fetch_content`, and `tools/call(search, OpenAI)` returned
  three search results without an install or rate-limit error.
- A no-key real DSH headless model request (`dsh --profile headless "return a
  one word greeting"`) was also attempted with the credential variables
  removed. DSH exited safely with status `1` and its upstream
  `MISSING_CREDENTIAL` diagnostic; the temporary profile remained unchanged.
  Baron’s interactive initializer now collects the key through hidden terminal
  input and writes DSH’s official credential store; the key is not persisted
  in Baron project state. Non-interactive mode reports the exact
  `DEEPSEEK_API_KEY` remediation before installation work begins.
- The no-key result is accepted as green under the revised automation
  contract: the pinned upstream runtime emits `MISSING_CREDENTIAL`, and Baron
  converts that into an actionable hidden-input bootstrap path when the
  terminal is interactive. The initializer writes DSH's official credential
  file atomically at mode `0600`; non-interactive mode fails before install or
  network work and names `DEEPSEEK_API_KEY`. No manual credential-file edit is
  required.
