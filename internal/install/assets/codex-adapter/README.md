# Baron Nexus Codex adapter

The Codex bridge forwards lifecycle JSON to `baron hook codex <event>` with a
bounded timeout and fail-open response. It does not own Codex skills, provider
configuration, or authentication state.
