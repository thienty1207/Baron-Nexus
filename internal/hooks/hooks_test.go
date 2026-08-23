package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/continuity"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/storage"
)

func TestHookPersistsCanonicalEventAndReturnsBoundedJSON(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-hook-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "hook"}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "hook", filepath.Join(root, "checkpoint.json")), projectID)
	response, err := runtime.Handle(context.Background(), Request{
		Client: contracts.ClientCodex, Event: contracts.EventToolFinished,
		ProjectID: projectID, SessionID: "ses-1", IdempotencyKey: "hook-event-1",
		Payload: json.RawMessage(`{"command":"go test ./...","exit_code":1,"summary":"failed"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || !response.Persisted {
		t.Fatalf("unexpected hook response: %#v", response)
	}
	count, err := store.CountEvents(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one persisted event, got %d", count)
	}
	state, err := runtime.Engine().Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.LatestTest.Status != "failed" || state.LatestTest.Command != "go test ./..." {
		t.Fatalf("test evidence not captured: %#v", state.LatestTest)
	}
}

func TestDuplicateHookDeliveryDoesNotMutateTwice(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-hook-dup-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "hook"}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "hook", filepath.Join(root, "checkpoint.json")), projectID)
	req := Request{Client: contracts.ClientDSH, Event: contracts.EventFileChanged, ProjectID: projectID, SessionID: "ses", IdempotencyKey: "same", Payload: json.RawMessage(`{"file":"a.go"}`)}
	for i := 0; i < 100; i++ {
		response, handleErr := runtime.Handle(context.Background(), req)
		if handleErr != nil || !response.OK {
			t.Fatalf("duplicate %d failed: %#v %v", i, response, handleErr)
		}
	}
	count, err := store.CountEvents(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate event count=%d", count)
	}
}

func TestMalformedHookPayloadFailsOpenWithJSONDiagnostic(t *testing.T) {
	var out bytes.Buffer
	err := ServeJSON(context.Background(), NewRuntime(nil, nil, ""), bytes.NewBufferString("not json"), &out)
	if err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.OK {
		t.Fatal("malformed hook was reported as successful")
	}
	if response.Error == "" {
		t.Fatal("malformed hook diagnostic missing")
	}
}

func TestNewAgentSessionReceivesEvidenceBackedRecoveryContext(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-handoff-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "handoff"}); err != nil {
		t.Fatal(err)
	}
	engine := continuity.NewEngine(store, projectID, "handoff", filepath.Join(root, "checkpoint.json"))
	if err := engine.Save(context.Background(), continuity.WorkState{
		ProjectID: projectID, ProjectName: "handoff", LastClient: contracts.ClientCodex, SessionID: "old-session", SessionState: contracts.SessionActive,
		Task:       continuity.TaskState{Goal: "Finish feature", Status: contracts.TaskInProgress, LastSuccessfulStep: "code written", CurrentStep: "run tests", NextAction: "rerun failing test"},
		LatestTest: continuity.TestEvidence{Command: "go test ./...", Status: "failed", Summary: "one test failed"},
	}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(store, engine, projectID)
	response, err := runtime.Handle(context.Background(), Request{Client: contracts.ClientDSH, Event: contracts.EventSessionStarted, ProjectID: projectID, SessionID: "new-session", IdempotencyKey: "new-session-start"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Context, "rerun failing test") || !strings.Contains(response.Context, "historical-reference-only") {
		t.Fatalf("recovery context missing evidence/boundary: %s", response.Context)
	}
}

func TestCleanSessionCloseDoesNotCreateFalseRecoveryContext(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-clean-handoff-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "clean"}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "clean", filepath.Join(root, "checkpoint.json")), projectID)
	for _, request := range []Request{
		{Client: contracts.ClientCodex, Event: contracts.EventSessionStarted, ProjectID: projectID, SessionID: "clean-session", IdempotencyKey: "clean-start"},
		{Client: contracts.ClientCodex, Event: contracts.EventSessionCleanClose, ProjectID: projectID, SessionID: "clean-session", IdempotencyKey: "clean-close"},
	} {
		if _, err := runtime.Handle(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	response, err := runtime.Handle(context.Background(), Request{Client: contracts.ClientDSH, Event: contracts.EventSessionStarted, ProjectID: projectID, SessionID: "next-session", IdempotencyKey: "next-start"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Context != "" {
		t.Fatalf("clean close incorrectly produced recovery packet: %s", response.Context)
	}
	state, err := runtime.Engine().Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Task.Status != contracts.TaskInProgress {
		t.Fatalf("clean close changed task completion state: %#v", state.Task)
	}
}

func TestUpstreamToolPayloadFieldsBecomeCanonicalTestEvidence(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-upstream-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "upstream"}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "upstream", filepath.Join(root, "checkpoint.json")), projectID)
	response, err := runtime.Handle(context.Background(), Request{Client: contracts.ClientCodex, Event: contracts.EventToolFinished, ProjectID: projectID, SessionID: "s", IdempotencyKey: "upstream-1", Payload: json.RawMessage(`{"tool_name":"shell","tool_input":{"command":"go test ./..."},"tool_output":"one failure","exit_code":1}`)})
	if err != nil || !response.OK {
		t.Fatalf("hook failed: %#v %v", response, err)
	}
	state, err := runtime.Engine().Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.LatestTest.Command != "go test ./..." || state.LatestTest.Status != "failed" {
		t.Fatalf("upstream evidence not normalized: %#v", state.LatestTest)
	}
}

type hookMemoryBackend struct{}

func (hookMemoryBackend) Health(context.Context) error { return nil }
func (hookMemoryBackend) EnsureIdentity(context.Context, contracts.IdentitySpec) (contracts.Identity, error) {
	return contracts.Identity{}, nil
}
func (hookMemoryBackend) EnsureProjectAgent(context.Context, contracts.IsolationContext, string) (contracts.ProjectBinding, error) {
	return contracts.ProjectBinding{}, nil
}
func (hookMemoryBackend) Capture(context.Context, contracts.IsolationContext, contracts.MemoryRecord, string) (contracts.MemoryReceipt, error) {
	return contracts.MemoryReceipt{}, nil
}
func (hookMemoryBackend) Search(context.Context, contracts.IsolationContext, contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	return []contracts.MemoryRecord{{ProjectID: "prj-memory-12345678", SourceClient: contracts.ClientCodex, Kind: "sentinel", Content: "Codex sentinel"}}, nil
}

type recordingHookBackend struct {
	captured []contracts.MemoryRecord
}

func (b *recordingHookBackend) Health(context.Context) error { return nil }
func (b *recordingHookBackend) EnsureIdentity(context.Context, contracts.IdentitySpec) (contracts.Identity, error) {
	return contracts.Identity{}, nil
}
func (b *recordingHookBackend) EnsureProjectAgent(context.Context, contracts.IsolationContext, string) (contracts.ProjectBinding, error) {
	return contracts.ProjectBinding{}, nil
}
func (b *recordingHookBackend) Capture(_ context.Context, _ contracts.IsolationContext, record contracts.MemoryRecord, _ string) (contracts.MemoryReceipt, error) {
	b.captured = append(b.captured, record)
	return contracts.MemoryReceipt{RequestID: record.ContentHash}, nil
}
func (b *recordingHookBackend) Search(context.Context, contracts.IsolationContext, contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	return nil, nil
}

func TestSessionStartAddsSharedMemorySentinelToContext(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-memory-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "memory"}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "memory", filepath.Join(root, "checkpoint.json")), projectID)
	runtime.SetMemoryBackend(hookMemoryBackend{}, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"})
	response, err := runtime.Handle(context.Background(), Request{Client: contracts.ClientDSH, Event: contracts.EventSessionStarted, ProjectID: projectID, SessionID: "new", IdempotencyKey: "memory-session"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Context, "Codex sentinel") {
		t.Fatalf("shared memory sentinel missing: %s", response.Context)
	}
}

func TestUserPromptRecallsAndQueuesRedactedMemoryAfterLocalPersistence(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-hook-memory-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "memory"}); err != nil {
		t.Fatal(err)
	}
	backend := &recordingHookBackend{}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "memory", filepath.Join(root, "checkpoint.json")), projectID)
	runtime.SetSecrets([]string{"sk-hook-secret"})
	runtime.SetMemoryBackend(backend, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"})
	response, err := runtime.Handle(context.Background(), Request{
		Client: contracts.ClientCodex, Event: contracts.EventUserPrompt, ProjectID: projectID,
		SessionID: "session", IdempotencyKey: "prompt-1",
		Payload: json.RawMessage(`{"prompt":"continue with sk-hook-secret"}`),
	})
	if err != nil || !response.OK {
		t.Fatalf("prompt hook failed: %#v %v", response, err)
	}
	if len(backend.captured) != 1 || strings.Contains(backend.captured[0].Content, "sk-hook-secret") {
		t.Fatalf("captured memory was not redacted or was not delivered: %#v", backend.captured)
	}
	if !strings.Contains(backend.captured[0].Content, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %#v", backend.captured[0])
	}
	if _, err := store.GetWorkState(context.Background(), projectID); err != nil {
		t.Fatalf("local state was not persisted before memory delivery: %v", err)
	}
}

func TestUpstreamPayloadSessionAndEventIDsAreStable(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-hook-session-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "session"}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "session", filepath.Join(root, "checkpoint.json")), projectID)
	response, err := runtime.Handle(context.Background(), Request{Client: contracts.ClientDSH, Event: contracts.EventToolFinished, ProjectID: projectID, Payload: json.RawMessage(`{"session_id":"upstream-session","event_id":"upstream-event","command":"go test"}`)})
	if err != nil || response.SessionID != "upstream-session" {
		t.Fatalf("upstream session was not adopted: %#v %v", response, err)
	}
	duplicate, err := runtime.Handle(context.Background(), Request{Client: contracts.ClientDSH, Event: contracts.EventToolFinished, ProjectID: projectID, Payload: json.RawMessage(`{"session_id":"upstream-session","event_id":"upstream-event","command":"go test"}`)})
	if err != nil || duplicate.Persisted {
		t.Fatalf("upstream event id was not idempotent: %#v %v", duplicate, err)
	}
}

func TestKnownSecretDoesNotPersistInLocalEventOrCheckpoint(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-secret-scan-12345678"
	databasePath := filepath.Join(root, "state.db")
	checkpointPath := filepath.Join(root, "checkpoint.json")
	store, err := storage.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "secret"}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "secret", checkpointPath), projectID)
	runtime.SetSecrets([]string{"sk-known-secret"})
	if _, err := runtime.Handle(context.Background(), Request{Client: contracts.ClientCodex, Event: contracts.EventToolFinished, ProjectID: projectID, SessionID: "secret-session", IdempotencyKey: "secret-event", Payload: json.RawMessage(`{"command":"go test","summary":"token sk-known-secret"}`)}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm", checkpointPath} {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "sk-known-secret") {
			t.Fatalf("secret persisted in %s", path)
		}
	}
}

func FuzzServeJSONNeverPanics(f *testing.F) {
	for _, seed := range []string{"", "{}", "not-json", `{"payload":{"command":"go test"}}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		var output bytes.Buffer
		if err := ServeJSON(context.Background(), nil, strings.NewReader(input), &output); err != nil {
			t.Fatalf("ServeJSON returned an encoding error: %v", err)
		}
		var response Response
		if err := json.Unmarshal(output.Bytes(), &response); err != nil {
			t.Fatalf("ServeJSON returned invalid JSON: %v", err)
		}
	})
}
