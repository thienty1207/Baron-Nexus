# Baron Nexus 0.1.11 Release Contract

Baron Nexus `0.1.11` fixes clean Ubuntu/WSL initialization with DSH
`0.1.1-rc.2` profiles.

## Changes

- Treat `@deepseek-ai/dsh-mcp-client` as a profile dependency when DSH lists it
  through `pnpm`, instead of requiring it to appear in the composed bundle
  tree.
- Keep Superpowers and Reverse Skill bundle verification strict.
- Preserve idempotent initialization by reading direct profile dependencies
  with `dsh plugin --profile <name> list --depth 0 --json` before reinstalling.
- Preserve the underlying DSH inspection error so a failed `--dump-config`
  reports the command failure cause.

## Verification

```text
go test ./...
go vet ./...
gofmt -l .
git diff --check
sh -n install.sh scripts/build-release.sh
```

The Linux amd64 release artifact is also initialized end-to-end in a clean
WSL Ubuntu prefix with DSH, the Baron adapter, and the DuckDuckGo MCP patch;
a second initialization is idempotent and completes without reinstalling the
MCP dependency.
