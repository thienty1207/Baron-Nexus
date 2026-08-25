package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/storage"
)

func TestBackupExcludesSecretsAndRestoreRejectsChecksumCorruption(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project A")
	if err := os.MkdirAll(filepath.Join(root, ".baron"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".baron", "project.toml"), []byte("version = 1\nproject_id = \"prj-a-12345678\"\nname = \"A\"\ncreated_at = \"2026-08-23T00:00:00Z\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".baron", "checkpoint.json"), []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteEnv(filepath.Join(root, ".baron", ".env"), map[string]string{"BARON_TENCENT_USER_KEY": "sk-secret"}); err != nil {
		t.Fatal(err)
	}
	application := New()
	globalPath := filepath.Join(t.TempDir(), "global.json")
	application.GlobalPath = globalPath
	if err := config.SaveGlobalState(globalPath, config.GlobalState{Identity: contracts.Identity{UserKey: "sk-secret", TeamID: "team-a"}}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "baron-backup.tar.gz")
	if err := application.BackupProject(context.Background(), root, archive); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-secret") {
		t.Fatal("portable backup contains plaintext secret")
	}
	corrupt := filepath.Join(t.TempDir(), "corrupt.tar.gz")
	if err := os.WriteFile(corrupt, append(data[:len(data)/2], data[len(data)/2]^0xff), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restore")
	if err := application.RestoreArchive(context.Background(), corrupt, target); err == nil {
		t.Fatal("corrupt backup was accepted")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("corrupt restore mutated target: %v", err)
	}
}

func TestBackupRoundTripsKnowledgeRegistryAndQueueWithoutTencentSecrets(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project Knowledge")
	if err := os.MkdirAll(filepath.Join(root, ".baron", "runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".baron", "project.toml"), []byte("version = 1\nproject_id = \"prj-knowledge-12345678\"\nname = \"Knowledge\"\ncreated_at = \"2026-08-23T00:00:00Z\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(root, ".baron", "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	projectID := "prj-knowledge-12345678"
	ctx := context.Background()
	if err := store.RegisterProject(ctx, storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "Knowledge"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.UpsertKnowledgeRegistry(ctx, storage.KnowledgeRegistry{ProjectID: projectID, TeamID: "team", UserID: "user", AgentID: "agent", WikiID: "wiki-1", CodeGraphID: "graph-1", WikiIngestStatus: "ready", CodeGraphStatus: "ready", CodeGraphCommit: "abc123"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := store.EnqueueSync(ctx, storage.QueueItem{ProjectID: projectID, IdempotencyKey: "knowledge-sync-1", Operation: storage.QueueOperationCodeGraphSync, Payload: []byte(`{"project_id":"prj-knowledge-12345678","code_graph_id":"graph-1"}`)}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteEnv(filepath.Join(root, ".baron", ".env"), map[string]string{"BARON_TENCENT_USER_KEY": "sk-user-secret"}); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	if err := config.SaveGlobalState(application.GlobalPath, config.GlobalState{Identity: contracts.Identity{UserKey: "sk-user-secret", TeamID: "team"}}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "knowledge-backup.tar.gz")
	if err := application.BackupProject(ctx, root, archive); err != nil {
		t.Fatal(err)
	}
	archiveData, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(archiveData), "sk-user-secret") {
		t.Fatal("Tencent user key was persisted in the portable backup")
	}
	target := filepath.Join(t.TempDir(), "restored")
	if err := application.RestoreArchive(ctx, archive, target); err != nil {
		t.Fatal(err)
	}
	restoredStore, err := storage.Open(filepath.Join(target, "project", ".baron", "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restoredStore.Close()
	registry, err := restoredStore.GetKnowledgeRegistry(ctx, projectID)
	if err != nil || registry.WikiID != "wiki-1" || registry.CodeGraphID != "graph-1" || registry.CodeGraphCommit != "abc123" {
		t.Fatalf("knowledge registry was not restored: %#v err=%v", registry, err)
	}
	queue, err := restoredStore.ListQueue(ctx, projectID, "pending", 10)
	if err != nil || len(queue) != 1 || queue[0].Operation != storage.QueueOperationCodeGraphSync {
		t.Fatalf("knowledge queue was not restored: %#v err=%v", queue, err)
	}
	if _, err := os.Stat(filepath.Join(target, "project", ".baron", ".env")); !os.IsNotExist(err) {
		t.Fatalf("project Tencent env was included in restore: %v", err)
	}
}

func TestRestoreConflictRequiresExplicitSafeReplacementAndKeepsRollbackCopy(t *testing.T) {
	root := filepath.Join(t.TempDir(), "restore-source")
	if err := os.MkdirAll(filepath.Join(root, ".baron"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".baron", "project.toml"), []byte("version = 1\nproject_id = \"prj-restore-safe-12345678\"\nname = \"safe\"\ncreated_at = \"2026-08-24T00:00:00Z\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	if err := config.SaveGlobalState(application.GlobalPath, config.GlobalState{}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "safe-restore.tar.gz")
	if err := application.BackupProject(context.Background(), root, archive); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "existing-state")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "user-owned-marker"), []byte("preserve me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := application.RestoreArchive(context.Background(), archive, target); err == nil || !strings.Contains(err.Error(), "--replace-existing") {
		t.Fatalf("existing state did not require explicit safe mode: %v", err)
	}
	if marker, err := os.ReadFile(filepath.Join(target, "user-owned-marker")); err != nil || string(marker) != "preserve me" {
		t.Fatalf("conflict refusal mutated current state: %q err=%v", marker, err)
	}
	if err := application.RestoreArchiveWithOptions(context.Background(), archive, target, RestoreOptions{ReplaceExisting: true}); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(target + ".baron-restore-backup-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("safe restore did not retain one rollback copy: %#v err=%v", backups, err)
	}
	marker, err := os.ReadFile(filepath.Join(backups[0], "user-owned-marker"))
	if err != nil || string(marker) != "preserve me" {
		t.Fatalf("rollback copy lost existing state: %q err=%v", marker, err)
	}
	if _, err := os.Stat(filepath.Join(target, "project", ".baron", "project.toml")); err != nil {
		t.Fatalf("restored state missing project identity: %v", err)
	}
}

func TestRestoreRunsTencentPreflightBeforeInstallingRestoredState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "restore-tencent-source")
	if err := os.MkdirAll(filepath.Join(root, ".baron"), 0o700); err != nil {
		t.Fatal(err)
	}
	projectID := "prj-restore-tencent-12345678"
	if err := os.WriteFile(filepath.Join(root, ".baron", "project.toml"), []byte("version = 1\nproject_id = \""+projectID+"\"\nname = \"restore-tencent\"\ncreated_at = \"2026-08-24T00:00:00Z\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	if err := config.SaveGlobalState(application.GlobalPath, config.GlobalState{
		Identity:           contracts.Identity{Endpoint: "http://tencent.example", TeamID: "team-restore", UserID: "user-restore"},
		TencentInstallPath: "/old-machine/tencent-memory",
		ProjectBindings:    map[string]contracts.ProjectBinding{projectID: {ProjectID: projectID, TeamID: "team-restore", AgentID: "agent-restore", UserID: "user-restore"}},
	}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "restore-tencent.tar.gz")
	if err := application.BackupProject(context.Background(), root, archive); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	called := false
	application.TencentRestore = func(_ context.Context, stage string, state config.GlobalState) (config.GlobalState, error) {
		called = true
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("Tencent preflight ran after target mutation: %v", err)
		}
		if state.Identity.Endpoint != "http://tencent.example" {
			t.Fatalf("Tencent preflight received the wrong restored identity: %#v", state.Identity)
		}
		if _, err := os.Stat(filepath.Join(stage, "project", ".baron", "project.toml")); err != nil {
			t.Fatalf("Tencent preflight did not receive staged project state: %v", err)
		}
		return state, nil
	}
	if err := application.RestoreArchive(context.Background(), archive, target); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("Tencent preflight was not called during restore")
	}
}
