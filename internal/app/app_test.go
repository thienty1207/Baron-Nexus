package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/continuity"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/storage"
)

func TestApplicationSetupAndHookRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project A")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	application.ProjectProvisioner = func(context.Context, string, string) (contracts.ProjectBinding, error) {
		return contracts.ProjectBinding{TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}, nil
	}
	if _, err := application.SetupProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".baron", "runtime", "state.db")); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	input := bytes.NewBufferString(`{"session_id":"ses-1","idempotency_key":"app-event-1","payload":{"command":"go test ./...","exit_code":1,"summary":"failed"}}`)
	if err := application.HandleHook(context.Background(), "codex", "tool_finished", root, input, &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["continue"] != true {
		t.Fatalf("unexpected hook response: %s", output.String())
	}
	state, err := config.ReadEnvFile(filepath.Join(root, ".baron", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if state["BARON_TENCENT_AGENT_ID"] != "agent-a" {
		t.Fatalf("binding was not written: %#v", state)
	}
}

func TestCodexHookWrapsRecoveryContextInHookSpecificOutput(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project B")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	application.ProjectProvisioner = func(context.Context, string, string) (contracts.ProjectBinding, error) {
		return contracts.ProjectBinding{TeamID: "team-b", AgentID: "agent-b", UserID: "user-b"}, nil
	}
	project, err := application.SetupProject(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(root, ".baron", "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	engine := continuity.NewEngine(store, project.ProjectID, project.Metadata.Name, filepath.Join(root, ".baron", "checkpoint.json"))
	if err := engine.Save(context.Background(), continuity.WorkState{ProjectID: project.ProjectID, ProjectName: project.Metadata.Name, LastClient: contracts.ClientDSH, SessionID: "dsh-old", SessionState: contracts.SessionActive, Task: continuity.TaskState{Goal: "finish", CurrentStep: "test", NextAction: "rerun test"}}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	store.Close()
	var output bytes.Buffer
	if err := application.HandleHook(context.Background(), "codex", "SessionStart", root, bytes.NewBufferString(`{}`), &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if _, ok := response["hookSpecificOutput"]; !ok {
		t.Fatalf("Codex-specific hook output missing: %s", output.String())
	}
}

func TestHookWiresConfiguredTencentBackendWithoutBlockingLocalState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project Remote")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var captureBodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v3/conversation/add" {
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode capture: %v", err)
			}
			mu.Lock()
			captureBodies = append(captureBodies, body)
			mu.Unlock()
			_, _ = writer.Write([]byte(`{"request_id":"remote-1"}`))
			return
		}
		if request.URL.Path == "/v3/atomic/search" {
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode search: %v", err)
			}
			if value, ok := body["project_id"].(string); !ok || !strings.HasPrefix(value, "prj-") {
				t.Fatalf("search omitted project isolation: %#v", body)
			}
			_, _ = writer.Write([]byte(`{"items":[{"project_id":"","source_client":"dsh","kind":"sentinel","content":"remote sentinel"}]}`))
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()

	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	if err := config.SaveGlobalState(application.GlobalPath, config.GlobalState{Identity: contracts.Identity{
		Endpoint: server.URL, UserID: "user-a", UserKey: "sk-test-key", TeamID: "team-a", ServiceID: "baron",
	}}); err != nil {
		t.Fatal(err)
	}
	application.ProjectProvisioner = func(context.Context, string, string) (contracts.ProjectBinding, error) {
		return contracts.ProjectBinding{TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}, nil
	}
	if _, err := application.SetupProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := application.HandleHook(context.Background(), "dsh", "user_prompt", root, bytes.NewBufferString(`{"prompt":"continue"}`), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "remote sentinel") {
		t.Fatalf("remote context was not returned: %s", output.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(captureBodies) != 1 {
		t.Fatalf("expected one remote capture, got %d", len(captureBodies))
	}
	if value, ok := captureBodies[0]["project_id"].(string); !ok || !strings.HasPrefix(value, "prj-") {
		t.Fatalf("unexpected capture project_id: %#v", captureBodies[0])
	}
	if captureBodies[0]["team_id"] != "team-a" || captureBodies[0]["agent_id"] != "agent-a" || captureBodies[0]["user_id"] != "user-a" {
		t.Fatalf("capture isolation was not strict: %#v", captureBodies[0])
	}
}

func TestTamperedProjectEnvIsRejectedBeforeRemoteHookUse(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project Tampered")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var requests int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		_, _ = writer.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	if err := config.SaveGlobalState(application.GlobalPath, config.GlobalState{Identity: contracts.Identity{Endpoint: server.URL, UserID: "user", UserKey: "sk-key", TeamID: "team"}}); err != nil {
		t.Fatal(err)
	}
	application.ProjectProvisioner = func(context.Context, string, string) (contracts.ProjectBinding, error) {
		return contracts.ProjectBinding{TeamID: "team", AgentID: "agent-a", UserID: "user"}, nil
	}
	if _, err := application.SetupProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	requests = 0
	mu.Unlock()
	envPath := filepath.Join(root, ".baron", ".env")
	env, err := config.ReadEnvFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	env["BARON_TENCENT_AGENT_ID"] = "agent-b"
	if err := config.WriteEnv(envPath, env); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := application.HandleHook(context.Background(), "codex", "SessionStart", root, bytes.NewBufferString(`{}`), &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["continue"] != true || !strings.Contains(output.String(), "integrity") {
		t.Fatalf("tampered hook was not blocked: %#v", response)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Fatalf("remote service was contacted after integrity failure: %d", requests)
	}
}

func TestMalformedCodexHookFailsOpenWithOfficialContinueResponse(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project Malformed")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	application.ProjectProvisioner = func(context.Context, string, string) (contracts.ProjectBinding, error) {
		return contracts.ProjectBinding{TeamID: "team", AgentID: "agent", UserID: "user"}, nil
	}
	if _, err := application.SetupProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := application.HandleHook(context.Background(), "codex", "UserPromptSubmit", root, bytes.NewBufferString("{"), &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["continue"] != true {
		t.Fatalf("malformed Codex hook was not fail-open: %s", output.String())
	}
}
