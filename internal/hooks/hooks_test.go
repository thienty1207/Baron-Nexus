package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestNewAgentReceivesLatestOtherClientCheckpointLocally(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-local-handoff-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "local-handoff"}); err != nil {
		t.Fatal(err)
	}
	engine := continuity.NewEngine(store, projectID, "local-handoff", filepath.Join(root, "checkpoint.json"))
	if err := engine.Save(context.Background(), continuity.WorkState{
		ProjectID: projectID, ProjectName: "local-handoff", LastClient: contracts.ClientCodex, SessionID: "codex-old", SessionState: contracts.SessionActive,
		Task: continuity.TaskState{Goal: "Recover the unfinished handoff", Status: contracts.TaskInProgress, CurrentStep: "waiting for DSH"},
	}); err != nil {
		t.Fatal(err)
	}
	if inserted, err := store.InsertEvent(context.Background(), storage.Event{
		ProjectID: projectID, SessionID: "codex-old", Client: contracts.ClientCodex, Type: contracts.EventCheckpointUpdated,
		OccurredAt: time.Now().UTC(), Payload: json.RawMessage(`{"summary":"CODEX_LOCAL_HANDOFF_SENTINEL"}`), IdempotencyKey: "codex-local-handoff",
	}); err != nil || !inserted {
		t.Fatalf("insert checkpoint: inserted=%v err=%v", inserted, err)
	}
	runtime := NewRuntime(store, engine, projectID)
	response, err := runtime.Handle(context.Background(), Request{
		Client: contracts.ClientDSH, Event: contracts.EventSessionStarted, ProjectID: projectID, SessionID: "dsh-new", IdempotencyKey: "dsh-local-handoff",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Context, "CODEX_LOCAL_HANDOFF_SENTINEL") || !strings.Contains(response.Context, "baron-local-handoff") {
		t.Fatalf("local cross-agent checkpoint missing: %s", response.Context)
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

func TestVerifiedCompletionSurvivesAssistantFinalAfterCleanClose(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-verified-close-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "verified-close"}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "verified-close", filepath.Join(root, "checkpoint.json")), projectID)
	requests := []Request{
		{Client: contracts.ClientDSH, Event: contracts.EventSessionStarted, ProjectID: projectID, SessionID: "verified-session", IdempotencyKey: "verified-start"},
		{Client: contracts.ClientDSH, Event: contracts.EventToolFinished, ProjectID: projectID, SessionID: "verified-session", IdempotencyKey: "verified-tool", Payload: json.RawMessage(`{"command":"printf VERIFIED","summary":"VERIFIED","status":"passed","exit_code":0}`)},
		// DSH headless currently flushes before its final assistant event.
		{Client: contracts.ClientDSH, Event: contracts.EventSessionCleanClose, ProjectID: projectID, SessionID: "verified-session", IdempotencyKey: "verified-close"},
		{Client: contracts.ClientDSH, Event: contracts.EventAssistantFinal, ProjectID: projectID, SessionID: "verified-session", IdempotencyKey: "verified-final", Payload: json.RawMessage(`{"completion_verified":true,"task_status":"completed","last_successful_step":"completion probe","response":"VERIFIED"}`)},
	}
	for _, request := range requests {
		if _, err := runtime.Handle(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	state, err := runtime.Engine().Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionState != contracts.SessionCleanClosed || state.Task.Status != contracts.TaskCompleted || !state.Task.CompletionVerified {
		t.Fatalf("verified clean completion was changed by the final event: %#v", state)
	}
	response, err := runtime.Handle(context.Background(), Request{
		Client: contracts.ClientCodex, Event: contracts.EventSessionStarted, ProjectID: projectID, SessionID: "next-session", IdempotencyKey: "next-start",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Context, "Session: interrupted") {
		t.Fatalf("verified completion was falsely handed off as interrupted: %s", response.Context)
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

func TestCodexOfficialLifecycleFieldsBecomeHandoffEvidence(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-codex-official-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "codex-official"}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "codex-official", filepath.Join(root, "checkpoint.json")), projectID)
	toolResponse := `Failed to create stream fd: Operation not permitted\nCODEX_OFFICIAL_FAILURE_SENTINEL`
	if _, err := runtime.Handle(context.Background(), Request{
		Client: contracts.ClientCodex, Event: contracts.EventToolFinished, ProjectID: projectID, SessionID: "codex-official-session",
		IdempotencyKey: "codex-official-tool", Payload: json.RawMessage(`{"tool_name":"Bash","tool_input":{"command":"bash -lc 'printf CODEX_OFFICIAL_FAILURE_SENTINEL >&2; exit 23'"},"tool_response":"` + toolResponse + `"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Handle(context.Background(), Request{
		Client: contracts.ClientCodex, Event: contracts.EventAssistantFinal, ProjectID: projectID, SessionID: "codex-official-session",
		IdempotencyKey: "codex-official-final", Payload: json.RawMessage(`{"last_assistant_message":"Exit code: 23"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Handle(context.Background(), Request{
		Client: contracts.ClientCodex, Event: contracts.EventSessionCleanClose, ProjectID: projectID, SessionID: "codex-official-session",
		IdempotencyKey: "codex-official-close",
	}); err != nil {
		t.Fatal(err)
	}
	state, err := runtime.Engine().Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.LatestTest.Command == "" || !strings.Contains(state.LatestTest.Summary, "CODEX_OFFICIAL_FAILURE_SENTINEL") || state.LatestTest.Status != "failed" || state.LatestTest.ExitCode == nil || *state.LatestTest.ExitCode != 23 {
		t.Fatalf("official Codex failure evidence was not normalized: %#v", state.LatestTest)
	}
	response, err := runtime.Handle(context.Background(), Request{
		Client: contracts.ClientDSH, Event: contracts.EventSessionStarted, ProjectID: projectID, SessionID: "dsh-official-session",
		IdempotencyKey: "dsh-official-start",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Context, "Exit code: 23") || !strings.Contains(response.Context, "CODEX_OFFICIAL_FAILURE_SENTINEL") {
		t.Fatalf("handoff omitted official Codex fields: %s", response.Context)
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

type sessionAgnosticMemoryBackend struct {
	searchSessionID string
}

func (b *sessionAgnosticMemoryBackend) Health(context.Context) error { return nil }
func (b *sessionAgnosticMemoryBackend) EnsureIdentity(context.Context, contracts.IdentitySpec) (contracts.Identity, error) {
	return contracts.Identity{}, nil
}
func (b *sessionAgnosticMemoryBackend) EnsureProjectAgent(context.Context, contracts.IsolationContext, string) (contracts.ProjectBinding, error) {
	return contracts.ProjectBinding{}, nil
}
func (b *sessionAgnosticMemoryBackend) Capture(context.Context, contracts.IsolationContext, contracts.MemoryRecord, string) (contracts.MemoryReceipt, error) {
	return contracts.MemoryReceipt{}, nil
}
func (b *sessionAgnosticMemoryBackend) Search(_ context.Context, isolation contracts.IsolationContext, _ contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	b.searchSessionID = isolation.SessionID
	return []contracts.MemoryRecord{{ProjectID: isolation.ProjectID, SourceClient: contracts.ClientCodex, Kind: "handoff", Content: "CODEX_TO_DSH_PROJECT_SENTINEL"}}, nil
}

type countingRecallBackend struct {
	searchCalls int
}

func (b *countingRecallBackend) Health(context.Context) error { return nil }
func (b *countingRecallBackend) EnsureIdentity(context.Context, contracts.IdentitySpec) (contracts.Identity, error) {
	return contracts.Identity{}, nil
}
func (b *countingRecallBackend) EnsureProjectAgent(context.Context, contracts.IsolationContext, string) (contracts.ProjectBinding, error) {
	return contracts.ProjectBinding{}, nil
}
func (b *countingRecallBackend) Capture(context.Context, contracts.IsolationContext, contracts.MemoryRecord, string) (contracts.MemoryReceipt, error) {
	return contracts.MemoryReceipt{}, nil
}
func (b *countingRecallBackend) Search(context.Context, contracts.IsolationContext, contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	b.searchCalls++
	return []contracts.MemoryRecord{{Kind: "unexpected-remote", Content: "remote should not be queried"}}, nil
}

func TestLocalSufficientPromptSkipsRemoteRecall(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-local-only-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "local-only"}); err != nil {
		t.Fatal(err)
	}
	engine := continuity.NewEngine(store, projectID, "local-only", filepath.Join(root, "checkpoint.json"))
	if err := engine.Save(context.Background(), continuity.WorkState{
		ProjectID:  projectID,
		Repository: continuity.RepositoryEvidence{GitHead: "head-local", DiffHash: "diff-local", Branch: "main"},
		Task:       continuity.TaskState{Status: contracts.TaskInProgress, Goal: "local task"},
	}); err != nil {
		t.Fatal(err)
	}
	backend := &countingRecallBackend{}
	runtime := NewRuntime(store, engine, projectID)
	runtime.SetMemoryBackend(backend, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"})
	response, err := runtime.Handle(context.Background(), Request{
		Client: contracts.ClientCodex, Event: contracts.EventUserPrompt, ProjectID: projectID,
		SessionID: "local-session", IdempotencyKey: "local-prompt", Payload: json.RawMessage(`{"task_id":"task-new","changed_files":["internal/new.go"],"module_paths":["internal/new"],"prompt":"continue local work"}`),
	})
	if err != nil || !response.OK {
		t.Fatalf("local-only prompt failed: %#v %v", response, err)
	}
	if backend.searchCalls != 0 {
		t.Fatalf("local-sufficient prompt queried remote memory %d times", backend.searchCalls)
	}
}

func TestRemoteRecallUsesLocalCacheForUnchangedFingerprint(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-recall-cache-hook-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "recall cache hook"}); err != nil {
		t.Fatal(err)
	}
	backend := &countingRecallBackend{}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "recall cache hook", filepath.Join(root, "checkpoint.json")), projectID)
	runtime.SetMemoryBackend(backend, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"})
	request := Request{
		Client: contracts.ClientCodex, Event: contracts.EventUserPrompt, ProjectID: projectID,
		SessionID: "same-session", Payload: json.RawMessage(`{"prompt":"recover the previous task"}`),
	}
	for index := 1; index <= 2; index++ {
		request.IdempotencyKey = fmt.Sprintf("cached-prompt-%d", index)
		response, handleErr := runtime.Handle(context.Background(), request)
		if handleErr != nil || !response.OK {
			t.Fatalf("cached prompt %d failed: %#v %v", index, response, handleErr)
		}
	}
	if backend.searchCalls != 1 {
		t.Fatalf("unchanged recovery fingerprint searched remote %d times", backend.searchCalls)
	}
}

func TestRecallSearchIsProjectScopedRatherThanCurrentSessionScoped(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-recall-scope-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "recall-scope"}); err != nil {
		t.Fatal(err)
	}
	backend := &sessionAgnosticMemoryBackend{}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "recall-scope", filepath.Join(root, "checkpoint.json")), projectID)
	runtime.SetMemoryBackend(backend, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"})
	response, err := runtime.Handle(context.Background(), Request{
		Client: contracts.ClientDSH, Event: contracts.EventCheckpointUpdated, ProjectID: projectID,
		SessionID: "dsh-new-session", IdempotencyKey: "recall-scope-checkpoint",
		Payload: json.RawMessage(`{"prompt":"continue the unfinished handoff"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if backend.searchSessionID != "" {
		t.Fatalf("recall search was incorrectly restricted to current session %q", backend.searchSessionID)
	}
	if !strings.Contains(response.Context, "CODEX_TO_DSH_PROJECT_SENTINEL") {
		t.Fatalf("project-scoped historical memory was not returned: %s", response.Context)
	}
}

type sessionKnowledgeBackend struct{}

func (sessionKnowledgeBackend) Retrieve(context.Context, contracts.IsolationContext, contracts.MemoryQuery) ([]continuity.KnowledgeCitation, error) {
	return []continuity.KnowledgeCitation{
		{Source: "wiki", Reference: "docs/auth.md", Content: "refresh token contract", Trust: "historical-reference-only", Freshness: "wiki-v2"},
		{Source: "codegraph", Reference: "RefreshToken", Content: "caller: middleware.go", Trust: "historical-reference-only", Freshness: "abc123"},
	}, nil
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

func TestSessionStartFlushesPreviouslyQueuedRemoteOperations(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-queue-repair-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "queue-repair"}); err != nil {
		t.Fatal(err)
	}
	isolation := contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "queue-repair", filepath.Join(root, "checkpoint.json")), projectID)
	runtime.SetMemoryBackend(hookMemoryBackend{}, isolation)
	var delivered []string
	runtime.SetQueueOperationHandler(func(_ context.Context, item storage.QueueItem) (string, error) {
		delivered = append(delivered, item.Operation)
		return item.Operation + "-request", nil
	})
	if _, err := runtime.syncer.QueueOperation(context.Background(), storage.QueueOperationCoreUpdate, "old-core", map[string]any{"summary": "queued before the next session"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Handle(context.Background(), Request{Client: contracts.ClientCodex, Event: contracts.EventSessionStarted, ProjectID: projectID, SessionID: "new-session", IdempotencyKey: "new-session-start"}); err != nil {
		t.Fatal(err)
	}
	if !containsHookString(delivered, storage.QueueOperationCoreUpdate) {
		t.Fatalf("previously queued operation was not flushed on session start: %#v", delivered)
	}
}

func TestSessionStartContextIncludesBoundedWikiAndCodeGraphCitations(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-session-knowledge-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "session-knowledge"}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "session-knowledge", filepath.Join(root, "checkpoint.json")), projectID)
	isolation := contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"}
	runtime.SetMemoryBackend(hookMemoryBackend{}, isolation)
	runtime.SetKnowledgeBackend(sessionKnowledgeBackend{}, isolation)
	response, err := runtime.Handle(context.Background(), Request{Client: contracts.ClientDSH, Event: contracts.EventSessionStarted, ProjectID: projectID, SessionID: "session", IdempotencyKey: "session-knowledge-start"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"docs/auth.md", "RefreshToken", "historical-reference-only"} {
		if !strings.Contains(response.Context, want) {
			t.Fatalf("session-start context omitted %q: %s", want, response.Context)
		}
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

func TestAssistantFinalQueuesCoreAndScenarioContinuitySummaries(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-summary-hook-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "summary"}); err != nil {
		t.Fatal(err)
	}
	backend := &recordingHookBackend{}
	runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "summary", filepath.Join(root, "checkpoint.json")), projectID)
	runtime.SetMemoryBackend(backend, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"})
	operations := []string{}
	runtime.SetQueueOperationHandler(func(_ context.Context, item storage.QueueItem) (string, error) {
		operations = append(operations, item.Operation)
		return item.Operation + "-request", nil
	})
	response, err := runtime.Handle(context.Background(), Request{
		Client: contracts.ClientCodex, Event: contracts.EventAssistantFinal, ProjectID: projectID, SessionID: "summary-session", IdempotencyKey: "summary-final",
		Payload: json.RawMessage(`{"summary":"refresh token flow implemented", "next_action":"run integration tests", "symbol":"RefreshToken"}`),
	})
	if err != nil || !response.OK {
		t.Fatalf("assistant final hook failed: %#v %v", response, err)
	}
	if !containsHookString(operations, storage.QueueOperationCoreUpdate) || !containsHookString(operations, storage.QueueOperationScenarioUpdate) {
		t.Fatalf("typed continuity summaries were not delivered: %#v", operations)
	}
}

func containsHookString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func TestContinuityRecallAndMemoryCaptureIncludeRelevantRepositoryEvidence(t *testing.T) {
	state := continuity.WorkState{
		Task:       continuity.TaskState{Goal: "fix authentication", CurrentStep: "refresh token", NextAction: "rerun integration test"},
		Repository: continuity.RepositoryEvidence{Branch: "feature/auth", GitHead: "abc123", ChangedFiles: []string{"auth.go", "refresh.go"}},
		LatestTest: continuity.TestEvidence{Command: "go test ./...", Status: "failed", Summary: "401 Unauthorized"},
		Errors:     []continuity.ErrorEvidence{{Class: "integration", Summary: "401 Unauthorized"}},
	}
	request := Request{Client: contracts.ClientCodex, Event: contracts.EventAssistantFinal, ProjectID: "prj-evidence-12345678", SessionID: "session", Payload: json.RawMessage(`{"summary":"JWT refresh flow", "file":"middleware.go", "symbol":"RefreshToken"}`)}
	query := recallQuery(request, state)
	for _, want := range []string{"auth.go", "refresh.go", "RefreshToken", "401 Unauthorized", "feature/auth"} {
		if !strings.Contains(query.Text, want) {
			t.Fatalf("recall query omitted relevant term %q: %s", want, query.Text)
		}
	}
	if len(query.Files) != 3 || query.Files[2] != "middleware.go" || len(query.Symbols) != 1 || query.Symbols[0] != "RefreshToken" {
		t.Fatalf("structured retrieval hints were not preserved: %#v", query)
	}
	record, ok := memoryRecord(request, state)
	if !ok || !strings.Contains(record.Content, "JWT refresh flow") || len(record.EvidenceRefs) != 3 || record.Metadata["symbol"] != "RefreshToken" {
		t.Fatalf("memory record omitted task evidence: %#v ok=%v", record, ok)
	}
}

func TestRecallQueryPrioritizesCurrentPromptBeforeRepositoryEvidence(t *testing.T) {
	changedFiles := make([]string, 0, 300)
	for index := 0; index < 300; index++ {
		changedFiles = append(changedFiles, fmt.Sprintf("generated/path/%03d/very-long-source-file-name.go", index))
	}
	request := Request{
		Client: contracts.ClientDSH, Event: contracts.EventCheckpointUpdated,
		Payload: json.RawMessage(`{"prompt":"CODEX_TO_DSH_PROMPT_PRIORITY_SENTINEL"}`),
	}
	query := recallQuery(request, continuity.WorkState{Repository: continuity.RepositoryEvidence{ChangedFiles: changedFiles}})
	if !strings.Contains(query.Text, "CODEX_TO_DSH_PROMPT_PRIORITY_SENTINEL") {
		t.Fatalf("current handoff prompt was truncated out of recall query: %s", query.Text)
	}
}

func TestDSHAndCodexShareCanonicalContinuityState(t *testing.T) {
	type fixture struct {
		client contracts.HookClient
		state  continuity.WorkState
	}
	run := func(client contracts.HookClient) fixture {
		root := t.TempDir()
		projectID := "prj-symmetric-12345678"
		store, err := storage.Open(filepath.Join(root, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "symmetric"}); err != nil {
			t.Fatal(err)
		}
		runtime := NewRuntime(store, continuity.NewEngine(store, projectID, "symmetric", filepath.Join(root, "checkpoint.json")), projectID)
		events := []Request{
			{Event: contracts.EventSessionStarted, IdempotencyKey: "start"},
			{Event: contracts.EventUserPrompt, IdempotencyKey: "prompt", Payload: json.RawMessage(`{"goal":"finish auth","current_step":"refresh flow"}`)},
			{Event: contracts.EventToolFinished, IdempotencyKey: "test", Payload: json.RawMessage(`{"command":"go test ./...","exit_code":1,"summary":"401 Unauthorized","file":"auth.go"}`)},
			{Event: contracts.EventAssistantFinal, IdempotencyKey: "final", Payload: json.RawMessage(`{"summary":"refresh flow is still failing","next_action":"fix middleware"}`)},
		}
		for index := range events {
			events[index].Client = client
			events[index].ProjectID = projectID
			events[index].SessionID = "session-1"
			if _, err := runtime.Handle(context.Background(), events[index]); err != nil {
				t.Fatal(err)
			}
		}
		state, err := runtime.Engine().Load(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return fixture{client: client, state: state}
	}
	dsh := run(contracts.ClientDSH)
	codex := run(contracts.ClientCodex)
	if dsh.state.Task != codex.state.Task || dsh.state.LatestTest.Command != codex.state.LatestTest.Command ||
		dsh.state.LatestTest.Status != codex.state.LatestTest.Status || dsh.state.LatestTest.Summary != codex.state.LatestTest.Summary ||
		strings.Join(dsh.state.Repository.ChangedFiles, ",") != strings.Join(codex.state.Repository.ChangedFiles, ",") {
		t.Fatalf("DSH/Codex canonical states diverged: dsh=%#v codex=%#v", dsh.state, codex.state)
	}
	if dsh.state.LastClient != contracts.ClientDSH || codex.state.LastClient != contracts.ClientCodex {
		t.Fatalf("agent identity was not preserved independently: dsh=%s codex=%s", dsh.state.LastClient, codex.state.LastClient)
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
