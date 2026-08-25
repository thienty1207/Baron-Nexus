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

func TestVerifyAuthSendsUserKeyInTencentV3Body(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/meta/auth/verify" {
			t.Fatalf("unexpected auth endpoint: %s", request.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["user_key"] != "sk-user-secret" {
			t.Fatalf("auth verify did not send user_key: %#v", payload)
		}
		_, _ = writer.Write([]byte(`{"valid":true}`))
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk-user-secret", ServiceID: "default", HTTPClient: server.Client()})
	if err := client.VerifyAuth(context.Background()); err != nil {
		t.Fatal(err)
	}
}

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

func TestCaptureBoundsConversationContentToTencentLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/conversation/add" {
			t.Fatalf("unexpected endpoint: %s", request.URL.Path)
		}
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Messages) != 1 {
			t.Fatalf("expected one message, got %#v", payload.Messages)
		}
		if len(payload.Messages[0].Content) > 8192 {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"message":"messages.0.content: Too big"}`))
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"request_id": "req-bounded"})
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "default", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-bounded-12345678", TeamID: "team-bounded", AgentID: "agent-bounded", UserID: "user-bounded"}
	record := contracts.MemoryRecord{ProjectID: isolation.ProjectID, SourceClient: contracts.ClientDSH, Kind: "tool_finished", Content: strings.Repeat("x", 9000)}
	if _, err := client.Capture(context.Background(), isolation, record, "bounded-content-1"); err != nil {
		t.Fatalf("oversized conversation content was not bounded before Tencent capture: %v", err)
	}
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func TestEnsureProjectAgentDoesNotBindAmbiguousDisplayName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/meta/agent/list" {
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["owner_user_id"] != "user" {
				t.Fatalf("agent create did not include owner_user_id: %#v", payload)
			}
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

func TestFindProjectAgentVerifiesExistingBindingWithoutCreating(t *testing.T) {
	createCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v3/meta/agent/create" {
			createCalled = true
			t.Fatalf("find-only project lookup attempted to create an agent")
		}
		if request.URL.Path != "/v3/meta/agent/list" {
			t.Fatalf("unexpected endpoint: %s", request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": []map[string]any{{
			"id": "agent-existing", "name": "Project A", "description": "Baron project_id=prj-a-12345678", "project_id": "prj-a-12345678",
		}}})
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, AdminKey: "admin-secret", ServiceID: "baron", HTTPClient: server.Client()})
	binding, err := client.FindProjectAgent(context.Background(), contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", UserID: "user-a"})
	if err != nil {
		t.Fatal(err)
	}
	if binding.AgentID != "agent-existing" || binding.ProjectID != "prj-a-12345678" || createCalled {
		t.Fatalf("unexpected find-only binding: %#v createCalled=%v", binding, createCalled)
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
		if request.URL.Path == "/v3/scenario/read" && payload["path"] != "baron/session.md" {
			t.Fatalf("scenario read omitted explicit path: %#v", payload)
		}
		paths[request.URL.Path] = true
		_ = json.NewEncoder(writer).Encode(map[string]any{"items": []map[string]any{{"content": request.URL.Path}}})
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "default", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a", SessionID: "session-a"}
	query := contracts.MemoryQuery{Text: "history", Limit: 3, ScenarioPath: "baron/session.md"}
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

func TestReadScenarioWithoutExplicitPathSkipsPathAddressedEndpoint(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		writer.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "default", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	records, err := client.ReadScenario(context.Background(), isolation, contracts.MemoryQuery{Text: "generic recall"})
	if err != nil {
		t.Fatalf("scenario read without a path failed: %v", err)
	}
	if called || len(records) != 0 {
		t.Fatalf("path-addressed scenario endpoint was called without a path: called=%v records=%v", called, records)
	}
}

func TestSearchConversationsDecodesTencentMessagesEnvelope(t *testing.T) {
	records := decodeRecords(json.RawMessage(`{"code":0,"data":{"messages":[{"id":"msg-1","role":"assistant","content":"handoff sentinel","timestamp":"2026-08-24T10:40:00Z"}]}}`), "prj-a-12345678")
	if len(records) != 1 || records[0].ID != "msg-1" || records[0].Content != "handoff sentinel" {
		t.Fatalf("Tencent messages envelope was not decoded: %#v", records)
	}
	if records[0].ProjectID != "prj-a-12345678" || !records[0].HistoricalOnly {
		t.Fatalf("decoded remote conversation lost project/trust metadata: %#v", records[0])
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

func TestIdentityProvisionRollbackDeletesOnlyNewOpaqueMetadata(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		body := string(mustJSON(payload))
		if strings.Contains(body, "sk-new-user-key") {
			t.Fatalf("identity rollback payload exposed the user key: %s", body)
		}
		switch request.URL.Path {
		case "/v3/meta/user/list":
			_, _ = writer.Write([]byte(`{"items":[]}`))
		case "/v3/meta/user/create":
			_, _ = writer.Write([]byte(`{"data":{"id":"user-baron"}}`))
		case "/v3/meta/user-key/list":
			_, _ = writer.Write([]byte(`{"items":[]}`))
		case "/v3/meta/user-key/create":
			_, _ = writer.Write([]byte(`{"data":{"key_id":"key-baron","key_value":"sk-new-user-key"}}`))
		case "/v3/meta/team/list":
			_, _ = writer.Write([]byte(`{"items":[]}`))
		case "/v3/meta/team/create":
			if request.Header.Get("x-tdai-user-key") != "sk-new-user-key" {
				t.Fatalf("team create did not use the owner user key: %q", request.Header.Get("x-tdai-user-key"))
			}
			if payload["owner_user_id"] != "user-baron" {
				t.Fatalf("team create did not use owner_user_id: %#v", payload)
			}
			if _, ok := payload["user_id"]; ok {
				t.Fatalf("team create used the list-only user_id field: %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"data":{"id":"team-baron","name":"baron-projects"}}`))
		case "/v3/meta/team/delete":
			if request.Header.Get("x-tdai-user-key") != "sk-new-user-key" {
				t.Fatalf("team rollback did not use the owner user key: %q", request.Header.Get("x-tdai-user-key"))
			}
			assertStringArrayPayload(t, payload, "team_ids", "team-baron")
			_, _ = writer.Write([]byte(`{"code":0}`))
		case "/v3/meta/user-key/revoke":
			if payload["key_id"] != "key-baron" {
				t.Fatalf("user-key revoke payload was not key-scoped: %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"code":0}`))
		case "/v3/meta/user/delete":
			assertStringArrayPayload(t, payload, "user_ids", "user-baron")
			_, _ = writer.Write([]byte(`{"code":0}`))
		default:
			t.Fatalf("unexpected metadata endpoint %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, AdminKey: "sk-admin", ServiceID: "default", HTTPClient: server.Client()})
	identity, provision, err := client.EnsureIdentityWithRollback(context.Background(), contracts.IdentitySpec{UserName: "baron", TeamName: "baron-projects"})
	if err != nil || identity.UserID != "user-baron" || identity.TeamID != "team-baron" {
		t.Fatalf("identity provisioning failed: %#v err=%v", identity, err)
	}
	if err := provision.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"/v3/meta/user/list", "/v3/meta/user/create", "/v3/meta/user-key/list", "/v3/meta/user-key/create", "/v3/meta/team/list", "/v3/meta/team/create", "/v3/meta/team/delete", "/v3/meta/user-key/revoke", "/v3/meta/user/delete"}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rollback did not use reverse created-resource order: got=%#v want=%#v", paths, want)
	}
	if err := provision.Rollback(context.Background()); err != nil {
		t.Fatalf("rollback was not idempotent: %v", err)
	}
}

