package hooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/continuity"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/recovery"
	"github.com/baron-shared-brain/baron/internal/storage"
)

type Request struct {
	Client         contracts.HookClient `json:"client"`
	Event          contracts.EventType  `json:"event_type"`
	ProjectID      string               `json:"project_id"`
	ProjectRoot    string               `json:"project_root,omitempty"`
	SessionID      string               `json:"session_id,omitempty"`
	IdempotencyKey string               `json:"idempotency_key,omitempty"`
	Payload        json.RawMessage      `json:"payload,omitempty"`
}

type Response struct {
	OK         bool   `json:"ok"`
	Persisted  bool   `json:"persisted"`
	ProjectID  string `json:"project_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Historical bool   `json:"historical_context_available,omitempty"`
	Context    string `json:"context,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Runtime struct {
	store          *storage.Store
	engine         *continuity.Engine
	projectID      string
	secrets        []string
	backend        contracts.MemoryBackend
	isolation      contracts.IsolationContext
	syncer         *continuity.Syncer
	repositoryRoot string
}

func NewRuntime(store *storage.Store, engine *continuity.Engine, projectID string) *Runtime {
	return &Runtime{store: store, engine: engine, projectID: projectID}
}

func (r *Runtime) SetSecrets(secrets []string) {
	r.secrets = append([]string(nil), secrets...)
	r.rebuildSyncer()
}

func (r *Runtime) SetMemoryBackend(backend contracts.MemoryBackend, isolation contracts.IsolationContext) {
	r.backend = backend
	r.isolation = isolation
	r.rebuildSyncer()
}

func (r *Runtime) SetRepositoryRoot(root string) {
	if r != nil {
		r.repositoryRoot = root
	}
}

func (r *Runtime) rebuildSyncer() {
	if r == nil || r.store == nil || r.backend == nil {
		return
	}
	r.syncer = continuity.NewSyncer(r.store, r.backend, r.isolation, r.secrets)
}

func (r *Runtime) Engine() *continuity.Engine {
	if r == nil {
		return nil
	}
	return r.engine
}

