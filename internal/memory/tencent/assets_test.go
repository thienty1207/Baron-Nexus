package tencent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

func TestEnsureKnowledgeAssetAndAgentBindingAreIdempotent(t *testing.T) {
	assetCreated := false
	bindingSetCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["team_id"] != "team-a" || body["user_id"] != "user-a" || body["agent_id"] != "agent-a" || body["project_id"] != "prj-a-12345678" {
			t.Fatalf("isolation fields missing from %s: %#v", request.URL.Path, body)
		}

		switch request.URL.Path {
		case "/v3/meta/asset/list":
			items := []any{}
			if assetCreated {
				items = append(items, map[string]any{
					"asset_id": "wiki-1", "team_id": "team-a", "asset_type": "llm_wiki",
					"name": "Project wiki", "owner_user_id": "user-a", "visibility": "team",
				})
			}
			writeMetadataEnvelope(writer, map[string]any{"items": items, "total": len(items)})
		case "/v3/meta/asset/create":
			if body["asset_id"] != "wiki-1" || body["asset_type"] != "llm_wiki" || body["owner_user_id"] != "user-a" || body["source_type"] != "manual" {
				t.Fatalf("unexpected asset registration: %#v", body)
			}
			assetCreated = true
			writeMetadataEnvelope(writer, map[string]any{"asset_id": "wiki-1"})
		case "/v3/meta/agent-fixed-asset/list":
			items := []any{
				map[string]any{
					"asset_id": "chat-memory-1", "asset_type": "chat_memory",
					"injection_mode": "summary", "priority": 50, "created_by": "user-a",
				},
			}
			if bindingSetCount > 0 {
				items = append(items, map[string]any{
					"asset_id": "wiki-1", "asset_type": "llm_wiki",
					"injection_mode": "tool", "priority": 50, "created_by": "user-a",
				})
			}
			writeMetadataEnvelope(writer, map[string]any{"items": items, "total": len(items)})
		case "/v3/meta/agent-fixed-asset/set":
			bindings, ok := body["bindings"].([]any)
			if !ok || len(bindings) != 2 {
				t.Fatalf("existing binding was not preserved: %#v", body["bindings"])
			}
			bindingSetCount++
			writeMetadataEnvelope(writer, map[string]any{"ok": true})
		default:
			t.Fatalf("unexpected Tencent metadata route: %s", request.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(Config{Endpoint: server.URL, UserKey: "sk-user", ServiceID: "baron", HTTPClient: server.Client()})
	isolation := contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}
	registration := KnowledgeAssetRegistration{
		AssetID:     "wiki-1",
		AssetType:   "llm_wiki",
		Name:        "Project wiki",
		OwnerUserID: "user-a",
		SourceType:  "manual",
		Visibility:  "team",
		ContentRef:  server.URL + "/v3",
	}

	if err := client.EnsureKnowledgeAsset(context.Background(), isolation, registration); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureAgentFixedAsset(context.Background(), isolation, "wiki-1", "llm_wiki"); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureKnowledgeAsset(context.Background(), isolation, registration); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureAgentFixedAsset(context.Background(), isolation, "wiki-1", "llm_wiki"); err != nil {
		t.Fatal(err)
	}
	if bindingSetCount != 1 {
		t.Fatalf("idempotent binding wrote %d times, want 1", bindingSetCount)
	}
}

func writeMetadataEnvelope(writer http.ResponseWriter, data any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "ok", "data": data})
}
