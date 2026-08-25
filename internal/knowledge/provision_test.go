package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/memory/tencent"
	"github.com/baron-shared-brain/baron/internal/storage"
)

func TestCollectSeedFilesExcludesSecretsIgnoredAndRuntimeState(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".baron"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"README.md":              "# Project\n",
		"docs/architecture.md":   "# Architecture\n",
		"docs/ignored.md":        "ignored\n",
		".env":                   "API_KEY=sk-secret\n",
		".baron/checkpoint.json": `{"secret":"sk-secret"}`,
		"main.go":                "package main\n",
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("docs/ignored.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", root, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	filesOut, err := CollectSeedFiles(root, "demo")
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, file := range filesOut {
		joined += file.Filename + "\n" + file.Content
	}
	if !strings.Contains(joined, "README.md") || !strings.Contains(joined, "docs/architecture.md") {
		t.Fatalf("expected documentation was not collected: %s", joined)
	}
	for _, forbidden := range []string{".env", ".baron", "ignored.md", "sk-secret", "main.go"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("forbidden seed content %q was uploaded: %s", forbidden, joined)
		}
	}
}

func TestCollectSeedFilesRedactsLoadedProviderSecrets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("provider token: custom-provider-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := CollectSeedFilesWithSecrets(root, "demo", []string{"custom-provider-secret"})
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, file := range files {
		joined += file.Content
	}
	if strings.Contains(joined, "custom-provider-secret") || !strings.Contains(joined, "[REDACTED]") {
		t.Fatalf("loaded provider secret was not redacted from Wiki seed: %s", joined)
	}
}

func TestProvisionProjectCreatesAndReusesIsolatedAssets(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if request.Body != nil {
			_ = json.NewDecoder(request.Body).Decode(&body)
		}
		if body == nil {
			body = map[string]any{}
		}
		if request.URL.Path == "/v3/code-graph/status" {
			if len(body) != 1 || body["code_graph_id"] != "graph-1" {
				t.Fatalf("narrow CodeGraph status body=%#v", body)
			}
		} else {
			for key, want := range map[string]string{"team_id": "team-a", "agent_id": "agent-a", "user_id": "user-a", "project_id": "prj-demo-12345678"} {
				if body[key] != want {
					t.Fatalf("%s=%v, want %s on %s", key, body[key], want, request.URL.Path)
				}
			}
		}
		mu.Lock()
		counts[request.URL.Path]++
		mu.Unlock()
		data := any(map[string]any{})
		switch request.URL.Path {
		case "/v3/wiki/list", "/v3/code-graph/list", "/v3/knowledge/list":
			data = map[string]any{"items": []any{}}
		case "/v3/wiki/create", "/v3/wiki/get":
			data = map[string]any{"id": "wiki-1", "wiki_id": "wiki-1", "name": "Baron Nexus project wiki prj-demo-12345678", "status": "ready"}
		case "/v3/code-graph/create", "/v3/code-graph/get":
			data = map[string]any{"id": "graph-1", "code_graph_id": "graph-1", "status": "ready"}
		case "/v3/knowledge/create":
			data = map[string]any{"knowledge_id": "meta-" + strings.ReplaceAll(request.URL.Path, "/", "-")}
		case "/v3/code-graph/status":
			data = map[string]any{"status": "ready"}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "ok", "data": data})
	}))
	defer server.Close()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	projectID := "prj-demo-12345678"
	if err := store.RegisterProject(ctx, storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "demo"}); err != nil {
		t.Fatal(err)
	}
	isolation := contracts.IsolationContext{ProjectID: projectID, TeamID: "team-a", AgentID: "agent-a", UserID: "user-a", ServiceID: "baron"}
	core := tencent.NewClient(tencent.Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()})
	knowledgeClient := tencent.NewKnowledgeClient(tencent.Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()})
	options := ProvisionOptions{Root: root, ProjectID: projectID, ProjectName: "demo", Isolation: isolation, Core: core, Knowledge: knowledgeClient, ServiceURL: server.URL + "/v3", Store: store, Repository: RepositoryInfo{Remote: "https://example.com/demo.git", Branch: "main", Commit: "abc123"}}
	first, err := ProvisionProject(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	var second storage.KnowledgeRegistry
	for rerun := 0; rerun < 4; rerun++ {
		second, err = ProvisionProject(ctx, options)
		if err != nil {
			t.Fatal(err)
		}
	}
	if first.WikiID != "wiki-1" || first.CodeGraphID != "graph-1" || first.WikiIngestStatus != "ready" || first.CodeGraphSyncStatus != "ready" || second.WikiID != first.WikiID || second.CodeGraphID != first.CodeGraphID {
		t.Fatalf("asset mapping was not stable: first=%#v second=%#v", first, second)
	}
	changed := options
	changed.Repository.Commit = "def456"
	third, err := ProvisionProject(ctx, changed)
	if err != nil {
		t.Fatal(err)
	}
	if third.LastSyncCommit != "def456" || third.CodeGraphCommit != "def456" || third.SupersededBy != "def456" || third.ConflictStatus != "superseded" {
		t.Fatalf("commit freshness/supersession was not recorded: %#v", third)
	}
	mu.Lock()
	defer mu.Unlock()
	if counts["/v3/wiki/create"] != 1 || counts["/v3/code-graph/create"] != 1 {
		t.Fatalf("rerun duplicated remote assets: %#v", counts)
	}
	if counts["/v3/wiki/raw/write"] != 6 || counts["/v3/wiki/ingest"] != 6 || counts["/v3/code-graph/sync"] != 6 {
		t.Fatalf("rerun did not refresh bounded knowledge sources: %#v", counts)
	}
}