func (r *Runtime) Handle(ctx context.Context, request Request) (Response, error) {
	response := Response{ProjectID: request.ProjectID, SessionID: request.SessionID}
	if r == nil || r.store == nil || r.engine == nil {
		return response, fmt.Errorf("Baron hook runtime is not initialized")
	}
	if request.ProjectID == "" || request.ProjectID != r.projectID {
		return response, fmt.Errorf("hook project identity mismatch")
	}
	if request.Client == "" || request.Event == "" {
		return response, fmt.Errorf("hook client and event_type are required")
	}
	if len(request.Payload) == 0 {
		request.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(request.Payload) {
		return response, fmt.Errorf("hook payload must be valid JSON")
	}
	request.Payload = json.RawMessage(config.Redact(string(request.Payload), r.secrets))
	if request.SessionID == "" {
		request.SessionID = payloadField(request.Payload, "session_id", "sessionId", "session")
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = payloadField(request.Payload, "idempotency_key", "idempotencyKey", "event_id", "eventId", "id")
		if request.IdempotencyKey == "" {
			request.IdempotencyKey = contracts.HashContent(string(request.Payload) + string(request.Client) + string(request.Event))
		}
	}
	if request.SessionID == "" {
		request.SessionID = storage.NewID("ses")
	}
	response.SessionID = request.SessionID
	var previousState *continuity.WorkState
	if request.Event == contracts.EventSessionStarted {
		if previous, previousErr := r.engine.Load(ctx); previousErr == nil && previous.SessionID != "" && previous.SessionID != request.SessionID && previous.SessionState != contracts.SessionCleanClosed {
			previousState = &previous
		}
	}
	inserted, err := r.store.InsertEvent(ctx, storage.Event{
		EventID: storage.NewID("evt"), ProjectID: request.ProjectID, SessionID: request.SessionID,
		Client: request.Client, Type: request.Event, OccurredAt: time.Now().UTC(), Payload: request.Payload,
		IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return response, err
	}
	if !inserted {
		response.OK = true
		response.Persisted = false
		return response, nil
	}

	sessionState := contracts.SessionActive
	if request.Event == contracts.EventSessionCleanClose {
		sessionState = contracts.SessionCleanClosed
	} else if request.Event == contracts.EventSessionInterrupted {
		sessionState = contracts.SessionInterrupted
	}
	if request.Event == contracts.EventSessionStarted {
		if err := r.store.StartSession(ctx, storage.Session{SessionID: request.SessionID, ProjectID: request.ProjectID, Client: request.Client, State: contracts.SessionActive}); err != nil {
			return response, err
		}
	} else if request.Event == contracts.EventSessionCleanClose || request.Event == contracts.EventSessionInterrupted {
		state := sessionState
		if request.Event == contracts.EventSessionInterrupted {
			state = contracts.SessionInterrupted
		}
		if err := r.store.UpdateSession(ctx, request.SessionID, state, "hook lifecycle event"); err != nil {
			return response, err
		}
	}

	state, loadErr := r.engine.Load(ctx)
	if loadErr != nil && !isMissing(loadErr) {
		return response, loadErr
	}
	if loadErr != nil {
		state = continuity.WorkState{ProjectID: request.ProjectID}
	}
	state.LastClient = request.Client
	state.SessionID = request.SessionID
	state.SessionState = sessionState
	if state.Task.Status == "" {
		state.Task.Status = contracts.TaskInProgress
	}
	applyEvidence(&state, request)
	if r.repositoryRoot != "" && (request.Event == contracts.EventSessionStarted || request.Event == contracts.EventFileChanged || request.Event == contracts.EventToolFinished || request.Event == contracts.EventTestFinished || request.Event == contracts.EventCheckpointUpdated) {
		if repository, repositoryErr := continuity.InspectRepository(ctx, r.repositoryRoot); repositoryErr == nil {
			state.Repository = repository
		}
	}
	if err := r.engine.Save(ctx, state); err != nil {
		return response, err
	}
	response.OK = true
	response.Persisted = true
	if previousState != nil {
		currentRepository := previousState.Repository
		if r.repositoryRoot != "" {
			if inspected, inspectErr := continuity.InspectRepository(ctx, r.repositoryRoot); inspectErr == nil {
				currentRepository = inspected
			}
		}
		response.Context = recovery.Build(*previousState, currentRepository, nil).Render()
		_ = r.store.RecordHandoff(ctx, storage.HandoffReceipt{
			ProjectID: request.ProjectID, SourceClient: previousState.LastClient, TargetClient: request.Client,
			SourceSessionID: previousState.SessionID, TargetSessionID: request.SessionID,
			CheckpointID: contracts.HashContent(previousState.ProjectID + "|" + previousState.SessionID + "|" + previousState.UpdatedAt.UTC().Format(time.RFC3339Nano)),
		})
	}
	// The local event and checkpoint are durable before this point. Remote
	// capture/recall is deliberately best-effort so memory outages never stop
	// the provider session.
	r.captureMemory(ctx, request, state)
	if r.backend != nil && shouldRecall(request.Event) {
		requestIsolation := r.isolation
		requestIsolation.SessionID = request.SessionID
		memoryCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		packet, packetErr := continuity.BuildContext(memoryCtx, state, r.backend, requestIsolation, recallQuery(request, state), 5000, r.secrets)
		cancel()
		if packetErr == nil {
			response.Historical = len(packet.Records) > 0
			if response.Context != "" {
				response.Context += "\n"
			}
			response.Context += packet.Text
		}
	}
	return response, nil
}

func (r *Runtime) captureMemory(ctx context.Context, request Request, state continuity.WorkState) {
	if r == nil || r.syncer == nil || !shouldCapture(request.Event) {
		return
	}
	record, ok := memoryRecord(request, state)
	if !ok {
		return
	}
	key := "mem-" + contracts.HashContent(string(request.Event)+"|"+request.SessionID+"|"+record.ContentHash)
	if _, err := r.syncer.QueueCapture(ctx, record, key); err != nil {
		return
	}
	flushCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	_, _ = r.syncer.Flush(flushCtx, 2)
	cancel()
}

func shouldCapture(event contracts.EventType) bool {
	switch event {
	case contracts.EventUserPrompt, contracts.EventAssistantFinal, contracts.EventDecisionRecorded,
		contracts.EventToolFinished, contracts.EventTestFinished, contracts.EventErrorObserved,
		contracts.EventCheckpointUpdated:
		return true
	default:
		return false
	}
}

func shouldRecall(event contracts.EventType) bool {
	return event == contracts.EventSessionStarted || event == contracts.EventUserPrompt || event == contracts.EventCheckpointUpdated
}

func recallQuery(request Request, state continuity.WorkState) contracts.MemoryQuery {
	query := state.Task.Goal + " " + state.Task.CurrentStep + " " + state.Task.NextAction
	var payload map[string]any
	if json.Unmarshal(request.Payload, &payload) == nil {
		for _, key := range []string{"prompt", "text", "message", "summary", "command"} {
			if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
				query += " " + value
			}
		}
	}
	return contracts.MemoryQuery{Text: boundedText(query, 4000), Limit: 10}
}

func memoryRecord(request Request, state continuity.WorkState) (contracts.MemoryRecord, bool) {
	var payload map[string]any
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		return contracts.MemoryRecord{}, false
	}
	content := ""
	for _, key := range []string{"prompt", "text", "message", "response", "summary", "decision", "content"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			content = value
			break
		}
	}
	if content == "" {
		command, _ := payload["command"].(string)
		if command == "" {
			if toolInput, ok := payload["tool_input"].(map[string]any); ok {
				command, _ = toolInput["command"].(string)
			}
		}
		summary, _ := payload["tool_output"].(string)
		if summary == "" {
			summary, _ = payload["summary"].(string)
		}
		if command != "" || summary != "" {
			content = strings.TrimSpace(command + "\n" + summary)
		}
	}
	if strings.TrimSpace(content) == "" {
		return contracts.MemoryRecord{}, false
	}
	metadata := map[string]string{"event_type": string(request.Event), "task_status": string(state.Task.Status)}
	if state.LatestTest.Command != "" {
		metadata["latest_test_command"] = state.LatestTest.Command
	}
	record := contracts.MemoryRecord{
		ProjectID: request.ProjectID, SourceClient: request.Client, SessionID: request.SessionID,
		Kind: string(request.Event), Content: boundedText(content, 32*1024), Metadata: metadata,
	}
	record.Normalize()
	return record, true
}

