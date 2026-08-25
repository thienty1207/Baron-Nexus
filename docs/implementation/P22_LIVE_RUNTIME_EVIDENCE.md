# P22 live Tencent runtime evidence

This record separates the upstream checkout/download probe from the Docker and
provider-backed acceptance that still requires user-owned runtime access.

- `git ls-remote` confirmed the pinned Baron ref
  `97f94654280b2932c35ba4806a491999ed244cc9` on the official Tencent
  repository HEAD.
- The pinned checkout was downloaded with a shallow blob-filter into the
  Baron-owned temporary path `/tmp/baron-tencent-live.m4rWWP` and detached at
  that exact commit.
- The official `deploy/global-images/*.sh` scripts pass `bash -n`. The checkout
  contains the official three-service flow: Memory Core, Memory Hub/Knowledge,
  and Proxy, with the expected image/env/health scripts.
- Baron now resolves provider values in the order process environment, managed
  deployment `.env`, DSH's official credential store, then safe DeepSeek
  defaults; an interactive missing-key path uses hidden input. Tencent admin
  authentication is likewise reused from the protected managed file or
  collected in-process, never copied into project state or printed.
- On the development host, all four managed health endpoints returned HTTP
  `200`: MemoryCore (`8420`), MemoryHub (`8125`), Proxy (`8096`), and Knowledge
  (`8424`). A disposable live `baron setup` created/reused an Agent and Wiki.
- The first live Agent create exposed a real API contract mismatch because
  Tencent requires `owner_user_id`; Baron now sends the authenticated user ID,
  and the rerun completed successfully. A Git-backed project created an
  asynchronous CodeGraph and Baron retained its `processing`/`pending` state
  for retry instead of claiming readiness; Wiki reached `ready` on the next
  setup rerun.
- Two live disposable projects were then set up repeatedly. Project A and
  Project B kept distinct Baron project IDs and distinct Tencent Agent IDs
  under the same team; a second setup on each reused the existing binding
  instead of creating a duplicate. Both Wiki assets reported `ready` after
  the repair/recheck pass.
- A real Git-backed disposable project exposed an upstream schema difference
  on `/v3/code-graph/status`: the route accepts only `code_graph_id`, unlike
  the other isolated Knowledge routes. Baron now uses that exact narrow request
  shape; the live status endpoint returns successfully, while Tencent's
  asynchronous indexing job remains `processing`/`pending` and is retained in
  the local repair queue rather than being reported as ready.
- A live L0 capture was read back through Tencent conversation query. The
  probe also exposed the upstream `scenario/read` requirement for a non-empty
  `path`; Baron now treats that endpoint as path-addressed and does not turn a
  generic recall into a false remote outage.
- Clean-machine bootstrap, five-run identity acceptance, service
  restart/volume checks, and release authentication remain external gates. The
  current agent terminal has no cached sudo ticket, so those gates are not
  falsely marked green here.

## Fresh recheck — 2026-08-25

- The requested non-interactive host probes both returned
  `sudo: interactive authentication is required`; direct Docker access also
  returned a `/var/run/docker.sock` permission error.
- Direct read-only probes of MemoryCore (`8420`), MemoryHub (`8125`), Proxy
  (`8096`), and Knowledge (`8424`) each returned HTTP `200`.
- The current project Wiki (`wiki-isuohvf4`) has ten raw source entries, while
  Tencent's `/v3/wiki/get` still reports `status=processing`, version `6`, and
  the unchanged `last_sync_at`. Baron therefore keeps local Wiki state at
  `pending`; no asynchronous completion was invented.
- The current-source regression fix makes health-first `baron repair` skip
  Docker bootstrap/deployment when those four endpoints are healthy. The
  regression test and a real rebuilt-binary `baron repair` both exited `0`.
- The upstream Knowledge compose file parses with its documented
  `docker/env.example` (`docker compose --env-file docker/env.example config
  --quiet`). Running it without that required env file correctly fails on
  `PUBLIC_URL` and is not counted as a pass.
- These results strengthen local/live health evidence but do not close the
  clean-machine bootstrap, restart/volume, legacy snapshot, CodeGraph
  completion, or authenticated release gates.

The downloaded source is evidence of the pinned acquisition path only. It is
not counted as a live Tencent service pass.