func TestIdentityProvisionFailureCompensatesCreatedMetadata(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		switch request.URL.Path {
		case "/v3/meta/user/list":
			_, _ = writer.Write([]byte(`{"items":[]}`))
		case "/v3/meta/user/create":
			_, _ = writer.Write([]byte(`{"data":{"id":"user-baron"}}`))
		case "/v3/meta/user-key/list":
			_, _ = writer.Write([]byte(`{"items":[]}`))
		case "/v3/meta/user-key/create":
			_, _ = writer.Write([]byte(`{"data":{"key_id":"key-baron","key_value":"sk-new-user-key"}}`))
		case "/v3/meta/team/list":
			_, _ = writer.Write([]byte(`{"items":[]}`))
		case "/v3/meta/team/create":
			if request.Header.Get("x-tdai-user-key") != "sk-new-user-key" {
				t.Fatalf("team create did not use the owner user key: %q", request.Header.Get("x-tdai-user-key"))
			}
			if payload["owner_user_id"] != "user-baron" {
				t.Fatalf("team create did not use owner_user_id: %#v", payload)
			}
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = writer.Write([]byte(`{"error":"team unavailable"}`))
		case "/v3/meta/user-key/revoke":
			if request.Header.Get("x-tdai-user-key") != "sk-admin" {
				t.Fatalf("user-key rollback did not use the admin key: %q", request.Header.Get("x-tdai-user-key"))
			}
			if payload["key_id"] != "key-baron" {
				t.Fatalf("user-key revoke payload was not key-scoped: %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"code":0}`))
		case "/v3/meta/user/delete":
			if request.Header.Get("x-tdai-user-key") != "sk-admin" {
				t.Fatalf("user rollback did not use the admin key: %q", request.Header.Get("x-tdai-user-key"))
			}
			assertStringArrayPayload(t, payload, "user_ids", "user-baron")
			_, _ = writer.Write([]byte(`{"code":0}`))
		default:
			t.Fatalf("unexpected endpoint during compensation: %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, AdminKey: "sk-admin", ServiceID: "default", HTTPClient: server.Client()})
	if _, _, err := client.EnsureIdentityWithRollback(context.Background(), contracts.IdentitySpec{}); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("team failure was not returned: %v", err)
	}
	if strings.Join(paths, "\n") != strings.Join([]string{"/v3/meta/user/list", "/v3/meta/user/create", "/v3/meta/user-key/list", "/v3/meta/user-key/create", "/v3/meta/team/list", "/v3/meta/team/create", "/v3/meta/user-key/revoke", "/v3/meta/user/delete"}, "\n") {
		t.Fatalf("automatic compensation did not clean created metadata: %#v", paths)
	}
}

func TestEnsureIdentityCreatesFreshKeyWhenExistingKeyValueIsUnavailable(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		switch request.URL.Path {
		case "/v3/meta/user/list":
			_, _ = writer.Write([]byte(`{"items":[{"user_id":"user-baron","username":"baron"}]}`))
		case "/v3/meta/user-key/list":
			_, _ = writer.Write([]byte(`{"items":[{"key_id":"key-old","user_id":"user-baron","key_prefix":"sk-old****"}]}`))
		case "/v3/meta/user-key/create":
			if payload["user_id"] != "user-baron" {
				t.Fatalf("fresh key was not created for the existing user: %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"data":{"key_id":"key-fresh","user_id":"user-baron","key_value":"sk-fresh-user-key"}}`))
		case "/v3/meta/team/list":
			if request.Header.Get("x-tdai-user-key") != "sk-fresh-user-key" {
				t.Fatalf("team list used a key id or admin key instead of the fresh secret: %q", request.Header.Get("x-tdai-user-key"))
			}
			_, _ = writer.Write([]byte(`{"items":[]}`))
		case "/v3/meta/team/create":
			if request.Header.Get("x-tdai-user-key") != "sk-fresh-user-key" {
				t.Fatalf("team create used a key id or admin key instead of the fresh secret: %q", request.Header.Get("x-tdai-user-key"))
			}
			if payload["owner_user_id"] != "user-baron" {
				t.Fatalf("team owner did not match the existing user: %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"data":{"team_id":"team-baron","name":"baron-projects"}}`))
		default:
			t.Fatalf("unexpected metadata endpoint %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, AdminKey: "sk-admin", ServiceID: "default", HTTPClient: server.Client()})
	identity, err := client.EnsureIdentity(context.Background(), contracts.IdentitySpec{UserName: "baron", TeamName: "baron-projects"})
	if err != nil {
		t.Fatal(err)
	}
	if identity.UserID != "user-baron" || identity.UserKey != "sk-fresh-user-key" || identity.TeamID != "team-baron" {
		t.Fatalf("unexpected identity after fresh key recovery: %#v", identity)
	}
	if strings.Contains(strings.Join(paths, "\n"), "user-key/revoke") {
		t.Fatalf("existing key was unexpectedly revoked during recovery: %#v", paths)
	}
}

func assertStringArrayPayload(t *testing.T, payload map[string]any, key, want string) {
	t.Helper()
	values, ok := payload[key].([]any)
	if !ok || len(values) != 1 || values[0] != want {
		t.Fatalf("%s payload was not [%q]: %#v", key, want, payload)
	}
}
