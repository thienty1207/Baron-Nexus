package hooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"sort"
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
	store            *storage.Store
	engine           *continuity.Engine
	projectID        string
	secrets          []string
	backend          contracts.MemoryBackend
	knowledge        continuity.KnowledgeBackend
	isolation        contracts.IsolationContext
	syncer           *continuity.Syncer
	operationHandler continuity.QueueOperationHandler
	repositoryRoot   string
}

const (
	// Hook payloads are event evidence, not an unbounded transcript store. A
	// bounded row keeps concurrent Codex hooks below the provider timeout.
	maxHookPayloadBytes      = 64 * 1024
	maxLifecycleStringBytes  = 8192
	maxLifecycleMapKeys      = 64
	maxLifecycleArrayItems   = 100
	maxLifecycleNestingDepth = 6
)

var lifecyclePayloadPriority = []string{
	"task_id", "active_task_id", "session_id", "event_id", "idempotency_key",
	"command", "tool_name", "tool_output", "tool_response", "summary", "error",
	"status", "exit_code", "task_status", "goal", "current_step", "last_successful_step", "next_action",
	"completion_verified", "completion_policy", "verification_ref", "verification_kind", "verification_scope",
	"git_head", "diff_hash", "file", "symbol", "changed_files", "module_paths", "dependencies",
	"last_assistant_message", "response", "raw_output", "baron_payload_truncated",
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

func (r *Runtime) SetKnowledgeBackend(backend continuity.KnowledgeBackend, isolation contracts.IsolationContext) {
	if r == nil {
		return
	}
	r.knowledge = backend
	if r.isolation.ProjectID == "" {
		r.isolation = isolation
	}
}

func (r *Runtime) SetQueueOperationHandler(handler continuity.QueueOperationHandler) {
	if r == nil {
		return
	}
	r.operationHandler = handler
	if r.syncer == nil && r.store != nil && handler != nil {
		r.syncer = continuity.NewSyncer(r.store, r.backend, r.isolation, r.secrets)
	}
	if r.syncer != nil {
		r.syncer.SetQueueOperationHandler(handler)
	}
}

func (r *Runtime) SetRepositoryRoot(root string) {
	if r != nil {
		r.repositoryRoot = root
	}
}

func (r *Runtime) rebuildSyncer() {
	if r == nil || r.store == nil || (r.backend == nil && r.operationHandler == nil) {
		return
	}
	r.syncer = continuity.NewSyncer(r.store, r.backend, r.isolation, r.secrets)
	if r.operationHandler != nil {
		r.syncer.SetQueueOperationHandler(r.operationHandler)
	}
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
	if request.SessionID == "" {
		request.SessionID = payloadField(request.Payload, "session_id", "sessionId", "session")
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = payloadField(request.Payload, "idempotency_key", "idempotencyKey", "event_id", "eventId", "id")
		if request.IdempotencyKey == "" {
			request.IdempotencyKey = contracts.HashContent(string(request.Payload) + string(request.Client) + string(request.Event))
		}
	}
	if normalized, normalizeErr := normalizeLifecyclePayload(request.Payload, request.Event); normalizeErr != nil {
		return response, normalizeErr
	} else {
		// Compact before redaction so credential scanning never repeatedly walks
		// an unbounded tool transcript that will not be persisted.
		request.Payload = json.RawMessage(config.Redact(string(normalized), r.secrets))
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
	// DSH can flush a clean session before emitting its final assistant event.
	// That trailing event belongs to the already-closed session and must not
	// turn a verified completion back into an active/interrupted handoff.
	if request.Event == contracts.EventAssistantFinal && state.SessionID == request.SessionID && state.SessionState == contracts.SessionCleanClosed {
		sessionState = contracts.SessionCleanClosed
	}
	state.LastClient = request.Client
	state.SessionID = request.SessionID
	state.SessionState = sessionState
	if state.Task.Status == "" {
		state.Task.Status = contracts.TaskInProgress
	}
	applyEvidence(&state, request)
	r.syncTaskStateFromLedger(ctx, &state, request)
	if r.repositoryRoot != "" && (request.Event == contracts.EventSessionStarted || request.Event == contracts.EventFileChanged || request.Event == contracts.EventToolFinished || request.Event == contracts.EventTestFinished || request.Event == contracts.EventCheckpointUpdated) {
		if repository, repositoryErr := continuity.InspectRepository(ctx, r.repositoryRoot); repositoryErr == nil {
			state.Repository = repository
		}
	}
	var saveErr error
	if shouldMaterializeCheckpoint(request.Event) {
		saveErr = r.engine.Save(ctx, state)
	} else {
		saveErr = r.engine.SaveLocal(ctx, state)
	}
	if saveErr != nil {
		return response, saveErr
	}
	resumeDecision, ledgerTasks := r.resumeDecision(ctx, request, state, loadErr == nil)
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
	if shouldRecall(request.Event) {
		if handoff := r.latestOtherClientContext(ctx, request.ProjectID, request.Client); handoff != "" {
			if response.Context != "" {
				response.Context += "\n"
			}
			response.Context += handoff
			response.Historical = true
		}
		if resumeDecision.Outcome != continuity.ResumeRemoteRecoveryRequired || len(ledgerTasks) > 0 || r.backend != nil || r.knowledge != nil {
			if localContext := continuity.BuildLocalResumeContext(state, ledgerTasks, resumeDecision, 5000); localContext != "" {
				if response.Context != "" {
					response.Context += "\n"
				}
				response.Context += localContext
			}
		}
	}
	// The local event and checkpoint are durable before this point. Remote
	// capture/recall is deliberately best-effort so memory outages never stop
	// the provider session.
	r.captureMemory(ctx, request, state)
	r.captureContinuitySummaries(ctx, request, state)
	// Only lifecycle boundaries drain previously queued remote work. Tool hooks
	// are the hot path and must not turn every local event into a remote retry
	// attempt; meaningful task summaries flush their own newly queued records.
	if shouldFlushRemoteQueue(request.Event) {
		r.flushRemoteQueue(ctx)
	}
	if (r.backend != nil || r.knowledge != nil) && shouldRecall(request.Event) && resumeDecision.Outcome == continuity.ResumeRemoteRecoveryRequired {
		requestIsolation := r.isolation
		// Recall is project-scoped by design. The current session is the
		// consumer of historical context, not a filter for the previous
		// agent's records; applying it here would make cross-agent handoff
		// invisible immediately after a session switch.
		requestIsolation.SessionID = ""
		query := recallQuery(request, state)
		queryHash := recallQueryHash(query)
		packet, packetErr := r.cachedRemoteContext(ctx, request.SessionID, resumeDecision.RecoveryFingerprint, queryHash)
		if packetErr != nil {
			memoryCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
			packet, packetErr = continuity.BuildContextWithKnowledge(memoryCtx, state, r.backend, r.knowledge, requestIsolation, query, 5000, r.secrets)
			cancel()
			if packetErr == nil && packet.RemoteError == "" {
				if snapshot, marshalErr := json.Marshal(packet); marshalErr == nil {
					_ = r.store.PutRemoteRecallCache(ctx, storage.RemoteRecallCache{
						ProjectID: request.ProjectID, SessionID: request.SessionID,
						Fingerprint: resumeDecision.RecoveryFingerprint, QueryHash: queryHash,
						Snapshot: snapshot, ReceiptID: "search-" + queryHash,
					})
				}
			}
		}
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

func (r *Runtime) resumeDecision(ctx context.Context, request Request, state continuity.WorkState, localStateReadable bool) (continuity.ResumeDecision, []storage.TaskRecord) {
	if r == nil || r.store == nil {
		return continuity.EvaluateResumeGate(continuity.ResumeGateInput{LocalStateReadable: false}), nil
	}
	tasks, err := r.store.ListTasks(ctx, request.ProjectID, []contracts.TaskStatus{
		contracts.TaskPlanned, contracts.TaskInProgress, contracts.TaskBlocked,
		contracts.TaskFailed, contracts.TaskInterrupted,
	}, 100)
	ledgerReadable := err == nil
	return continuity.EvaluateResumeGate(continuity.ResumeGateInput{
		LocalStateReadable:        localStateReadable,
		LedgerReadable:            ledgerReadable,
		Repository:                state.Repository,
		UnresolvedTasks:           tasks,
		RequestedTask:             resumeTaskScope(request.Payload),
		HistoricalRecallRequested: payloadBool(request.Payload, "historical_recall", "remote_recall", "historical_request"),
	}), tasks
}

func (r *Runtime) syncTaskStateFromLedger(ctx context.Context, state *continuity.WorkState, request Request) {
	if r == nil || r.store == nil || state == nil {
		return
	}
	taskID := payloadField(request.Payload, "task_id")
	if contracts.IsTaskEvent(request.Event) || (taskID != "" && taskID == state.ActiveTaskID) {
		if taskID == "" {
			taskID = state.ActiveTaskID
		}
		if task, err := r.store.GetTask(ctx, request.ProjectID, taskID); err == nil {
			state.ActiveTaskID = task.TaskID
			state.Task.TaskID = task.TaskID
			state.Task.Goal = task.Goal
			state.Task.Status = task.Status
			state.Task.CurrentStep = task.CurrentStep
			state.Task.NextAction = task.NextAction
			state.Task.CompletionVerified = task.CompletionVerified
			state.Task.CompletionPolicy = task.CompletionPolicy
			state.Task.LatestVerificationEventID = task.LatestVerificationEventID
			state.Task.LatestVerificationKind = task.LatestVerificationKind
			state.Task.LatestVerificationScope = task.LatestVerificationScope
			state.Task.LatestErrorRef = task.LatestErrorRef
		}
	}
}

func (r *Runtime) cachedRemoteContext(ctx context.Context, sessionID, fingerprint, queryHash string) (continuity.ContextPacket, error) {
	if r == nil || r.store == nil {
		return continuity.ContextPacket{}, sql.ErrNoRows
	}
	cache, err := r.store.GetRemoteRecallCache(ctx, r.projectID, sessionID, fingerprint, queryHash)
	if err != nil {
		return continuity.ContextPacket{}, err
	}
	var packet continuity.ContextPacket
	if err := json.Unmarshal(cache.Snapshot, &packet); err != nil {
		return continuity.ContextPacket{}, fmt.Errorf("decode cached remote context: %w", err)
	}
	return packet, nil
}

func (r *Runtime) latestOtherClientContext(ctx context.Context, projectID string, currentClient contracts.HookClient) string {
	if r == nil || r.store == nil {
		return ""
	}
	event, err := r.store.LatestEventFromOtherClient(ctx, projectID, currentClient)
	if err != nil || len(event.Payload) == 0 {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal(event.Payload, &payload) != nil {
		return ""
	}
	content := ""
	for _, key := range []string{"response", "summary", "content", "message", "prompt", "decision"} {
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
		toolOutput, _ := payload["tool_output"].(string)
		if toolOutput == "" {
			toolOutput, _ = payload["error"].(string)
		}
		content = strings.TrimSpace(command + "\n" + toolOutput)
	}
	// A provider may close cleanly immediately after a failed command. In that
	// case the latest durable event can be only the short assistant sentence;
	// append the canonical checkpoint's test evidence so the next agent still
	// receives the real command, failure output, and exit code.
	if state, stateErr := r.engine.Load(ctx); stateErr == nil {
		evidence := latestTestHandoffEvidence(state.LatestTest)
		if evidence != "" && !strings.Contains(content, evidence) {
			content = strings.TrimSpace(content + "\n" + evidence)
		}
	}
	if content == "" {
		return ""
	}
	return fmt.Sprintf("<baron-local-handoff trust=\"local-authoritative\">\nPrevious agent checkpoint [%s/%s session=%s]: %s\n</baron-local-handoff>",
		html.EscapeString(string(event.Client)), html.EscapeString(string(event.Type)), html.EscapeString(event.SessionID), html.EscapeString(boundedText(content, 4000)))
}

func latestTestHandoffEvidence(test continuity.TestEvidence) string {
	if test.Command == "" && test.Summary == "" && test.ExitCode == nil {
		return ""
	}
	parts := make([]string, 0, 4)
	if test.Command != "" {
		parts = append(parts, "command="+boundedText(test.Command, 2048))
	}
	if test.Status != "" {
		parts = append(parts, "status="+boundedText(test.Status, 128))
	}
	if test.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit_code=%d", *test.ExitCode))
	}
	if test.Summary != "" {
		parts = append(parts, "evidence="+boundedText(test.Summary, 8192))
	}
	return "Latest canonical test evidence (rerun before trusting): " + strings.Join(parts, "; ")
}

func (r *Runtime) flushRemoteQueue(ctx context.Context) {
	if r == nil || r.syncer == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	flushCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	_, _ = r.syncer.Flush(flushCtx, 20)
}

func shouldFlushRemoteQueue(event contracts.EventType) bool {
	switch event {
	case contracts.EventSessionStarted, contracts.EventAssistantFinal,
		contracts.EventSessionCleanClose, contracts.EventSessionInterrupted,
		contracts.EventHandoffStarted, contracts.EventHandoffCompleted:
		return true
	default:
		return false
	}
}

func shouldMaterializeCheckpoint(event contracts.EventType) bool {
	switch event {
	case contracts.EventToolStarted, contracts.EventToolFinished,
		contracts.EventFileChanged, contracts.EventTestStarted,
		contracts.EventCheckpointUpdated:
		return false
	default:
		return true
	}
}

func (r *Runtime) captureMemory(ctx context.Context, request Request, state continuity.WorkState) {
	if r == nil || r.syncer == nil || r.backend == nil || !shouldCaptureRemote(request, state) {
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

func (r *Runtime) captureContinuitySummaries(ctx context.Context, request Request, state continuity.WorkState) {
	if r == nil || r.syncer == nil || r.operationHandler == nil || !shouldSyncSummary(request, state) {
		return
	}
	record, ok := memoryRecord(request, state)
	if !ok {
		return
	}
	payload := map[string]any{
		"session_id": request.SessionID, "client": request.Client, "event_type": request.Event,
		"summary": record.Content, "task_id": firstNonEmptyTaskID(state, request), "active_task_id": state.ActiveTaskID,
		"goal": boundedText(state.Task.Goal, 2048), "status": state.Task.Status,
		"current_step": boundedText(state.Task.CurrentStep, 1024), "next_action": boundedText(state.Task.NextAction, 1024),
		"completion_verified": state.Task.CompletionVerified, "completion_policy": state.Task.CompletionPolicy,
		"latest_verification_event_id": state.Task.LatestVerificationEventID,
		"latest_verification_kind":     state.Task.LatestVerificationKind, "latest_verification_scope": boundedText(state.Task.LatestVerificationScope, 512),
		"latest_error_ref": boundedText(state.Task.LatestErrorRef, 512), "evidence_refs": boundedValues(record.EvidenceRefs, 20, 512),
		"git_branch": boundedText(state.Repository.Branch, 256), "git_head": boundedText(state.Repository.GitHead, 128),
		"diff_hash": boundedText(state.Repository.DiffHash, 128), "changed_files": boundedValues(state.Repository.ChangedFiles, 20, 512),
		"latest_test": remoteTestEvidence(state.LatestTest),
	}
	keyBase := "summary-" + request.ProjectID + "-" + request.SessionID + "-" + record.ContentHash
	_, _ = r.syncer.QueueOperation(ctx, storage.QueueOperationCoreUpdate, keyBase+":core", payload)
	_, _ = r.syncer.QueueOperation(ctx, storage.QueueOperationScenarioUpdate, keyBase+":scenario", payload)
	flushCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	_, _ = r.syncer.Flush(flushCtx, 4)
	cancel()
}

func shouldSyncSummary(request Request, state continuity.WorkState) bool {
	switch request.Event {
	case contracts.EventTaskFailed, contracts.EventTaskBlocked, contracts.EventTaskInterrupted,
		contracts.EventSessionCleanClose, contracts.EventSessionInterrupted,
		contracts.EventHandoffStarted, contracts.EventHandoffCompleted, contracts.EventErrorObserved:
		return true
	case contracts.EventAssistantFinal:
		return payloadBool(request.Payload, "handoff", "explicit_handoff", "handoff_requested")
	case contracts.EventTaskVerified:
		return state.Task.CompletionVerified
	case contracts.EventTaskCompleted:
		return state.Task.Status == contracts.TaskCompleted && state.Task.CompletionVerified
	case contracts.EventTestFinished:
		return importantTestEvidence(request.Payload)
	case contracts.EventCheckpointUpdated:
		return payloadBool(request.Payload, "handoff", "explicit_handoff", "handoff_requested")
	default:
		return false
	}
}

func shouldCaptureRemote(request Request, state continuity.WorkState) bool {
	return shouldSyncSummary(request, state)
}

func importantTestEvidence(raw json.RawMessage) bool {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	if payloadBool(raw, "important", "verification", "acceptance", "build") {
		return true
	}
	if exitCode, ok := payload["exit_code"].(float64); ok && int(exitCode) != 0 {
		return true
	}
	if kind, ok := payload["verification_kind"].(string); ok && contracts.VerificationKind(kind).Valid() {
		return true
	}
	command, _ := payload["command"].(string)
	if command == "" {
		if toolInput, ok := payload["tool_input"].(map[string]any); ok {
			command, _ = toolInput["command"].(string)
		}
	}
	return strings.Contains(strings.ToLower(command), "build")
}

func firstNonEmptyTaskID(state continuity.WorkState, request Request) string {
	if state.Task.TaskID != "" {
		return boundedText(state.Task.TaskID, 256)
	}
	if state.ActiveTaskID != "" {
		return boundedText(state.ActiveTaskID, 256)
	}
	return payloadField(request.Payload, "task_id")
}

func remoteTestEvidence(test continuity.TestEvidence) map[string]any {
	result := map[string]any{}
	if test.Command != "" {
		result["command"] = boundedText(test.Command, 512)
	}
	if test.Status != "" {
		result["status"] = boundedText(test.Status, 64)
	}
	if test.Summary != "" {
		result["summary"] = boundedText(test.Summary, 1024)
	}
	if test.ExitCode != nil {
		result["exit_code"] = *test.ExitCode
	}
	if test.ObservedAt != "" {
		result["observed_at"] = boundedText(test.ObservedAt, 64)
	}
	return result
}

func shouldRecall(event contracts.EventType) bool {
	return event == contracts.EventSessionStarted || event == contracts.EventUserPrompt || event == contracts.EventCheckpointUpdated
}

func recallQuery(request Request, state continuity.WorkState) contracts.MemoryQuery {
	// Prefer the incoming prompt as the remote retrieval query. Tencent's
	// semantic search ranks the top records for the whole query; appending a
	// large repository/task fingerprint can push the exact handoff record out
	// of the result set even when the prompt contains its identifier.
	terms := make([]string, 0, 16+len(state.Repository.ChangedFiles)+len(state.Errors)*2)
	var payload map[string]any
	payloadTerms := make([]string, 0, 9)
	hasPrompt := false
	if json.Unmarshal(request.Payload, &payload) == nil {
		for _, key := range []string{"prompt", "text", "message", "summary", "command", "file", "symbol", "test", "error"} {
			if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
				payloadTerms = append(payloadTerms, value)
				if key == "prompt" || key == "text" || key == "message" {
					hasPrompt = true
				}
			}
		}
	}
	if hasPrompt {
		terms = append(terms, payloadTerms...)
	} else {
		terms = append(terms, state.Task.Goal, state.Task.CurrentStep, state.Task.NextAction, state.Repository.Branch,
			state.Repository.GitHead, state.LatestTest.Command, state.LatestTest.Summary)
		terms = append(terms, payloadTerms...)
		for _, file := range boundedValues(state.Repository.ChangedFiles, 8, 1024) {
			terms = append(terms, file)
		}
		for index, evidence := range state.Errors {
			if index == 8 {
				break
			}
			terms = append(terms, evidence.Class, evidence.Summary)
		}
	}
	files := append([]string(nil), state.Repository.ChangedFiles...)
	symbols := []string(nil)
	if payload != nil {
		for _, key := range []string{"prompt", "text", "message", "summary", "command", "file", "symbol", "test", "error"} {
			if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
				if key == "file" {
					files = append(files, value)
				}
				if key == "symbol" {
					symbols = append(symbols, value)
				}
			}
		}
	}
	kinds := []string(nil)
	if request.Event == contracts.EventSessionStarted {
		kinds = []string{"session_start"}
	}
	if len(symbols) > 0 {
		kinds = append(kinds, "symbol_change")
	}
	return contracts.MemoryQuery{Text: boundedText(strings.Join(terms, " "), 4000), Limit: 10, Kinds: kinds,
		Files: boundedValues(files, 8, 1024), Symbols: boundedValues(symbols, 4, 512)}
}

func recallQueryHash(query contracts.MemoryQuery) string {
	data, err := json.Marshal(query)
	if err != nil {
		return contracts.HashContent(query.Text)
	}
	return contracts.HashContent(string(data))
}

func resumeTaskScope(raw json.RawMessage) continuity.ResumeTaskScope {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return continuity.ResumeTaskScope{}
	}
	scope := continuity.ResumeTaskScope{
		TaskID:        payloadString(payload, "task_id"),
		Goal:          payloadString(payload, "goal"),
		ChangedFiles:  payloadStrings(payload, "changed_files"),
		ModulePaths:   payloadStrings(payload, "module_paths"),
		Dependencies:  payloadStrings(payload, "dependencies"),
		GitHead:       payloadString(payload, "git_head"),
		DiffHash:      payloadString(payload, "diff_hash"),
		ExplicitScope: payloadBool(raw, "task_scope_explicit"),
	}
	if file := payloadString(payload, "file"); file != "" {
		scope.ChangedFiles = append(scope.ChangedFiles, file)
	}
	if module := payloadString(payload, "module_path"); module != "" {
		scope.ModulePaths = append(scope.ModulePaths, module)
	}
	return scope
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return boundedText(value, 2048)
}

func payloadStrings(payload map[string]any, key string) []string {
	values, _ := payload[key].([]any)
	result := make([]string, 0, len(values))
	for _, item := range values {
		if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
			result = append(result, boundedText(value, 1024))
		}
	}
	return result
}

func payloadBool(raw json.RawMessage, keys ...string) bool {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	for _, key := range keys {
		if value, ok := payload[key].(bool); ok && value {
			return true
		}
	}
	return false
}

func boundedValues(values []string, limit, maxLength int) []string {
	result := make([]string, 0, minInt(len(values), limit))
	seen := make(map[string]bool)
	for _, value := range values {
		value = boundedText(value, maxLength)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
		if len(result) == limit {
			break
		}
	}
	return result
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
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
	if strings.TrimSpace(content) == "" && contracts.IsTaskEvent(request.Event) {
		content = fmt.Sprintf("task transition: task_id=%s; status=%s; goal=%s; current_step=%s; next_action=%s; verification=%s/%s; completion_verified=%t; error_ref=%s",
			firstNonEmptyTaskID(state, request), state.Task.Status, state.Task.Goal, state.Task.CurrentStep, state.Task.NextAction,
			state.Task.LatestVerificationKind, state.Task.LatestVerificationScope, state.Task.CompletionVerified, state.Task.LatestErrorRef)
	}
	if strings.TrimSpace(content) == "" && (request.Event == contracts.EventSessionCleanClose || request.Event == contracts.EventSessionInterrupted || request.Event == contracts.EventCheckpointUpdated || request.Event == contracts.EventHandoffStarted || request.Event == contracts.EventHandoffCompleted) {
		content = fmt.Sprintf("continuity checkpoint: goal=%s; status=%s; current_step=%s; next_action=%s; last_test=%s (%s)",
			state.Task.Goal, state.Task.Status, state.Task.CurrentStep, state.Task.NextAction, state.LatestTest.Command, state.LatestTest.Status)
	}
	if strings.TrimSpace(content) == "" {
		return contracts.MemoryRecord{}, false
	}
	metadata := map[string]string{"event_type": string(request.Event), "task_status": string(state.Task.Status)}
	if taskID := firstNonEmptyTaskID(state, request); taskID != "" {
		metadata["task_id"] = taskID
	}
	if state.Repository.Branch != "" {
		metadata["git_branch"] = boundedText(state.Repository.Branch, 256)
	}
	if state.Repository.GitHead != "" {
		metadata["git_head"] = boundedText(state.Repository.GitHead, 128)
	}
	if state.LatestTest.Command != "" {
		metadata["latest_test_command"] = state.LatestTest.Command
	}
	for _, key := range []string{"symbol", "test", "class"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			metadata[key] = boundedText(value, 512)
		}
	}
	if exitCode, ok := payload["exit_code"].(float64); ok {
		metadata["exit_code"] = fmt.Sprintf("%d", int(exitCode))
	}
	evidenceRefs := append([]string(nil), state.Repository.ChangedFiles...)
	if file, ok := payload["file"].(string); ok && strings.TrimSpace(file) != "" {
		evidenceRefs = append(evidenceRefs, file)
	}
	if len(evidenceRefs) > 20 {
		evidenceRefs = evidenceRefs[len(evidenceRefs)-20:]
	}
	record := contracts.MemoryRecord{
		ProjectID: request.ProjectID, SourceClient: request.Client, SessionID: request.SessionID,
		Kind: string(request.Event), Content: boundedText(content, 4096), Metadata: metadata, EvidenceRefs: evidenceRefs,
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
	if completionVerified && !contracts.IsTaskEvent(request.Event) {
		state.Task.CompletionVerified = true
	}
	if taskStatus, ok := payload["task_status"].(string); ok {
		switch contracts.TaskStatus(taskStatus) {
		case contracts.TaskPlanned, contracts.TaskInProgress, contracts.TaskBlocked, contracts.TaskFailed, contracts.TaskInterrupted:
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
	if request.Event == contracts.EventAssistantFinal && state.LatestTest.Command != "" {
		// Codex reports the command result and the human-readable exit code in
		// separate lifecycle events. Complete the existing test evidence without
		// replacing the richer tool stderr with the short final sentence.
		if status != "" {
			state.LatestTest.Status = status
		}
		if exitCode != nil {
			state.LatestTest.ExitCode = exitCode
		}
		if state.LatestTest.Summary == "" {
			state.LatestTest.Summary = summary
		}
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

// normalizeLifecyclePayload keeps the original upstream fields but adds the
// canonical evidence names Baron uses internally. Codex's official lifecycle
// payload currently calls the tool result `tool_response` and the final text
// `last_assistant_message`; without this bridge a live handoff can persist an
// event while losing the command output, failure status, or next-agent text.
func normalizeLifecyclePayload(raw json.RawMessage, event contracts.EventType) (json.RawMessage, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return raw, err
	}
	if payload == nil {
		return raw, nil
	}
	if _, ok := payload["exit_code"]; !ok {
		for _, key := range []string{"last_assistant_message", "response", "tool_response", "tool_output"} {
			if text, ok := payload[key].(string); ok {
				if exitCode, found := exitCodeFromText(text); found {
					payload["exit_code"] = exitCode
					break
				}
			}
		}
	}
	if _, ok := payload["status"]; !ok {
		if exitCode, ok := lifecycleExitCode(payload["exit_code"]); ok && exitCode != 0 {
			payload["status"] = "failed"
		} else {
			for _, key := range []string{"tool_response", "tool_output"} {
				if output, ok := payload[key].(string); ok && looksLikeToolFailure(output) {
					payload["status"] = "failed"
					break
				}
			}
		}
	}
	compacted, truncated := compactLifecyclePayload(payload)
	payload = compacted
	if _, ok := payload["command"]; !ok {
		if toolInput, ok := payload["tool_input"].(map[string]any); ok {
			if command, ok := toolInput["command"].(string); ok && strings.TrimSpace(command) != "" {
				payload["command"] = command
			}
		}
	}
	if _, ok := payload["tool_output"]; !ok {
		if toolResponse, ok := payload["tool_response"]; ok {
			payload["tool_output"] = toolResponse
		}
	}
	if _, ok := payload["response"]; !ok {
		if message, ok := payload["last_assistant_message"].(string); ok && strings.TrimSpace(message) != "" {
			payload["response"] = message
		}
	}
	if _, ok := payload["summary"]; !ok {
		if event == contracts.EventAssistantFinal {
			if message, ok := payload["last_assistant_message"].(string); ok && strings.TrimSpace(message) != "" {
				payload["summary"] = message
			} else if response, ok := payload["response"].(string); ok && strings.TrimSpace(response) != "" {
				payload["summary"] = response
			}
		} else if output, ok := payload["tool_output"].(string); ok && strings.TrimSpace(output) != "" {
			payload["summary"] = output
		}
	}
	if payload["status"] == "failed" {
		if _, ok := payload["error"]; !ok {
			if output, ok := payload["tool_output"].(string); ok && strings.TrimSpace(output) != "" {
				payload["error"] = output
			}
		}
	}
	if truncated {
		payload["baron_payload_truncated"] = true
	}
	normalized, err := marshalLifecyclePayload(payload)
	if err != nil {
		return raw, err
	}
	return normalized, nil
}

func compactLifecyclePayload(payload map[string]any) (map[string]any, bool) {
	return compactLifecycleMap(payload, maxLifecycleStringBytes, maxLifecycleMapKeys, maxLifecycleArrayItems, 0)
}

func compactLifecycleMap(payload map[string]any, stringLimit, mapLimit, arrayLimit, depth int) (map[string]any, bool) {
	keys, truncated := lifecycleMapKeys(payload, mapLimit)
	result := make(map[string]any, len(keys))
	for _, key := range keys {
		value, valueTruncated := compactLifecycleValue(payload[key], key, stringLimit, mapLimit, arrayLimit, depth)
		result[key] = value
		truncated = truncated || valueTruncated
	}
	return result, truncated
}

func lifecycleMapKeys(payload map[string]any, limit int) ([]string, bool) {
	if limit <= 0 {
		return nil, len(payload) > 0
	}
	allKeys := make([]string, 0, len(payload))
	for key := range payload {
		allKeys = append(allKeys, key)
	}
	sort.Strings(allKeys)
	if len(allKeys) <= limit {
		return allKeys, false
	}
	keys := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, key := range lifecyclePayloadPriority {
		if _, ok := payload[key]; !ok {
			continue
		}
		keys = append(keys, key)
		seen[key] = struct{}{}
		if len(keys) == limit {
			return keys, true
		}
	}
	for _, key := range allKeys {
		if _, ok := seen[key]; ok {
			continue
		}
		keys = append(keys, key)
		if len(keys) == limit {
			break
		}
	}
	return keys, true
}

func compactLifecycleValue(value any, key string, stringLimit, mapLimit, arrayLimit, depth int) (any, bool) {
	switch value := value.(type) {
	case string:
		return boundedLifecycleText(value, lifecycleStringLimit(key, stringLimit))
	case map[string]any:
		if depth >= maxLifecycleNestingDepth {
			return "[nested payload omitted]", true
		}
		return compactLifecycleMap(value, stringLimit, mapLimit, arrayLimit, depth+1)
	case []any:
		if depth >= maxLifecycleNestingDepth {
			return "[nested payload omitted]", true
		}
		truncated := len(value) > arrayLimit
		if truncated {
			value = value[:arrayLimit]
		}
		result := make([]any, 0, len(value))
		for _, item := range value {
			compacted, itemTruncated := compactLifecycleValue(item, key, stringLimit, mapLimit, arrayLimit, depth+1)
			result = append(result, compacted)
			truncated = truncated || itemTruncated
		}
		return result, truncated
	default:
		return value, false
	}
}

func lifecycleStringLimit(key string, fallback int) int {
	switch key {
	case "command", "tool_name":
		return 4096
	case "goal":
		return 4096
	case "tool_output", "tool_response", "last_assistant_message", "response", "summary", "error", "raw_output":
		return maxLifecycleStringBytes
	case "current_step", "last_successful_step", "next_action":
		return 2048
	case "task_id", "active_task_id", "session_id", "event_id", "idempotency_key", "verification_ref", "git_head", "diff_hash":
		return 512
	default:
		return fallback
	}
}

func boundedLifecycleText(value string, max int) (string, bool) {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value, false
	}
	const marker = "...[truncated]"
	if max <= len(marker) {
		return value[:max], true
	}
	available := max - len(marker)
	head := available / 2
	tail := available - head
	return value[:head] + marker + value[len(value)-tail:], true
}

func lifecycleExitCode(value any) (int, bool) {
	switch value := value.(type) {
	case int:
		return value, true
	case float64:
		return int(value), value == float64(int(value))
	default:
		return 0, false
	}
}

func marshalLifecyclePayload(payload map[string]any) ([]byte, error) {
	normalized, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(normalized) <= maxHookPayloadBytes {
		return normalized, nil
	}
	// A pathological upstream object can still exceed the per-field limits.
	// Recompact it with a smaller shape before dropping any canonical field.
	tight, _ := compactLifecycleMap(payload, 4096, 16, 32, 0)
	tight["baron_payload_truncated"] = true
	normalized, err = json.Marshal(tight)
	if err != nil {
		return nil, err
	}
	if len(normalized) <= maxHookPayloadBytes {
		return normalized, nil
	}
	return marshalMinimalLifecyclePayload(tight)
}

func marshalMinimalLifecyclePayload(payload map[string]any) ([]byte, error) {
	minimal := make(map[string]any)
	for _, key := range lifecyclePayloadPriority {
		if value, ok := payload[key]; ok {
			candidate := make(map[string]any, len(minimal)+1)
			for existingKey, existingValue := range minimal {
				candidate[existingKey] = existingValue
			}
			candidate[key] = value
			data, err := json.Marshal(candidate)
			if err != nil {
				return nil, err
			}
			if len(data) <= maxHookPayloadBytes {
				minimal = candidate
			}
		}
	}
	return json.Marshal(minimal)
}

func looksLikeToolFailure(value string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	return strings.HasPrefix(trimmed, "failed") || strings.HasPrefix(trimmed, "error:") || strings.Contains(trimmed, "\nerror:")
}

func exitCodeFromText(value string) (int, bool) {
	lower := strings.ToLower(value)
	for _, marker := range []string{"exit code", "exit_code"} {
		index := strings.Index(lower, marker)
		if index < 0 {
			continue
		}
		index += len(marker)
		for index < len(value) && (value[index] == ' ' || value[index] == '\t' || value[index] == ':' || value[index] == '=') {
			index++
		}
		start := index
		if index < len(value) && value[index] == '-' {
			index++
		}
		for index < len(value) && value[index] >= '0' && value[index] <= '9' {
			index++
		}
		if index == start || (value[start] == '-' && index == start+1) {
			continue
		}
		var result int
		if _, err := fmt.Sscanf(value[start:index], "%d", &result); err == nil {
			return result, true
		}
	}
	return 0, false
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
