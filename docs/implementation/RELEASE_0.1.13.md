# Baron Nexus 0.1.13 Release Contract

Baron Nexus `0.1.13` fixes Docker object cleanup found by a real Ubuntu/WSL
full uninstall run of `0.1.12`.

## Changes

- Remove Docker volumes with `docker volume rm <id>`.
- Remove Docker custom networks with `docker network rm <id>`.
- Keep the full purge cleanup for recreated npm caches, known Codex launchers,
  and Baron update backups.

## Verification

```text
go test ./...
go vet ./...
gofmt -l internal/uninstall/full_purge.go internal/uninstall/uninstall.go internal/uninstall/uninstall_test.go
git diff --check
sh -n install.sh scripts/build-release.sh
```

The Docker cleanup command shape is covered by a regression test and was
reproduced with real Ubuntu Docker volume and network objects before the fix.