func TestKnowledgeOutageKeepsRegistryAndQueuesTypedWikiRepair(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v3/wiki/list":
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"items": []any{}}})
		case "/v3/wiki/create":
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"id": "wiki-outage", "wiki_id": "wiki-outage", "status": "ready"}})
		case "/v3/wiki/raw/write":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"error":"knowledge service stopped"}`))
		default:
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{}})
		}
	}))
	defer server.Close()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# outage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	projectID := "prj-outage-12345678"
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "outage"}); err != nil {
		t.Fatal(err)
	}
	isolation := contracts.IsolationContext{ProjectID: projectID, TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	client := tencent.NewKnowledgeClient(tencent.Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()})
	registry, provisionErr := ProvisionProject(context.Background(), ProvisionOptions{Root: root, ProjectID: projectID, ProjectName: "outage", Isolation: isolation, Knowledge: client, ServiceURL: server.URL + "/v3", Store: store, ReadinessBudget: time.Millisecond, PollInterval: time.Millisecond})
	if provisionErr == nil || registry.WikiID != "wiki-outage" {
		t.Fatalf("knowledge outage was not classified while preserving registry: %#v err=%v", registry, provisionErr)
	}
	items, err := store.ListQueue(context.Background(), projectID, "pending", 10)
	if err != nil || len(items) != 1 || items[0].Operation != storage.QueueOperationWikiIngest {
		t.Fatalf("Wiki outage did not queue typed repair: %#v err=%v", items, err)
	}
	if _, err := store.GetKnowledgeRegistry(context.Background(), projectID); err != nil {
		t.Fatalf("local continuity registry was lost during outage: %v", err)
	}
}

func TestProvisionProjectReconstructsRemoteAssetsAfterLocalRegistryLoss(t *testing.T) {
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		counts[request.URL.Path]++
		data := map[string]any{}
		switch request.URL.Path {
		case "/v3/wiki/list":
			data = map[string]any{"items": []any{map[string]any{"id": "wiki-existing", "wiki_id": "wiki-existing", "name": "Baron Nexus project wiki prj-reconstruct-12345678", "status": "ready"}}}
		case "/v3/code-graph/list":
			data = map[string]any{"items": []any{map[string]any{"id": "graph-existing", "code_graph_id": "graph-existing", "name": "Baron Nexus project code graph prj-reconstruct-12345678", "status": "ready"}}}
		case "/v3/wiki/create":
			data = map[string]any{"id": "unexpected-wiki-create", "wiki_id": "unexpected-wiki-create"}
		case "/v3/code-graph/create":
			data = map[string]any{"id": "unexpected-graph-create", "code_graph_id": "unexpected-graph-create"}
		case "/v3/code-graph/status":
			data = map[string]any{"status": "ready"}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# reconstruct\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	projectID := "prj-reconstruct-12345678"
	if err := store.RegisterProject(ctx, storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "reconstruct"}); err != nil {
		t.Fatal(err)
	}
	isolation := contracts.IsolationContext{ProjectID: projectID, TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	client := tencent.NewKnowledgeClient(tencent.Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()})
	options := ProvisionOptions{Root: root, ProjectID: projectID, ProjectName: "reconstruct", Isolation: isolation, Knowledge: client, ServiceURL: server.URL + "/v3", Store: store, Repository: RepositoryInfo{Remote: "https://example.com/reconstruct.git", Branch: "main", Commit: "abc123"}}
	first, err := ProvisionProject(ctx, options)
	if err != nil || first.WikiID != "wiki-existing" || first.CodeGraphID != "graph-existing" {
		t.Fatalf("remote asset reconstruction failed: %#v err=%v", first, err)
	}
	if err := store.DeleteKnowledgeRegistry(ctx, projectID); err != nil {
		t.Fatal(err)
	}
	second, err := ProvisionProject(ctx, options)
	if err != nil || second.WikiID != first.WikiID || second.CodeGraphID != first.CodeGraphID {
		t.Fatalf("registry loss did not reconstruct the same assets: first=%#v second=%#v err=%v", first, second, err)
	}
	if counts["/v3/wiki/create"] != 0 || counts["/v3/code-graph/create"] != 0 {
		t.Fatalf("registry repair created duplicate remote assets: %#v", counts)
	}
}

func TestTwoSameNamedProjectsReceiveIsolatedKnowledgeAssets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		projectID := ""
		if request.URL.Path == "/v3/code-graph/status" {
			if len(body) != 1 || body["code_graph_id"] == nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
		} else {
			projectID, _ = body["project_id"].(string)
			if !strings.HasPrefix(projectID, "prj-") {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		data := map[string]any{}
		switch request.URL.Path {
		case "/v3/wiki/list", "/v3/code-graph/list":
			data = map[string]any{"items": []any{}}
		case "/v3/wiki/create":
			data = map[string]any{"id": "wiki-" + projectID, "wiki_id": "wiki-" + projectID, "status": "ready"}
		case "/v3/code-graph/create":
			data = map[string]any{"id": "graph-" + projectID, "code_graph_id": "graph-" + projectID, "status": "ready"}
		case "/v3/code-graph/status":
			data = map[string]any{"status": "ready"}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	root := t.TempDir()
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	projects := []struct {
		id    string
		root  string
		team  string
		agent string
	}{
		{id: "prj-same-a-12345678", root: filepath.Join(root, "a"), team: "team-a", agent: "agent-a"},
		{id: "prj-same-b-12345678", root: filepath.Join(root, "b"), team: "team-b", agent: "agent-b"},
	}
	client := tencent.NewKnowledgeClient(tencent.Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()})
	registries := make([]storage.KnowledgeRegistry, 0, len(projects))
	for _, item := range projects {
		if err := os.MkdirAll(item.root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(item.root, "README.md"), []byte("# same name\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := store.RegisterProject(ctx, storage.ProjectRecord{ProjectID: item.id, Root: item.root, Name: "same"}); err != nil {
			t.Fatal(err)
		}
		registry, provisionErr := ProvisionProject(ctx, ProvisionOptions{Root: item.root, ProjectID: item.id, ProjectName: "same", Isolation: contracts.IsolationContext{ProjectID: item.id, TeamID: item.team, AgentID: item.agent, UserID: "user-" + item.team}, Knowledge: client, ServiceURL: server.URL + "/v3", Store: store, Repository: RepositoryInfo{Remote: "https://example.com/same.git", Branch: "main", Commit: "abc123"}})
		if provisionErr != nil {
			t.Fatal(provisionErr)
		}
		registries = append(registries, registry)
	}
	if registries[0].WikiID == registries[1].WikiID || registries[0].CodeGraphID == registries[1].CodeGraphID || registries[0].TeamID == registries[1].TeamID || registries[0].AgentID == registries[1].AgentID {
		t.Fatalf("same-named projects were not isolated: %#v", registries)
	}
}
