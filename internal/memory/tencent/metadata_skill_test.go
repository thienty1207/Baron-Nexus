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

func TestKnowledgeMetadataAndSkillOperationsUseAllowlistedIsolatedRoutes(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if request.Header.Get("x-tdai-service-id") != "baron-service" || request.Header.Get("Authorization") != "Bearer sk-secret" {
			t.Fatalf("missing Tencent headers")
		}
		if strings.Contains(string(mustJSONMetadata(body)), "sk-secret") {
			t.Fatal("secret leaked into request body")
		}
		for key, want := range map[string]string{"team_id": "team-a", "agent_id": "agent-a", "user_id": "user-a", "project_id": "prj-a-12345678"} {
			if body[key] != want {
				t.Fatalf("isolation field %s=%v, want %s", key, body[key], want)
			}
		}
		paths = append(paths, request.URL.Path)
		data := map[string]any{"knowledge_id": "wiki-1", "type": "wiki"}
		if strings.HasPrefix(request.URL.Path, "/v3/skill/") {
			data = map[string]any{"skill_id": "skill-1", "name": "review"}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "ok", "data": data})
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk-secret", ServiceID: "baron-service", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	if _, err := client.CreateKnowledgeMetadata(context.Background(), isolation, KnowledgeMetadata{KnowledgeID: "wiki-1", Type: "wiki", ServiceURL: server.URL + "/v3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateSkill(context.Background(), isolation, "review", "content"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListSkillResources(context.Background(), isolation, "skill-1"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/v3/knowledge/create", "/v3/skill/create", "/v3/skill/resource/ls"} {
		if !containsString(paths, want) {
			t.Fatalf("missing route %s in %#v", want, paths)
		}
	}
}

func TestMetadataAndSkillRejectUnknownRouteOrMissingIsolationBeforeNetwork(t *testing.T) {
	client := NewClient(Config{Endpoint: "http://127.0.0.1:1", UserKey: "sk-secret", ServiceID: "service"})
	_, err := client.MetadataOperation(context.Background(), contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}, "/v3/knowledge/unknown", nil)
	if err == nil || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatalf("unknown metadata route was not rejected: %v", err)
	}
	_, err = client.SkillOperation(context.Background(), contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a"}, "/v3/skill/list", nil)
	if err == nil || !strings.Contains(err.Error(), "agent_id is required") {
		t.Fatalf("missing skill isolation was not rejected: %v", err)
	}
}

func TestCapabilityDiscoveryDecodesVersionedFeatureEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/meta/capabilities" {
			t.Fatalf("unexpected capability path: %s", request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "ok", "data": map[string]any{"version": "v3", "features": map[string]bool{"knowledge": true, "skill": true}}})
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, ServiceID: "service", HTTPClient: server.Client()})
	capabilities, err := client.DiscoverCapabilities(context.Background())
	if err != nil || capabilities.Version != "v3" || !capabilities.Features["knowledge"] || !capabilities.Features["skill"] {
		t.Fatalf("capability discovery failed: %#v err=%v", capabilities, err)
	}
}

func TestUnsupportedCapabilityEndpointReturnsActionableVersionError(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, ServiceID: "baron", HTTPClient: server.Client()})
	_, err := client.DiscoverCapabilities(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("unsupported capability endpoint was not classified: %v", err)
	}
}

func TestMetaOperationUsesAllowlistedOwnershipRouteWithoutSecretLeak(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/meta/asset/list-accessible" {
			t.Fatalf("unexpected metadata route: %s", request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{
			"team_id": "team-a", "user_id": "user-a", "agent_id": "agent-a", "project_id": "prj-a-12345678",
		} {
			if body[key] != want {
				t.Fatalf("ownership field %s=%v, want %s", key, body[key], want)
			}
		}
		if strings.Contains(string(mustJSONMetadata(body)), "sk-secret") {
			t.Fatal("metadata operation leaked user key")
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "ok", "data": map[string]any{"items": []any{}}})
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk-secret", ServiceID: "baron-service", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	if _, err := client.ListAccessibleAssets(context.Background(), isolation, "private", 20); err != nil {
		t.Fatal(err)
	}
}

