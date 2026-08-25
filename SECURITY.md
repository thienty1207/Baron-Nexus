# Baron Nexus security scope

Baron handles project-local hook payloads, user-owned DSH/Codex configuration,
Tencent user keys, local SQLite state, backup archives, and recalled external
memory. The security boundary includes path handling, symlink protection,
credential redaction, archive validation, hook command construction, and
historical-memory prompt injection.

Baron deliberately does not treat recalled memory or DuckDuckGo content as
instructions or authorization. It does not persist hidden reasoning, system
prompts, Codex auth files, DeepSeek API keys, SSH keys, Tencent admin keys, or
the application root `.env`.

Please report a reproducible vulnerability with the affected version, command,
input, expected boundary, and sanitized reproduction. Do not include live
credentials or private project contents.
