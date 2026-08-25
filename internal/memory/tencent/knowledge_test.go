package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

func TestKnowledgeClientWikiAndCodeGraphUseV3Isolation(t *testing.T) {
	paths := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("x-tdai-service-id") != "baron-service" || request.Header.Get("Authorization") != "Bearer sk-knowledge" {
			t.Fatalf("knowledge auth headers missing")
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{"team_id": "team-a", "user_id": "user-a", "agent_id": "agent-a"} {
			if body[key] != want {
				t.Fatalf("%s isolation=%#v body=%#v", key, body, body)
			}
		}
		if strings.Contains(string(mustJSON(body)), "sk-knowledge") || strings.Contains(string(mustJSON(body)), "custom-provider-secret") {
			t.Fatal("knowledge request body exposed user key")
		}
		paths = append(paths, request.URL.Path)
		switch request.URL.Path {
		case "/v3/wiki/create":
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"wiki_id": "wiki-1", "name": "baron"}})
		case "/v3/code-graph/search":
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"text": "symbol result"}})
		default:
			t.Fatalf("unexpected knowledge path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	client := NewKnowledgeClient(Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk-knowledge", Secrets: []string{"custom-provider-secret"}, ServiceID: "baron-service", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	wiki, err := client.CreateWiki(context.Background(), isolation, "baron")
	if err != nil || wiki.ID != "wiki-1" {
		t.Fatalf("create wiki failed: %#v %v", wiki, err)
	}
	result, err := client.SearchCodeGraph(context.Background(), isolation, "cg-1", "symbol custom-provider-secret")
	if err != nil || !strings.Contains(string(result.Data), "symbol result") {
		t.Fatalf("code graph search failed: %#v %v", result, err)
	}
	if strings.Join(paths, ",") != "/v3/wiki/create,/v3/code-graph/search" {
		t.Fatalf("unexpected knowledge paths: %#v", paths)
	}
}

func TestKnowledgeClientRejectsMissingIsolationBeforeNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	client := NewKnowledgeClient(Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk", ServiceID: "service", HTTPClient: server.Client()})
	_, err := client.CreateWiki(context.Background(), contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", UserID: "user-a"}, "baron")
	if err == nil || !strings.Contains(err.Error(), "agent_id") {
		t.Fatalf("missing isolation was not rejected: %v", err)
	}
	if requests != 0 {
		t.Fatalf("network was contacted after isolation failure: %d", requests)
	}
}

func TestCodeGraphStatusUsesItsNarrowUpstreamSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/code-graph/status" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body["code_graph_id"] != "cg-status-1" {
			t.Fatalf("status request included unsupported identity fields: %#v", body)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"status": "ready", "code_graph_id": "cg-status-1"}})
	}))
	defer server.Close()

	client := NewKnowledgeClient(Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk-status", ServiceID: "baron", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-status-12345678", TeamID: "team-status", AgentID: "agent-status", UserID: "user-status"}
	result, err := client.StatusCodeGraph(context.Background(), isolation, "cg-status-1")
	if err != nil || !strings.Contains(string(result.Data), `"status":"ready"`) {
		t.Fatalf("code graph status failed: %#v %v", result, err)
	}
}

func TestKnowledgeToolsUseTheSelectedKnowledgeResource(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["knowledge_id"] != "wiki-tools-1" {
			t.Fatalf("knowledge resource was not scoped: %#v", body)
		}
		if request.URL.Path == "/v3/tools/call" && (body["tool_name"] != "wiki_search" || body["params"] == nil) {
			t.Fatalf("tool call used the wrong Tencent schema: %#v", body)
		}
		paths = append(paths, request.URL.Path)
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"tools": []any{}}})
	}))
	defer server.Close()
	client := NewKnowledgeClient(Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk-tools", ServiceID: "default", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-tools-12345678", TeamID: "team-tools", AgentID: "agent-tools", UserID: "user-tools"}
	if _, err := client.ListTools(context.Background(), isolation, "wiki-tools-1"); err != nil {
		t.Fatalf("tools list failed: %v", err)
	}
	if _, err := client.CallTool(context.Background(), isolation, "wiki-tools-1", "wiki_search", map[string]any{"query": "Baron"}); err != nil {
		t.Fatalf("tool call failed: %v", err)
	}
	if strings.Join(paths, ",") != "/v3/tools/list,/v3/tools/call" {
		t.Fatalf("unexpected tools paths: %#v", paths)
	}
}

