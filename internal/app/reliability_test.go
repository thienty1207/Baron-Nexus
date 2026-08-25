package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/continuity"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/hooks"
	"github.com/baron-shared-brain/baron/internal/knowledge"
	"github.com/baron-shared-brain/baron/internal/memory/tencent"
	"github.com/baron-shared-brain/baron/internal/storage"
)

func TestBackupRestoreReusesKnowledgeAssetsWithoutDuplicate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "knowledge-project")
	if err := os.MkdirAll(filepath.Join(root, ".baron", "runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Restored knowledge project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	projectID := "prj-backup-knowledge-12345678"
	if err := os.WriteFile(filepath.Join(root, ".baron", "project.toml"), []byte("version = 1\nproject_id = \""+projectID+"\"\nname = \"Restored knowledge\"\ncreated_at = \"2026-08-24T00:00:00Z\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(root, ".baron", "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	registry := storage.KnowledgeRegistry{
		ProjectID: projectID, TeamID: "team-backup", UserID: "user-backup", AgentID: "agent-backup",
		WikiID: "wiki-existing", CodeGraphID: "graph-existing", WikiMetadataID: "wiki-meta-existing", CodeGraphMetadataID: "graph-meta-existing",
		ServiceURL: "https://knowledge.example/v3", Repository: "https://example.com/restored.git", Branch: "main", LastSyncCommit: "commit-old",
		WikiStatus: "ready", WikiIngestStatus: "ready", CodeGraphStatus: "ready", CodeGraphSyncStatus: "ready", CodeGraphCommit: "commit-old",
	}
	if err := store.RegisterProject(ctx, storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "Restored knowledge"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.UpsertKnowledgeRegistry(ctx, registry); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	if err := config.SaveGlobalState(application.GlobalPath, config.GlobalState{Identity: contracts.Identity{UserKey: "sk-backup-secret", TeamID: "team-backup", UserID: "user-backup"}}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "knowledge.tar.gz")
	if err := application.BackupProject(ctx, root, archive); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if err := application.RestoreArchive(ctx, archive, target); err != nil {
		t.Fatal(err)
	}

	var createCalls map[string]int = map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v3/wiki/create" || request.URL.Path == "/v3/code-graph/create" {
			createCalls[request.URL.Path]++
		}
		var data any = map[string]any{}
		switch request.URL.Path {
		case "/v3/wiki/get":
			data = map[string]any{"id": "wiki-existing", "wiki_id": "wiki-existing", "name": "restored", "status": "ready"}
		case "/v3/wiki/raw/write", "/v3/wiki/ingest":
			data = map[string]any{"request_id": "wiki-request"}
		case "/v3/code-graph/get":
			data = map[string]any{"id": "graph-existing", "code_graph_id": "graph-existing", "status": "ready"}
		case "/v3/code-graph/sync":
			data = map[string]any{"request_id": "graph-request"}
		case "/v3/code-graph/status":
			data = map[string]any{"status": "ready", "commit_hash": "commit-old"}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "ok", "data": data})
	}))
	defer server.Close()

	restoredStore, err := storage.Open(filepath.Join(target, "project", ".baron", "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restoredStore.Close()
	client := tencent.NewKnowledgeClient(tencent.Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk-backup-secret", ServiceID: "baron", HTTPClient: server.Client()})
	restored, err := knowledge.ProvisionProject(ctx, knowledge.ProvisionOptions{
		Root: filepath.Join(target, "project"), ProjectID: projectID, ProjectName: "Restored knowledge",
		Isolation: contracts.IsolationContext{ProjectID: projectID, TeamID: "team-backup", UserID: "user-backup", AgentID: "agent-backup"},
		Knowledge: client, ServiceURL: server.URL + "/v3", Store: restoredStore,
		Repository: knowledge.RepositoryInfo{Remote: "https://example.com/restored.git", Branch: "main", Commit: "commit-old"},
	})
	if err != nil || restored.WikiID != "wiki-existing" || restored.CodeGraphID != "graph-existing" {
		t.Fatalf("restored mapping was not reused: %#v err=%v", restored, err)
	}
	if createCalls["/v3/wiki/create"] != 0 || createCalls["/v3/code-graph/create"] != 0 {
		t.Fatalf("backup restore created duplicate remote assets: %#v", createCalls)
	}
}

type unavailableSecretBackend struct{}

func (unavailableSecretBackend) Health(context.Context) error {
	return errors.New("provider unavailable")
}
func (unavailableSecretBackend) EnsureIdentity(context.Context, contracts.IdentitySpec) (contracts.Identity, error) {
	return contracts.Identity{}, errors.New("provider unavailable")
}
func (unavailableSecretBackend) EnsureProjectAgent(context.Context, contracts.IsolationContext, string) (contracts.ProjectBinding, error) {
	return contracts.ProjectBinding{}, errors.New("provider unavailable")
}
func (unavailableSecretBackend) Capture(context.Context, contracts.IsolationContext, contracts.MemoryRecord, string) (contracts.MemoryReceipt, error) {
	return contracts.MemoryReceipt{}, errors.New("provider unavailable")
}
func (unavailableSecretBackend) Search(context.Context, contracts.IsolationContext, contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	return nil, errors.New("provider unavailable")
}

func TestExpandedSecretCorpusStaysOutOfContinuityKnowledgeAndBackupArtifacts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "secret-project")
	if err := os.MkdirAll(filepath.Join(root, ".baron", "runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	projectID := "prj-secret-corpus-12345678"
	if err := os.WriteFile(filepath.Join(root, ".baron", "project.toml"), []byte("version = 1\nproject_id = \""+projectID+"\"\nname = \"secret corpus\"\ncreated_at = \"2026-08-24T00:00:00Z\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("provider note: sk-provider-corpus-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := []string{"sk-admin-corpus-secret", "sk-user-corpus-secret", "sk-provider-corpus-secret"}
	store, err := storage.Open(filepath.Join(root, ".baron", "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.RegisterProject(ctx, storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "secret corpus"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	runtime := hooks.NewRuntime(store, continuity.NewEngine(store, projectID, "secret corpus", filepath.Join(root, ".baron", "checkpoint.json")), projectID)
	runtime.SetSecrets(secrets)
	runtime.SetMemoryBackend(unavailableSecretBackend{}, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"})
	payload := `{"command":"echo sk-admin-corpus-secret","summary":"user=sk-user-corpus-secret provider=sk-provider-corpus-secret"}`
	if _, err := runtime.Handle(ctx, hooks.Request{Client: contracts.ClientCodex, Event: contracts.EventToolFinished, ProjectID: projectID, SessionID: "secret-session", IdempotencyKey: "secret-corpus-event", Payload: []byte(payload)}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	logPath := filepath.Join(root, ".baron", "runtime", "hook.log")
	if err := hooks.AppendLog(logPath, payload+"\n", secrets, 64*1024); err != nil {
		store.Close()
		t.Fatal(err)
	}
	seedFiles, err := knowledge.CollectSeedFilesWithSecrets(root, "secret corpus", secrets)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	for _, file := range seedFiles {
		assertNoSecrets(t, "knowledge seed "+file.Filename, []byte(file.Content), secrets)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(root, ".baron", "runtime", "state.db"),
		filepath.Join(root, ".baron", "runtime", "state.db-wal"),
		filepath.Join(root, ".baron", "runtime", "state.db-shm"),
		filepath.Join(root, ".baron", "checkpoint.json"), logPath, logPath + ".1",
	} {
		data, readErr := os.ReadFile(path)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		assertNoSecrets(t, path, data, secrets)
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	if err := config.SaveGlobalState(application.GlobalPath, config.GlobalState{Identity: contracts.Identity{UserKey: secrets[1], TeamID: "team"}}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "secret-corpus.tar.gz")
	if err := application.BackupProject(ctx, root, archive); err != nil {
		t.Fatal(err)
	}
	archiveData, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecrets(t, archive, archiveData, secrets)
}

func assertNoSecrets(t *testing.T, name string, data []byte, secrets []string) {
	t.Helper()
	text := string(data)
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("raw secret %q persisted in %s", secret, name)
		}
	}
}
