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

func TestRetrieverLoadsWikiAndCodeGraphStatusAtSessionStart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v3/wiki/get":
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"id": "wiki-1", "status": "ready", "version": "wiki-v2"}})
		case "/v3/code-graph/status":
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": map[string]any{"status": "ready", "commit_hash": "abc123"}})
		default:
			t.Fatalf("unexpected session-start knowledge route: %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := tencent.NewKnowledgeClient(tencent.Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()})
	retriever := NewRetriever(client, storage.KnowledgeRegistry{WikiID: "wiki-1", CodeGraphID: "graph-1"})
	isolation := contracts.IsolationContext{ProjectID: "prj-status-12345678", TeamID: "team", AgentID: "agent", UserID: "user"}
	citations, err := retriever.Retrieve(context.Background(), isolation, contracts.MemoryQuery{Kinds: []string{"session_start"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	for _, citation := range citations {
		text += citation.Source + " " + citation.Content + " " + citation.Freshness + "\n"
	}
	if !strings.Contains(text, "wiki-status") || !strings.Contains(text, "CodeGraph status=ready") || !strings.Contains(text, "abc123") {
		t.Fatalf("session-start status citations missing: %#v", citations)
	}
}

func TestRetrieverUsesRelevantCodeGraphSlicesForSymbolTasks(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		data := map[string]any{"items": []any{map[string]any{"path": request.URL.Path, "content": "relevant"}}}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	client := tencent.NewKnowledgeClient(tencent.Config{KnowledgeEndpoint: server.URL + "/v3", UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()})
	retriever := NewRetriever(client, storage.KnowledgeRegistry{WikiID: "wiki-1", CodeGraphID: "graph-1"})
	isolation := contracts.IsolationContext{ProjectID: "prj-symbol-12345678", TeamID: "team", AgentID: "agent", UserID: "user"}
	_, err := retriever.Retrieve(context.Background(), isolation, contracts.MemoryQuery{
		Text: "refresh token", Limit: 10, Kinds: []string{"symbol_change"}, Symbols: []string{"Auth.Refresh"}, Files: []string{"auth.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(paths, ",")
	for _, want := range []string{"/v3/wiki/search", "/v3/code-graph/callers", "/v3/code-graph/callees", "/v3/code-graph/impact"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("relevant route %q missing: %s", want, joined)
		}
	}
	for _, forbidden := range []string{"/v3/code-graph/search", "/v3/code-graph/files", "/v3/wiki/graph"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("irrelevant broad route %q was called: %s", forbidden, joined)
		}
	}
}