func TestWriteWikiRawRetriesNestedSourcesWithFlatNamesForLegacyService(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/wiki/raw/write" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		requests++
		var body struct {
			Files []KnowledgeFile `json:"files"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if requests == 1 {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"code":400,"message":"ENOENT: no such file or directory"}`))
			return
		}
		for _, file := range body.Files {
			if strings.Contains(file.Filename, "/") {
				t.Fatalf("legacy retry retained nested filename %q", file.Filename)
			}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"ok": true}})
	}))
	defer server.Close()

	client := NewKnowledgeClient(Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk", ServiceID: "baron", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	_, err := client.WriteWikiRaw(context.Background(), isolation, "wiki-a", []KnowledgeFile{
		{Filename: "adapters/codex/README.md", Content: "adapter"},
		{Filename: "docs/guide.md", Content: "guide"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("legacy nested-path fallback requests=%d, want 2", requests)
	}
}

func TestWriteWikiRawBatchesToLegacyServiceFileLimit(t *testing.T) {
	requestSizes := make([]int, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Files []KnowledgeFile `json:"files"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		requestSizes = append(requestSizes, len(body.Files))
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"ok": true}})
	}))
	defer server.Close()

	files := make([]KnowledgeFile, 11)
	for index := range files {
		files[index] = KnowledgeFile{Filename: "doc-" + string(rune('a'+index)) + ".md", Content: "content"}
	}
	client := NewKnowledgeClient(Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk", ServiceID: "baron", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	if _, err := client.WriteWikiRaw(context.Background(), isolation, "wiki-a", files); err != nil {
		t.Fatal(err)
	}
	if len(requestSizes) != 2 || requestSizes[0] != 10 || requestSizes[1] != 1 {
		t.Fatalf("raw write batches=%v, want [10 1]", requestSizes)
	}
}

func TestKnowledgeToolCallUsesAllowlistAndBoundedArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/tools/call" {
			t.Fatalf("unexpected tools path %s", request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"ok": true}})
	}))
	defer server.Close()
	client := NewKnowledgeClient(Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk", ServiceID: "service", HTTPClient: server.Client()})
	result, err := client.CallTool(context.Background(), contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}, "graph-a", "code_graph_search", map[string]any{"query": "auth"})
	if err != nil || !strings.Contains(string(result.Data), "ok") {
		t.Fatalf("allowlisted tool call failed: %#v %v", result, err)
	}
	if _, err := client.CallTool(context.Background(), contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}, "graph-a", "shell", nil); err == nil || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatalf("unallowlisted tool was not rejected: %v", err)
	}
}

func TestKnowledgeReadinessPollingAcceptsStatusEnvelopeAndHonorsBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		status := "ready"
		if request.URL.Path == "/v3/wiki/get" {
			status = "pending"
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"status": status}})
	}))
	defer server.Close()
	client := NewKnowledgeClient(Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk", ServiceID: "service", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	graph, err := client.WaitCodeGraphReady(context.Background(), isolation, "graph-1", time.Millisecond)
	if err != nil || graph.ID != "graph-1" || graph.Status != "ready" {
		t.Fatalf("status-only CodeGraph readiness failed: %#v %v", graph, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := client.WaitWikiReady(ctx, isolation, "wiki-1", time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wiki readiness did not honor bounded context: %v", err)
	}
}

func TestWaitCodeGraphReadyFallsBackToAssetGetForTextStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["code_graph_id"] != "graph-text-status" {
			t.Fatalf("unexpected graph id: %#v", body)
		}
		switch request.URL.Path {
		case "/v3/code-graph/status":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"text": "**CodeGraph Status**\\n\\n**Files indexed:** 3"},
			})
		case "/v3/code-graph/get":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"code_graph_id": "graph-text-status", "status": "ready", "stats": map[string]any{"files": 3}},
			})
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := NewKnowledgeClient(Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk-status", ServiceID: "baron", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-text-status-12345678", TeamID: "team-status", AgentID: "agent-status", UserID: "user-status"}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	graph, err := client.WaitCodeGraphReady(ctx, isolation, "graph-text-status", time.Millisecond)
	if err != nil || graph.Status != "ready" || graph.ID != "graph-text-status" {
		t.Fatalf("text-only status did not fall back to CodeGraph get: %#v %v", graph, err)
	}
}

func TestWikiAndCodeGraphLifecycleUsesEveryBoundedRoute(t *testing.T) {
	paths := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths[request.URL.Path] = true
		data := map[string]any{"ok": true}
		switch request.URL.Path {
		case "/v3/wiki/create", "/v3/wiki/get":
			data = map[string]any{"id": "wiki-1", "wiki_id": "wiki-1", "name": "baron", "status": "ready"}
		case "/v3/code-graph/create", "/v3/code-graph/get":
			data = map[string]any{"id": "graph-1", "code_graph_id": "graph-1", "status": "ready"}
		case "/v3/code-graph/status":
			data = map[string]any{"status": "ready", "commit_hash": "abc123"}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "ok", "data": data})
	}))
	defer server.Close()
	client := NewKnowledgeClient(Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-lifecycle-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	wiki, err := client.CreateWiki(context.Background(), isolation, "baron")
	if err != nil || wiki.ID != "wiki-1" {
		t.Fatalf("create Wiki failed: %#v %v", wiki, err)
	}
	if _, err := client.GetWiki(context.Background(), isolation, wiki.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.WriteWikiRaw(context.Background(), isolation, wiki.ID, []KnowledgeFile{{Filename: "README.md", Content: "docs"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListWikiRaw(context.Background(), isolation, wiki.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadWikiRaw(context.Background(), isolation, wiki.ID, []string{"README.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.IngestWiki(context.Background(), isolation, wiki.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SearchWiki(context.Background(), isolation, wiki.ID, "docs", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := client.WriteWikiPages(context.Background(), isolation, wiki.ID, []KnowledgePage{{Ref: "README", Content: "docs"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListWikiPages(context.Background(), isolation, wiki.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadWikiPages(context.Background(), isolation, wiki.ID, []string{"README"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.WikiGraph(context.Background(), isolation, wiki.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RemoveWikiPages(context.Background(), isolation, wiki.ID, []string{"README"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RemoveWikiRaw(context.Background(), isolation, wiki.ID, []string{"README.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeleteWikis(context.Background(), isolation, []string{wiki.ID}); err != nil {
		t.Fatal(err)
	}

	graph, err := client.CreateCodeGraph(context.Background(), isolation, "https://example.com/repo.git", "main", "repo")
	if err != nil || graph.ID != "graph-1" {
		t.Fatalf("create CodeGraph failed: %#v %v", graph, err)
	}
	if _, err := client.GetCodeGraph(context.Background(), isolation, graph.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SyncCodeGraph(context.Background(), isolation, graph.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.WaitCodeGraphReady(context.Background(), isolation, graph.ID, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() (KnowledgeResult, error){
		func() (KnowledgeResult, error) {
			return client.SearchCodeGraph(context.Background(), isolation, graph.ID, "Auth")
		},
		func() (KnowledgeResult, error) {
			return client.ExploreCodeGraph(context.Background(), isolation, graph.ID, "Auth", 5)
		},
		func() (KnowledgeResult, error) {
			return client.Callers(context.Background(), isolation, graph.ID, "Auth.Refresh", 5)
		},
		func() (KnowledgeResult, error) {
			return client.Callees(context.Background(), isolation, graph.ID, "Auth.Refresh", 5)
		},
		func() (KnowledgeResult, error) {
			return client.Impact(context.Background(), isolation, graph.ID, "Auth.Refresh", 2)
		},
		func() (KnowledgeResult, error) {
			return client.Node(context.Background(), isolation, graph.ID, "Auth.Refresh", "auth.go", 10, false)
		},
		func() (KnowledgeResult, error) {
			return client.Files(context.Background(), isolation, graph.ID, "auth.go", "", "json", false, 2)
		},
	} {
		if _, err := call(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.DeleteCodeGraphs(context.Background(), isolation, []string{graph.ID}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/v3/wiki/create", "/v3/wiki/get", "/v3/wiki/raw/write", "/v3/wiki/raw/ls", "/v3/wiki/raw/read", "/v3/wiki/ingest", "/v3/wiki/search", "/v3/wiki/page/write", "/v3/wiki/page/ls", "/v3/wiki/page/read", "/v3/wiki/graph", "/v3/wiki/page/rm", "/v3/wiki/raw/rm", "/v3/wiki/delete", "/v3/code-graph/create", "/v3/code-graph/get", "/v3/code-graph/sync", "/v3/code-graph/status", "/v3/code-graph/search", "/v3/code-graph/explore", "/v3/code-graph/callers", "/v3/code-graph/callees", "/v3/code-graph/impact", "/v3/code-graph/node", "/v3/code-graph/files", "/v3/code-graph/delete"} {
		if !paths[want] {
			t.Fatalf("lifecycle route %q was not exercised", want)
		}
	}
}
