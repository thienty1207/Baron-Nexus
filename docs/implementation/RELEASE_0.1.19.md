# Baron Nexus 0.1.19

## Knowledge and credential recovery

- Register project Wiki and CodeGraph assets in Tencent MemoryCore metadata and
  bind both assets to the project agent idempotently.
- Rotate a validated DeepSeek key into the official DSH store and the managed
  Tencent runtime environment without exposing the secret in Baron state,
  receipts, or diagnostics.
- Recreate the managed Tencent containers after key rotation so the running
  Wiki worker receives the new credential instead of a stale container
  environment.
- Automatically retry the current initialized Baron project from
  `baron deepseek api_key`; a separate `baron setup` or manual `.env` edit is
  not required.
- Preserve asynchronous Wiki and CodeGraph processing as `pending` or
  `processing` until the upstream service reports readiness, while queueing
  deterministic repair work for later polling.

## Verification

- Targeted app, install, Tencent metadata, and knowledge queue tests pass.
- `go vet ./internal/app ./internal/install` passes.
- Linux and Windows amd64 binaries build from the same source revision.
