package tencent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

func TestMemoryLayerOperationsCoverV3IsolationAndEnvelope(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{"team_id": "team-a", "agent_id": "agent-a", "user_id": "user-a", "project_id": "prj-a-12345678"} {
			if body[key] != want {
				t.Fatalf("%s missing from %#v", key, body)
			}
		}
		paths = append(paths, request.URL.Path)
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "ok", "data": map[string]any{"ok": true}})
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk", ServiceID: "service", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a", SessionID: "session-a"}
	if _, err := client.ConversationQuery(context.Background(), isolation, map[string]any{"query": "JWT"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.AtomicUpdate(context.Background(), isolation, map[string]any{"id": "atomic-1", "content": "decision"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.WriteCore(context.Background(), isolation, map[string]any{"content": "summary"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/v3/conversation/query", "/v3/atomic/update", "/v3/core/write"} {
		if !containsString(paths, want) {
			t.Fatalf("missing memory layer path %s in %#v", want, paths)
		}
	}
}

func TestMemoryOperationRejectsUnknownPath(t *testing.T) {
	client := NewClient(Config{Endpoint: "http://127.0.0.1:1", UserKey: "sk", ServiceID: "service"})
	_, err := client.MemoryOperation(context.Background(), contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}, "/v3/shell", nil)
	if err == nil || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatalf("unknown memory endpoint was not rejected: %v", err)
	}
}

func TestEveryMemoryLayerRouteRoundTripsWithStrictIsolation(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode %s: %v", request.URL.Path, err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		for key, want := range map[string]string{"team_id": "team-a", "agent_id": "agent-a", "user_id": "user-a", "project_id": "prj-layers-12345678", "session_id": "session-a"} {
			if body[key] != want {
				t.Errorf("%s isolation %s=%v want %s", request.URL.Path, key, body[key], want)
			}
		}
		seen = append(seen, request.URL.Path)
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "ok", "data": map[string]any{"ok": true}})
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-layers-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a", SessionID: "session-a"}
	paths := make([]string, 0, len(allowedMemoryPaths))
	for path := range allowedMemoryPaths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if _, err := client.MemoryOperation(context.Background(), isolation, path, map[string]any{"content": "round-trip"}); err != nil {
			t.Fatalf("memory route %s failed: %v", path, err)
		}
	}
	if len(seen) != len(paths) {
		t.Fatalf("memory route count=%d want %d", len(seen), len(paths))
	}
}
