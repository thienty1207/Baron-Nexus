# Baron Nexus Codex adapter

This package is the explicit Codex lifecycle bridge. It accepts a Codex hook
event and forwards the JSON payload to the Baron Go hook runtime:

```text
baron hook codex <event>
```

The default Codex installation keeps hooks on the direct Go command so a
normal session does not depend on Node or a package path. The bridge is
materialized under Baron-owned global state for Codex environments that need
an explicit adapter entrypoint. It never reads or writes Codex skills,
provider settings, or authentication files.
