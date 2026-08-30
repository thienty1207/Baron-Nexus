package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/memory/tencent"
	"github.com/baron-shared-brain/baron/internal/storage"
)

func TestQueueHandlerNormalizesLegacyCoreSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/core/write" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["content"] != "legacy checkpoint summary" {
			t.Fatalf("core content=%v, want normalized summary", body["content"])
		}
		if _, exists := body["path"]; exists {
			t.Fatal("core payload unexpectedly included a scenario path")
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "request_id": "core-1", "data": map[string]any{}})
	}))
	defer server.Close()

	isolation := contracts.IsolationContext{ProjectID: "prj-queue-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	handler := NewQueueHandler(
		tencent.NewClient(tencent.Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()}),
		nil,
		nil,
		isolation,
		storage.KnowledgeRegistry{ProjectID: isolation.ProjectID},
	)
	requestID, err := handler.Handle(context.Background(), storage.QueueItem{
		ProjectID:      isolation.ProjectID,
		Operation:      storage.QueueOperationCoreUpdate,
		IdempotencyKey: "core-queue-1",
		Payload:        []byte(`{"summary":"legacy checkpoint summary","session_id":"sess-a"}`),
	})
	if err != nil || requestID != "core-1" {
		t.Fatalf("legacy core queue was not repaired: request_id=%q err=%v", requestID, err)
	}
}

func TestQueueHandlerNormalizesLegacyScenarioSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/scenario/write" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["path"] != "baron/sessions/sess-a.md" || body["content"] != "legacy checkpoint summary" {
			t.Fatalf("scenario body=%#v, want normalized path/content", body)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "request_id": "scenario-1", "data": map[string]any{}})
	}))
	defer server.Close()

	isolation := contracts.IsolationContext{ProjectID: "prj-queue-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	handler := NewQueueHandler(
		tencent.NewClient(tencent.Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()}),
		nil,
		nil,
		isolation,
		storage.KnowledgeRegistry{ProjectID: isolation.ProjectID},
	)
	requestID, err := handler.Handle(context.Background(), storage.QueueItem{
		ProjectID:      isolation.ProjectID,
		Operation:      storage.QueueOperationScenarioUpdate,
		IdempotencyKey: "scenario-queue-1",
		Payload:        []byte(`{"summary":"legacy checkpoint summary","session_id":"sess-a"}`),
	})
	if err != nil || requestID != "scenario-1" {
		t.Fatalf("legacy scenario queue was not repaired: request_id=%q err=%v", requestID, err)
	}
}

func TestQueueHandlerFallsBackToL0WhenScenarioFileIsNotSeeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch request.URL.Path {
		case "/v3/scenario/write":
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"code":404,"message":"Scenario file not found: baron/sessions/sess-a.md"}`))
		case "/v3/conversation/add":
			messages, ok := body["messages"].([]any)
			if !ok || len(messages) != 1 || !strings.Contains(string(mustQueueJSON(messages[0])), "legacy checkpoint summary") {
				t.Fatalf("L0 fallback did not preserve summary: %#v", body)
			}
			if body["session_id"] != "sess-a" {
				t.Fatalf("L0 fallback session=%v, want sess-a", body["session_id"])
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "request_id": "l0-fallback-1", "data": map[string]any{}})
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	isolation := contracts.IsolationContext{ProjectID: "prj-queue-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	handler := NewQueueHandler(
		tencent.NewClient(tencent.Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()}),
		nil,
		nil,
		isolation,
		storage.KnowledgeRegistry{ProjectID: isolation.ProjectID},
	)
	requestID, err := handler.Handle(context.Background(), storage.QueueItem{
		ProjectID:      isolation.ProjectID,
		Operation:      storage.QueueOperationScenarioUpdate,
		IdempotencyKey: "scenario-queue-fallback-1",
		Payload:        []byte(`{"summary":"legacy checkpoint summary","session_id":"sess-a"}`),
	})
	if err != nil || requestID != "l0-fallback-1" {
		t.Fatalf("missing scenario file did not fall back to L0: request_id=%q err=%v", requestID, err)
	}
}

func TestScenarioPathSanitizesSessionIdentity(t *testing.T) {
	path := normalizedScenarioPath(map[string]any{"session_id": "../../unsafe session"}, "fallback")
	if strings.Contains(path, "..") || strings.Contains(path, " ") || path != "baron/sessions/unsafe_session.md" {
		t.Fatalf("unsafe scenario path=%q", path)
	}
}

func TestQueueMetadataRepairUsesCodeGraphAssetWhenBothAssetsExist(t *testing.T) {
	paths := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["team_id"] != "team-a" || body["agent_id"] != "agent-a" || body["user_id"] != "user-a" || body["project_id"] != "prj-repair-12345678" {
			t.Fatalf("metadata repair lost isolation on %s: %#v", request.URL.Path, body)
		}
		paths[request.URL.Path] = true
		data := map[string]any{}
		switch request.URL.Path {
		case "/v3/knowledge/create":
			if body["type"] != "code-graph" || body["code_graph_id"] != "graph-1" || body["wiki_id"] != nil {
				t.Fatalf("metadata repair targeted the wrong asset: %#v", body)
			}
			data = map[string]any{"knowledge_id": "graph-1", "id": "graph-1"}
		case "/v3/meta/asset/list":
			data = map[string]any{"items": []any{}, "total": 0}
		case "/v3/meta/asset/create":
			if body["asset_id"] != "graph-1" || body["asset_type"] != "code_graph" {
				t.Fatalf("unexpected CodeGraph asset registration: %#v", body)
			}
		case "/v3/meta/agent-fixed-asset/list":
			data = map[string]any{"items": []any{}, "total": 0}
		case "/v3/meta/agent-fixed-asset/set":
			bindings, ok := body["bindings"].([]any)
			if !ok || len(bindings) != 1 {
				t.Fatalf("unexpected CodeGraph binding: %#v", body["bindings"])
			}
		default:
			t.Fatalf("unexpected metadata repair path: %s", request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "ok", "data": data})
	}))
	defer server.Close()

	isolation := contracts.IsolationContext{ProjectID: "prj-repair-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	handler := NewQueueHandler(
		tencent.NewClient(tencent.Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()}),
		nil,
		nil,
		isolation,
		storage.KnowledgeRegistry{
			ProjectID: isolation.ProjectID, WikiID: "wiki-1", CodeGraphID: "graph-1",
			ServiceURL: server.URL + "/v3", Repository: "https://example.com/project.git",
			Branch: "main", CodeGraphCommit: "abc123",
		},
	)
	requestID, err := handler.Handle(context.Background(), storage.QueueItem{
		ProjectID: isolation.ProjectID, Operation: storage.QueueOperationMetadataRepair,
		IdempotencyKey: "metadata-repair-codegraph-1",
		Payload:        []byte(`{"project_id":"prj-repair-12345678","team_id":"team-a","user_id":"user-a","agent_id":"agent-a","wiki_id":"wiki-1","code_graph_id":"graph-1","action":"codegraph_metadata"}`),
	})
	if err != nil || requestID != "graph-1" {
		t.Fatalf("CodeGraph metadata repair failed: request_id=%q err=%v", requestID, err)
	}
	for _, path := range []string{"/v3/knowledge/create", "/v3/meta/asset/list", "/v3/meta/asset/create", "/v3/meta/agent-fixed-asset/list", "/v3/meta/agent-fixed-asset/set"} {
		if !paths[path] {
			t.Fatalf("metadata repair did not exercise %s: %#v", path, paths)
		}
	}
}

func mustQueueJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
