package tencent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

func TestCaptureAndSearchUseStrictProjectIsolation(t *testing.T) {
	var seen []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Fatalf("method=%s", request.Method)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		seen = append(seen, payload)
		if request.Header.Get("x-tdai-user-key") != "sk-mem-secret" || request.Header.Get("Authorization") != "Bearer sk-mem-secret" {
			t.Fatalf("strict auth headers missing")
		}
		if request.URL.Path == "/v3/conversation/add" {
			if strings.Contains(string(mustJSON(payload)), "sk-mem-secret") {
				t.Fatal("Tencent capture payload exposed user key")
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"request_id": "req-1"})
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"items": []map[string]any{{
			"id": "mem-1", "content": "JWT decision", "content_hash": "hash-1", "kind": "decision",
		}}})
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk-mem-secret", ServiceID: "default", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agt-a", UserID: "usr-a", ServiceID: "default", SessionID: "session-a"}
	record := contracts.MemoryRecord{ProjectID: isolation.ProjectID, SourceClient: contracts.ClientCodex, Kind: "decision", Content: "JWT decision sk-mem-secret"}
	record.Normalize()
	if _, err := client.Capture(context.Background(), isolation, record, "idem-1"); err != nil {
		t.Fatal(err)
	}
	results, err := client.Search(context.Background(), isolation, contracts.MemoryQuery{Text: "JWT", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Content != "JWT decision" {
		t.Fatalf("unexpected search results: %#v", results)
	}
	if len(seen) != 2 {
		t.Fatalf("expected two calls, got %d", len(seen))
	}
	for _, payload := range seen {
		for key, want := range map[string]string{"team_id": "team-a", "agent_id": "agt-a", "user_id": "usr-a"} {
			if got, _ := payload[key].(string); got != want {
				t.Fatalf("isolation %s=%q want %q in %#v", key, got, want, payload)
			}
		}
		if got, _ := payload["session_id"].(string); got != "session-a" {
			t.Fatalf("search session isolation=%q", got)
		}
	}
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func TestEnsureProjectAgentDoesNotBindAmbiguousDisplayName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/meta/agent/list" {
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": "agt-new", "agent_id": "agt-new", "name": "Project-A [prj-b-12]", "description": "project_id=prj-b-12345678"})
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"items": []map[string]any{
			{"agent_id": "agt-old", "name": "Project-A", "description": "project_id=prj-other-12345678"},
		}})
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "user-key", ServiceID: "default", HTTPClient: server.Client()})
	binding, err := client.EnsureProjectAgent(context.Background(), contracts.IsolationContext{ProjectID: "prj-b-12345678", TeamID: "team", UserID: "user"}, "Project-A")
	if err != nil {
		t.Fatal(err)
	}
	if binding.AgentID != "agt-new" || !strings.Contains(binding.AgentName, "prj-b") {
		t.Fatalf("ambiguous agent was reused: %#v", binding)
	}
}

func TestHealthReturnsActionableErrorOnTencentOutage(t *testing.T) {
	client := NewClient(Config{Endpoint: "http://127.0.0.1:1", UserKey: "secret", ServiceID: "default"})
	if err := client.Health(context.Background()); err == nil {
		t.Fatal("expected Tencent outage error")
	}
}

func TestZeroLikeBooleanResponseCodeIsSuccessful(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"code":false}`))
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "default", HTTPClient: server.Client()})
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("false response code was treated as failure: %v", err)
	}
}

func TestLayeredReadsUseCurrentV3EndpointsAndIsolation(t *testing.T) {
	paths := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Fatalf("method=%s", request.Method)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["team_id"] != "team-a" || payload["agent_id"] != "agent-a" || payload["user_id"] != "user-a" || payload["project_id"] != "prj-a-12345678" {
			t.Fatalf("layer omitted strict isolation: %#v", payload)
		}
		paths[request.URL.Path] = true
		_ = json.NewEncoder(writer).Encode(map[string]any{"items": []map[string]any{{"content": request.URL.Path}}})
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "default", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a", SessionID: "session-a"}
	query := contracts.MemoryQuery{Text: "history", Limit: 3}
	if _, err := client.ReadCore(context.Background(), isolation, query); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadScenario(context.Background(), isolation, query); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SearchConversations(context.Background(), isolation, query); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v3/core/read", "/v3/scenario/read", "/v3/conversation/search"} {
		if !paths[path] {
			t.Fatalf("expected layered endpoint %s, got %#v", path, paths)
		}
	}
}

func TestEnsureIdentityUsesUsernameAndDefaultUserKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		switch request.URL.Path {
		case "/v3/meta/user/list":
			if payload["username"] != "baron" {
				t.Fatalf("user list did not use username: %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"items":[]}`))
		case "/v3/meta/user/create":
			if payload["username"] != "baron" {
				t.Fatalf("user create did not use username: %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"data":{"user_id":"user-baron","default_user_key":"sk-user-key"}}`))
		case "/v3/meta/team/list":
			if payload["name"] != "baron-projects" || payload["user_id"] != "user-baron" {
				t.Fatalf("team list payload was not owner-scoped: %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"items":[{"id":"team-baron","name":"baron-projects"}]}`))
		default:
			t.Fatalf("unexpected metadata endpoint %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, AdminKey: "admin-key", ServiceID: "default", HTTPClient: server.Client()})
	identity, err := client.EnsureIdentity(context.Background(), contracts.IdentitySpec{UserName: "baron", TeamName: "baron-projects"})
	if err != nil {
		t.Fatal(err)
	}
	if identity.UserID != "user-baron" || identity.UserKey != "sk-user-key" || identity.TeamID != "team-baron" {
		t.Fatalf("unexpected identity: %#v", identity)
	}
}