func boundedText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 {
		return ""
	}
	if len(value) <= max {
		return value
	}
	const suffix = "...[truncated]"
	if max <= len(suffix) {
		return value[:max]
	}
	return value[:max-len(suffix)] + suffix
}

func ServeJSON(ctx context.Context, runtime *Runtime, input io.Reader, output io.Writer) error {
	var request Request
	decoder := json.NewDecoder(io.LimitReader(input, 2*1024*1024))
	if err := decoder.Decode(&request); err != nil {
		return json.NewEncoder(output).Encode(Response{OK: false, Error: config.Redact(err.Error(), nil)})
	}
	response, err := runtime.Handle(ctx, request)
	if err != nil {
		response.OK = false
		var secrets []string
		if runtime != nil {
			secrets = runtime.secrets
		}
		response.Error = config.Redact(err.Error(), secrets)
	}
	return json.NewEncoder(output).Encode(response)
}

func applyEvidence(state *continuity.WorkState, request Request) {
	var payload map[string]any
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	command, _ := payload["command"].(string)
	summary, _ := payload["summary"].(string)
	status, _ := payload["status"].(string)
	completionVerified, _ := payload["completion_verified"].(bool)
	if completionVerified {
		state.Task.CompletionVerified = true
	}
	if taskStatus, ok := payload["task_status"].(string); ok {
		switch contracts.TaskStatus(taskStatus) {
		case contracts.TaskPlanned, contracts.TaskInProgress, contracts.TaskBlocked, contracts.TaskFailed:
			state.Task.Status = contracts.TaskStatus(taskStatus)
		case contracts.TaskCompleted:
			if state.Task.CompletionVerified {
				state.Task.Status = contracts.TaskCompleted
			}
		}
	}
	if goal, ok := payload["goal"].(string); ok && strings.TrimSpace(goal) != "" {
		state.Task.Goal = boundedText(goal, 4096)
	}
	if step, ok := payload["current_step"].(string); ok && strings.TrimSpace(step) != "" {
		state.Task.CurrentStep = boundedText(step, 2048)
	}
	if step, ok := payload["last_successful_step"].(string); ok && strings.TrimSpace(step) != "" {
		state.Task.LastSuccessfulStep = boundedText(step, 2048)
	}
	if command == "" {
		if toolInput, ok := payload["tool_input"].(map[string]any); ok {
			command, _ = toolInput["command"].(string)
		}
	}
	if command == "" {
		command, _ = payload["tool_name"].(string)
	}
	if summary == "" {
		summary, _ = payload["tool_output"].(string)
	}
	command = boundedText(command, 2048)
	summary = boundedText(summary, 8192)
	var exitCode *int
	if exit, ok := payload["exit_code"].(float64); ok {
		value := int(exit)
		exitCode = &value
		if exit != 0 {
			status = "failed"
		} else if status == "" {
			status = "passed"
		}
	}
	if request.Event == contracts.EventToolFinished || request.Event == contracts.EventTestFinished || command != "" {
		state.LatestTest.Command = command
		state.LatestTest.Status = status
		state.LatestTest.Summary = summary
		state.LatestTest.ExitCode = exitCode
		state.LatestTest.ObservedAt = now
	}
	if request.Event == contracts.EventErrorObserved {
		class, _ := payload["class"].(string)
		state.Errors = appendBoundedError(state.Errors, continuity.ErrorEvidence{Class: boundedText(class, 256), Summary: summary, ObservedAt: now})
	}
	if request.Event == contracts.EventFileChanged {
		if file, ok := payload["file"].(string); ok && strings.TrimSpace(file) != "" {
			file = boundedText(file, 1024)
			for _, existing := range state.Repository.ChangedFiles {
				if existing == file {
					return
				}
			}
			state.Repository.ChangedFiles = append(state.Repository.ChangedFiles, file)
		}
	}
	if request.Event == contracts.EventAssistantFinal {
		if next, ok := payload["next_action"].(string); ok {
			state.Task.NextAction = boundedText(next, 2048)
		}
	}
}

func payloadField(payload json.RawMessage, keys ...string) string {
	var values map[string]any
	if json.Unmarshal(payload, &values) != nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return boundedText(value, 256)
		}
	}
	return ""
}

func appendBoundedError(existing []continuity.ErrorEvidence, value continuity.ErrorEvidence) []continuity.ErrorEvidence {
	existing = append(existing, value)
	if len(existing) > 20 {
		existing = existing[len(existing)-20:]
	}
	return existing
}

func isMissing(err error) bool {
	return errorsIs(err, sql.ErrNoRows)
}

func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		unwrapped, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}