func mustJSONMetadata(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func TestSkillLifecyclePreservesVersionsResourcesAndTeamBoundary(t *testing.T) {
	paths := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body["team_id"] != "team-a" || body["agent_id"] != "agent-a" || body["user_id"] != "user-a" || body["project_id"] != "prj-skill-12345678" {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		paths[request.URL.Path] = true
		data := map[string]any{"ok": true}
		if request.URL.Path == "/v3/skill/create" || request.URL.Path == "/v3/skill/get" {
			data = map[string]any{"id": "skill-1", "skill_id": "skill-1", "name": "review", "version": "2"}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "ok", "data": data})
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-skill-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	if skill, err := client.CreateSkill(context.Background(), isolation, "review", "review content"); err != nil || skill.ID != "skill-1" {
		t.Fatalf("create skill failed: %#v %v", skill, err)
	}
	if _, err := client.GetSkill(context.Background(), isolation, "skill-1"); err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() (KnowledgeResult, error){
		func() (KnowledgeResult, error) { return client.ListSkills(context.Background(), isolation, 10) },
		func() (KnowledgeResult, error) {
			return client.SearchSkills(context.Background(), isolation, "review", 10)
		},
		func() (KnowledgeResult, error) {
			return client.UpdateSkill(context.Background(), isolation, "skill-1", map[string]any{"content": "updated"})
		},
		func() (KnowledgeResult, error) {
			return client.ListSkillVersions(context.Background(), isolation, "skill-1", 10)
		},
		func() (KnowledgeResult, error) {
			return client.GetSkillVersion(context.Background(), isolation, "skill-1", "2")
		},
		func() (KnowledgeResult, error) {
			return client.ListSkillResources(context.Background(), isolation, "skill-1")
		},
		func() (KnowledgeResult, error) {
			return client.ReadSkillResource(context.Background(), isolation, "skill-1", "rules.md")
		},
		func() (KnowledgeResult, error) {
			return client.WriteSkillResource(context.Background(), isolation, "skill-1", "rules.md", "rules")
		},
		func() (KnowledgeResult, error) {
			return client.RemoveSkillResource(context.Background(), isolation, "skill-1", "rules.md")
		},
		func() (KnowledgeResult, error) {
			return client.ExtractSkills(context.Background(), isolation, map[string]any{"content": "extract"})
		},
		func() (KnowledgeResult, error) {
			return client.DeleteSkills(context.Background(), isolation, []string{"skill-1"})
		},
	} {
		if _, err := call(); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"/v3/skill/create", "/v3/skill/get", "/v3/skill/list", "/v3/skill/search", "/v3/skill/update", "/v3/skill/version/list", "/v3/skill/version/get", "/v3/skill/resource/ls", "/v3/skill/resource/read", "/v3/skill/resource/write", "/v3/skill/resource/rm", "/v3/skill/extract", "/v3/skill/delete"} {
		if !paths[want] {
			t.Fatalf("Skill lifecycle route %q was not exercised", want)
		}
	}
}

func TestMetadataLifecycleCoversOwnershipAndAccessRelationships(t *testing.T) {
	paths := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body["team_id"] != "team-a" || body["agent_id"] != "agent-a" || body["user_id"] != "user-a" || body["project_id"] != "prj-meta-12345678" {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		paths[request.URL.Path] = true
		data := map[string]any{"knowledge_id": "knowledge-1", "id": "knowledge-1", "items": []any{}}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "ok", "data": data})
	}))
	defer server.Close()
	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-meta-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	metadata := KnowledgeMetadata{KnowledgeID: "knowledge-1", Type: "wiki", Name: "docs", ServiceURL: server.URL}
	if _, err := client.CreateKnowledgeMetadata(context.Background(), isolation, metadata); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetKnowledgeMetadata(context.Background(), isolation, "knowledge-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateKnowledgeMetadata(context.Background(), isolation, metadata); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListKnowledgeMetadata(context.Background(), isolation, "wiki", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeleteKnowledgeMetadata(context.Background(), isolation, []string{"knowledge-1"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v3/meta/task/create", "/v3/meta/asset/create", "/v3/meta/membership/create", "/v3/meta/access/list"} {
		if _, err := client.MetaOperation(context.Background(), isolation, path, map[string]any{"name": "baron"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.ListAccessibleAssets(context.Background(), isolation, "private", 10); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/v3/knowledge/create", "/v3/knowledge/get", "/v3/knowledge/update", "/v3/knowledge/list", "/v3/knowledge/delete", "/v3/meta/task/create", "/v3/meta/asset/create", "/v3/meta/membership/create", "/v3/meta/access/list", "/v3/meta/asset/list-accessible"} {
		if !paths[want] {
			t.Fatalf("metadata lifecycle route %q was not exercised", want)
		}
	}
}
