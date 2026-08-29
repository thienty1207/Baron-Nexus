package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

var taskLedgerSchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS tasks (
		project_id TEXT NOT NULL REFERENCES projects(project_id) ON DELETE CASCADE,
		task_id TEXT NOT NULL,
		goal TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'in_progress',
		current_step TEXT NOT NULL DEFAULT '',
		next_action TEXT NOT NULL DEFAULT '',
		source_client TEXT NOT NULL DEFAULT '',
		last_session_id TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		git_head TEXT NOT NULL DEFAULT '',
		diff_hash TEXT NOT NULL DEFAULT '',
		completion_verified INTEGER NOT NULL DEFAULT 0,
		completion_policy TEXT NOT NULL DEFAULT 'completion',
		latest_verification_event_id TEXT NOT NULL DEFAULT '',
		latest_verification_ref TEXT NOT NULL DEFAULT '',
		latest_verification_kind TEXT NOT NULL DEFAULT '',
		latest_verification_scope TEXT NOT NULL DEFAULT '',
		latest_error_event_id TEXT NOT NULL DEFAULT '',
		latest_error_ref TEXT NOT NULL DEFAULT '',
		legacy INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(project_id, task_id)
	)`,
	`CREATE INDEX IF NOT EXISTS tasks_project_status ON tasks(project_id, status, updated_at)`,
	`CREATE INDEX IF NOT EXISTS tasks_project_task ON tasks(project_id, task_id)`,
	`CREATE TABLE IF NOT EXISTS task_files (
		project_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		path TEXT NOT NULL,
		strength TEXT NOT NULL DEFAULT 'strong',
		PRIMARY KEY(project_id, task_id, path),
		FOREIGN KEY(project_id, task_id) REFERENCES tasks(project_id, task_id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS task_files_path ON task_files(project_id, path, strength)`,
	`CREATE TABLE IF NOT EXISTS task_modules (
		project_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		module_path TEXT NOT NULL,
		PRIMARY KEY(project_id, task_id, module_path),
		FOREIGN KEY(project_id, task_id) REFERENCES tasks(project_id, task_id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS task_modules_path ON task_modules(project_id, module_path)`,
	`CREATE TABLE IF NOT EXISTS task_dependencies (
		project_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		dependency TEXT NOT NULL,
		PRIMARY KEY(project_id, task_id, dependency),
		FOREIGN KEY(project_id, task_id) REFERENCES tasks(project_id, task_id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS task_dependencies_name ON task_dependencies(project_id, dependency)`,
	`CREATE TABLE IF NOT EXISTS task_verifications (
		verification_id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		event_id TEXT NOT NULL,
		verification_ref TEXT NOT NULL,
		verification_kind TEXT NOT NULL,
		verification_scope TEXT NOT NULL,
		git_head TEXT NOT NULL,
		diff_hash TEXT NOT NULL,
		exit_code INTEGER,
		command TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		observed_at TEXT NOT NULL,
		created_at TEXT NOT NULL,
		UNIQUE(project_id, task_id, verification_ref),
		FOREIGN KEY(project_id, task_id) REFERENCES tasks(project_id, task_id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS task_verifications_task_time ON task_verifications(project_id, task_id, observed_at)`,
	`CREATE TABLE IF NOT EXISTS active_tasks (
		project_id TEXT PRIMARY KEY REFERENCES projects(project_id) ON DELETE CASCADE,
		session_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(project_id, task_id) REFERENCES tasks(project_id, task_id) ON DELETE CASCADE
	)`,
}

type TaskRecord struct {
	ProjectID                 string
	TaskID                    string
	Goal                      string
	Status                    contracts.TaskStatus
	CurrentStep               string
	NextAction                string
	SourceClient              contracts.HookClient
	LastSessionID             string
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	GitHead                   string
	DiffHash                  string
	ChangedFiles              []string
	FileEvidence              []TaskFileEvidence
	ModulePaths               []string
	Dependencies              []string
	CompletionVerified        bool
	CompletionPolicy          contracts.CompletionPolicy
	LatestVerificationEventID string
	LatestVerificationRef     string
	LatestVerificationKind    contracts.VerificationKind
	LatestVerificationScope   string
	LatestErrorEventID        string
	LatestErrorRef            string
	Legacy                    bool
}

type TaskFileEvidence struct {
	Path     string
	Strength string
}

type TaskVerification struct {
	VerificationID  string
	ProjectID       string
	TaskID          string
	EventID         string
	VerificationRef string
	Kind            contracts.VerificationKind
	Scope           string
	GitHead         string
	DiffHash        string
	ExitCode        *int
	Command         string
	Summary         string
	ObservedAt      time.Time
}

type sqlQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type parsedTaskPayload struct {
	TaskID string

	Goal                 string
	HasGoal              bool
	Status               contracts.TaskStatus
	HasStatus            bool
	CurrentStep          string
	HasCurrentStep       bool
	NextAction           string
	HasNextAction        bool
	SourceClient         contracts.HookClient
	HasSourceClient      bool
	SessionID            string
	HasSessionID         bool
	GitHead              string
	HasGitHead           bool
	DiffHash             string
	HasDiffHash          bool
	ChangedFiles         []string
	HasChangedFiles      bool
	ModulePaths          []string
	HasModulePaths       bool
	Dependencies         []string
	HasDependencies      bool
	VerificationRef      string
	HasVerificationRef   bool
	VerificationKind     contracts.VerificationKind
	HasVerificationKind  bool
	VerificationScope    string
	HasVerificationScope bool
	LatestErrorRef       string
	HasLatestErrorRef    bool
	CompletionPolicy     contracts.CompletionPolicy
	HasCompletionPolicy  bool
	ExitCode             *int
	HasExitCode          bool
	Command              string
	Summary              string
}

func (s *Store) insertTaskEvent(ctx context.Context, event Event) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	payload, err := parseTaskPayload(event)
	if err != nil {
		return false, err
	}
	prepareEvent(&event)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin task event transaction: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO events(event_id, project_id, session_id, client, event_type, occurred_at, payload, payload_hash, idempotency_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, idempotency_key) DO NOTHING`,
		event.EventID, event.ProjectID, event.SessionID, event.Client, event.Type,
		event.OccurredAt.UTC().Format(time.RFC3339Nano), []byte(event.Payload), event.PayloadHash,
		event.IdempotencyKey, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("insert task event: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows == 0 {
		return false, nil
	}
	if err := projectTaskEvent(ctx, tx, event, payload); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit task event transaction: %w", err)
	}
	return true, nil
}

func prepareEvent(event *Event) {
	if event.EventID == "" {
		event.EventID = newID("evt")
	}
	if event.IdempotencyKey == "" {
		event.IdempotencyKey = event.EventID
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if len(event.Payload) == 0 {
		event.Payload = json.RawMessage(`{}`)
	}
	if event.PayloadHash == "" {
		event.PayloadHash = contracts.HashContent(string(event.Payload))
	}
}

func parseTaskPayload(event Event) (parsedTaskPayload, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(event.Payload, &raw); err != nil {
		return parsedTaskPayload{}, fmt.Errorf("decode task event payload: %w", err)
	}
	result := parsedTaskPayload{}
	result.TaskID = stringField(raw, "task_id")
	if strings.TrimSpace(result.TaskID) == "" {
		return result, errors.New("task event requires task_id")
	}
	var err error
	if result.Goal, result.HasGoal, err = optionalStringField(raw, "goal"); err != nil {
		return result, err
	}
	result.Status, result.HasStatus, err = optionalStatusField(raw)
	if err != nil {
		return result, err
	}
	if result.CurrentStep, result.HasCurrentStep, err = optionalStringField(raw, "current_step"); err != nil {
		return result, err
	}
	if result.NextAction, result.HasNextAction, err = optionalStringField(raw, "next_action"); err != nil {
		return result, err
	}
	var source string
	if source, result.HasSourceClient, err = optionalStringField(raw, "source_client"); err != nil {
		return result, err
	}
	result.SourceClient = contracts.HookClient(source)
	if result.SessionID, result.HasSessionID, err = optionalStringField(raw, "session_id"); err != nil {
		return result, err
	}
	if result.GitHead, result.HasGitHead, err = optionalStringField(raw, "git_head"); err != nil {
		return result, err
	}
	if result.DiffHash, result.HasDiffHash, err = optionalStringField(raw, "diff_hash"); err != nil {
		return result, err
	}
	if result.ChangedFiles, result.HasChangedFiles, err = optionalStringSliceField(raw, "changed_files"); err != nil {
		return result, err
	}
	if result.ModulePaths, result.HasModulePaths, err = optionalStringSliceField(raw, "module_paths"); err != nil {
		return result, err
	}
	if result.Dependencies, result.HasDependencies, err = optionalStringSliceField(raw, "dependencies"); err != nil {
		return result, err
	}
	if result.VerificationRef, result.HasVerificationRef, err = optionalStringField(raw, "verification_ref"); err != nil {
		return result, err
	}
	var kind string
	if kind, result.HasVerificationKind, err = optionalStringField(raw, "verification_kind"); err != nil {
		return result, err
	}
	result.VerificationKind = contracts.VerificationKind(kind)
	if result.HasVerificationKind && !result.VerificationKind.Valid() {
		return result, fmt.Errorf("unsupported verification_kind %q", kind)
	}
	if result.VerificationScope, result.HasVerificationScope, err = optionalStringField(raw, "verification_scope"); err != nil {
		return result, err
	}
	if result.LatestErrorRef, result.HasLatestErrorRef, err = optionalStringField(raw, "latest_error_ref"); err != nil {
		return result, err
	}
	var policy string
	if policy, result.HasCompletionPolicy, err = optionalStringField(raw, "completion_policy"); err != nil {
		return result, err
	}
	result.CompletionPolicy = contracts.CompletionPolicy(policy)
	if result.HasCompletionPolicy && !result.CompletionPolicy.Valid() {
		return result, fmt.Errorf("unsupported completion_policy %q", policy)
	}
	if rawExit, ok := raw["exit_code"]; ok {
		var exitCode int
		if err := json.Unmarshal(rawExit, &exitCode); err != nil {
			return result, fmt.Errorf("decode exit_code: %w", err)
		}
		result.ExitCode = &exitCode
		result.HasExitCode = true
	}
	if result.Command, _, err = optionalStringField(raw, "command"); err != nil {
		return result, err
	}
	if result.Summary, _, err = optionalStringField(raw, "summary"); err != nil {
		return result, err
	}
	if event.Type == contracts.EventTaskVerified {
		if !result.HasVerificationRef || strings.TrimSpace(result.VerificationRef) == "" {
			return result, errors.New("task_verified requires verification_ref")
		}
		if !result.HasVerificationKind || !result.VerificationKind.Valid() {
			return result, errors.New("task_verified requires verification_kind")
		}
		if !result.HasVerificationScope || strings.TrimSpace(result.VerificationScope) == "" {
			return result, errors.New("task_verified requires verification_scope")
		}
		if !result.HasGitHead || strings.TrimSpace(result.GitHead) == "" || !result.HasDiffHash || strings.TrimSpace(result.DiffHash) == "" {
			return result, errors.New("task_verified requires git_head and diff_hash")
		}
		if result.HasExitCode && result.ExitCode != nil && *result.ExitCode != 0 {
			return result, errors.New("task_verified requires successful verification evidence")
		}
	}
	return result, nil
}

func projectTaskEvent(ctx context.Context, tx *sql.Tx, event Event, payload parsedTaskPayload) error {
	task, err := loadTask(ctx, tx, event.ProjectID, payload.TaskID)
	known := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := event.OccurredAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	switch event.Type {
	case contracts.EventTaskStarted:
		if known && task.Status == contracts.TaskCompleted {
			return fmt.Errorf("completed task %q cannot be reopened by task_started", payload.TaskID)
		}
		if !known {
			task = TaskRecord{ProjectID: event.ProjectID, TaskID: payload.TaskID, CreatedAt: now,
				CompletionPolicy: contracts.CompletionPolicyCompletion, SourceClient: event.Client,
				LastSessionID: event.SessionID}
		}
		if payload.HasStatus && payload.Status != contracts.TaskPlanned && payload.Status != contracts.TaskInProgress {
			return fmt.Errorf("task_started status must be planned or in_progress, got %q", payload.Status)
		}
		if payload.HasStatus {
			task.Status = payload.Status
		} else {
			task.Status = contracts.TaskInProgress
		}
		applyTaskFields(&task, payload, event)
		if !known && task.Status == "" {
			task.Status = contracts.TaskInProgress
		}
		if payload.HasCompletionPolicy {
			task.CompletionPolicy = payload.CompletionPolicy
		}
		if task.CompletionPolicy == "" {
			task.CompletionPolicy = contracts.CompletionPolicyCompletion
		}
		if err := writeTask(ctx, tx, task, now); err != nil {
			return err
		}
		if err := replaceTaskScope(ctx, tx, task.ProjectID, task.TaskID, payload); err != nil {
			return err
		}
		return setActiveTask(ctx, tx, event.ProjectID, firstNonEmpty(payload.SessionID, event.SessionID), payload.TaskID, now)

	case contracts.EventTaskUpdated:
		if !known {
			return fmt.Errorf("task_updated requires existing task_id %q", payload.TaskID)
		}
		if payload.HasStatus && payload.Status == contracts.TaskCompleted {
			return errors.New("task_updated cannot promote a task to completed")
		}
		applyTaskFields(&task, payload, event)
		return writeTaskAndScope(ctx, tx, task, payload, now)

	case contracts.EventTaskFailed, contracts.EventTaskBlocked, contracts.EventTaskInterrupted:
		if !known {
			return fmt.Errorf("%s requires existing task_id %q", event.Type, payload.TaskID)
		}
		switch event.Type {
		case contracts.EventTaskFailed:
			task.Status = contracts.TaskFailed
		case contracts.EventTaskBlocked:
			task.Status = contracts.TaskBlocked
		case contracts.EventTaskInterrupted:
			task.Status = contracts.TaskInterrupted
		}
		task.CompletionVerified = false
		applyTaskFields(&task, payload, event)
		if !payload.HasLatestErrorRef {
			payload.LatestErrorRef = event.EventID
		}
		task.LatestErrorEventID = event.EventID
		task.LatestErrorRef = payload.LatestErrorRef
		return writeTaskAndScope(ctx, tx, task, payload, now)

	case contracts.EventTaskVerified:
		if !known {
			return fmt.Errorf("task_verified requires existing task_id %q", payload.TaskID)
		}
		if exists, err := verificationEvidenceExists(ctx, tx, event.ProjectID, payload.VerificationRef); err != nil {
			return err
		} else if !exists {
			return fmt.Errorf("verification_ref %q does not reference local evidence", payload.VerificationRef)
		}
		if task.GitHead != "" && task.GitHead != payload.GitHead {
			return errors.New("verification git_head does not match current task evidence")
		}
		if task.DiffHash != "" && task.DiffHash != payload.DiffHash {
			return errors.New("verification diff_hash does not match current task evidence")
		}
		verification := TaskVerification{
			VerificationID: event.EventID, ProjectID: event.ProjectID, TaskID: payload.TaskID,
			EventID: event.EventID, VerificationRef: payload.VerificationRef,
			Kind: payload.VerificationKind, Scope: payload.VerificationScope,
			GitHead: payload.GitHead, DiffHash: payload.DiffHash, ExitCode: payload.ExitCode,
			Command: payload.Command, Summary: payload.Summary, ObservedAt: now,
		}
		if err := insertTaskVerification(ctx, tx, verification); err != nil {
			return err
		}
		task.GitHead = payload.GitHead
		task.DiffHash = payload.DiffHash
		task.LatestVerificationEventID = event.EventID
		task.LatestVerificationRef = payload.VerificationRef
		task.LatestVerificationKind = payload.VerificationKind
		task.LatestVerificationScope = payload.VerificationScope
		task.CompletionVerified = task.CompletionPolicy.Allows(payload.VerificationKind)
		return writeTask(ctx, tx, task, now)

	case contracts.EventTaskCompleted:
		if !known {
			return fmt.Errorf("task_completed requires existing task_id %q", payload.TaskID)
		}
		verification, verificationErr := latestMatchingVerification(ctx, tx, task, payload.VerificationRef)
		if verificationErr != nil || !task.CompletionPolicy.Allows(verification.Kind) ||
			verification.GitHead == "" || verification.DiffHash == "" ||
			verification.GitHead != task.GitHead || verification.DiffHash != task.DiffHash ||
			(payload.HasGitHead && payload.GitHead != task.GitHead) ||
			(payload.HasDiffHash && payload.DiffHash != task.DiffHash) {
			return nil
		}
		task.Status = contracts.TaskCompleted
		task.CompletionVerified = true
		return writeTask(ctx, tx, task, now)
	default:
		return fmt.Errorf("unsupported canonical task event %q", event.Type)
	}
}

func applyTaskFields(task *TaskRecord, payload parsedTaskPayload, event Event) {
	if payload.HasGoal {
		task.Goal = boundedTaskText(payload.Goal, 4096)
	}
	if payload.HasCurrentStep {
		task.CurrentStep = boundedTaskText(payload.CurrentStep, 2048)
	}
	if payload.HasNextAction {
		task.NextAction = boundedTaskText(payload.NextAction, 2048)
	}
	if payload.HasSourceClient {
		task.SourceClient = payload.SourceClient
	} else if task.SourceClient == "" {
		task.SourceClient = event.Client
	}
	if payload.HasSessionID {
		task.LastSessionID = boundedTaskText(payload.SessionID, 256)
	} else if task.LastSessionID == "" {
		task.LastSessionID = event.SessionID
	}
	if payload.HasGitHead {
		task.GitHead = boundedTaskText(payload.GitHead, 128)
	}
	if payload.HasDiffHash {
		task.DiffHash = boundedTaskText(payload.DiffHash, 128)
	}
	if payload.HasStatus {
		task.Status = payload.Status
	}
	if task.CompletionPolicy == "" {
		task.CompletionPolicy = contracts.CompletionPolicyCompletion
	}
}

func writeTaskAndScope(ctx context.Context, tx *sql.Tx, task TaskRecord, payload parsedTaskPayload, now time.Time) error {
	if err := writeTask(ctx, tx, task, now); err != nil {
		return err
	}
	return replaceTaskScope(ctx, tx, task.ProjectID, task.TaskID, payload)
}

func writeTask(ctx context.Context, tx *sql.Tx, task TaskRecord, now time.Time) error {
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() || now.After(task.UpdatedAt) {
		task.UpdatedAt = now
	}
	if task.Status == "" {
		task.Status = contracts.TaskInProgress
	}
	if task.CompletionPolicy == "" {
		task.CompletionPolicy = contracts.CompletionPolicyCompletion
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO tasks(
		project_id, task_id, goal, status, current_step, next_action, source_client,
		last_session_id, created_at, updated_at, git_head, diff_hash,
		completion_verified, completion_policy, latest_verification_event_id,
		latest_verification_ref, latest_verification_kind, latest_verification_scope,
		latest_error_event_id, latest_error_ref, legacy)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, task_id) DO UPDATE SET
		goal=excluded.goal, status=excluded.status, current_step=excluded.current_step,
		next_action=excluded.next_action, source_client=excluded.source_client,
		last_session_id=excluded.last_session_id, updated_at=excluded.updated_at,
		git_head=excluded.git_head, diff_hash=excluded.diff_hash,
		completion_verified=excluded.completion_verified, completion_policy=excluded.completion_policy,
		latest_verification_event_id=excluded.latest_verification_event_id,
		latest_verification_ref=excluded.latest_verification_ref,
		latest_verification_kind=excluded.latest_verification_kind,
		latest_verification_scope=excluded.latest_verification_scope,
		latest_error_event_id=excluded.latest_error_event_id,
		latest_error_ref=excluded.latest_error_ref, legacy=excluded.legacy`,
		task.ProjectID, task.TaskID, task.Goal, task.Status, task.CurrentStep, task.NextAction,
		task.SourceClient, task.LastSessionID, task.CreatedAt.UTC().Format(time.RFC3339Nano),
		task.UpdatedAt.UTC().Format(time.RFC3339Nano), task.GitHead, task.DiffHash,
		boolInt(task.CompletionVerified), task.CompletionPolicy, task.LatestVerificationEventID,
		task.LatestVerificationRef, task.LatestVerificationKind, task.LatestVerificationScope,
		task.LatestErrorEventID, task.LatestErrorRef, boolInt(task.Legacy))
	if err != nil {
		return fmt.Errorf("write task projection: %w", err)
	}
	return nil
}

func replaceTaskScope(ctx context.Context, tx *sql.Tx, projectID, taskID string, payload parsedTaskPayload) error {
	if payload.HasChangedFiles {
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_files WHERE project_id=? AND task_id=?`, projectID, taskID); err != nil {
			return err
		}
		for _, path := range uniqueTaskValues(payload.ChangedFiles) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO task_files(project_id, task_id, path, strength) VALUES (?, ?, ?, ?)`, projectID, taskID, path, classifyTaskPath(path)); err != nil {
				return err
			}
		}
	}
	if payload.HasModulePaths {
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_modules WHERE project_id=? AND task_id=?`, projectID, taskID); err != nil {
			return err
		}
		for _, module := range uniqueTaskValues(payload.ModulePaths) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO task_modules(project_id, task_id, module_path) VALUES (?, ?, ?)`, projectID, taskID, module); err != nil {
				return err
			}
		}
	}
	if payload.HasDependencies {
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_dependencies WHERE project_id=? AND task_id=?`, projectID, taskID); err != nil {
			return err
		}
		for _, dependency := range uniqueTaskValues(payload.Dependencies) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO task_dependencies(project_id, task_id, dependency) VALUES (?, ?, ?)`, projectID, taskID, dependency); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertTaskVerification(ctx context.Context, tx *sql.Tx, verification TaskVerification) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO task_verifications(
		verification_id, project_id, task_id, event_id, verification_ref,
		verification_kind, verification_scope, git_head, diff_hash, exit_code,
		command, summary, observed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		verification.VerificationID, verification.ProjectID, verification.TaskID,
		verification.EventID, verification.VerificationRef, verification.Kind,
		verification.Scope, verification.GitHead, verification.DiffHash, nullableInt(verification.ExitCode),
		boundedTaskText(verification.Command, 2048), boundedTaskText(verification.Summary, 8192),
		verification.ObservedAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("write task verification: %w", err)
	}
	return nil
}

func verificationEvidenceExists(ctx context.Context, tx *sql.Tx, projectID, reference string) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE project_id=? AND (event_id=? OR idempotency_key=?))`, projectID, reference, reference).Scan(&exists)
	return exists == 1, err
}

func latestMatchingVerification(ctx context.Context, tx *sql.Tx, task TaskRecord, reference string) (TaskVerification, error) {
	query := `SELECT verification_id, project_id, task_id, event_id, verification_ref,
		verification_kind, verification_scope, git_head, diff_hash, exit_code, command,
		summary, observed_at FROM task_verifications WHERE project_id=? AND task_id=?`
	args := []any{task.ProjectID, task.TaskID}
	if strings.TrimSpace(reference) != "" {
		query += ` AND (verification_ref=? OR event_id=?)`
		args = append(args, reference, reference)
	}
	query += ` ORDER BY observed_at DESC LIMIT 1`
	var result TaskVerification
	var kind, scope, observed string
	var exit sql.NullInt64
	err := tx.QueryRowContext(ctx, query, args...).Scan(
		&result.VerificationID, &result.ProjectID, &result.TaskID, &result.EventID,
		&result.VerificationRef, &kind, &scope, &result.GitHead, &result.DiffHash,
		&exit, &result.Command, &result.Summary, &observed)
	if err != nil {
		return TaskVerification{}, err
	}
	result.Kind = contracts.VerificationKind(kind)
	result.Scope = scope
	if exit.Valid {
		value := int(exit.Int64)
		result.ExitCode = &value
	}
	result.ObservedAt = parseTime(observed)
	return result, nil
}

func setActiveTask(ctx context.Context, tx *sql.Tx, projectID, sessionID, taskID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO active_tasks(project_id, session_id, task_id, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET session_id=excluded.session_id, task_id=excluded.task_id, updated_at=excluded.updated_at`,
		projectID, sessionID, taskID, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("set active task: %w", err)
	}
	return nil
}

func (s *Store) GetTask(ctx context.Context, projectID, taskID string) (TaskRecord, error) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(taskID) == "" {
		return TaskRecord{}, errors.New("project ID and task ID are required")
	}
	return loadTask(ctx, s.db, projectID, taskID)
}

func (s *Store) ListTasks(ctx context.Context, projectID string, statuses []contracts.TaskStatus, limit int) ([]TaskRecord, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("project ID is required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query := `SELECT task_id FROM tasks WHERE project_id=?`
	args := []any{projectID}
	if len(statuses) > 0 {
		placeholders := make([]string, len(statuses))
		for index, status := range statuses {
			placeholders[index] = "?"
			args = append(args, status)
		}
		query += ` AND status IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY updated_at DESC, task_id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	var result []TaskRecord
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			return nil, err
		}
		task, err := s.GetTask(ctx, projectID, taskID)
		if err != nil {
			return nil, err
		}
		result = append(result, task)
	}
	return result, rows.Err()
}

func (s *Store) ActiveTask(ctx context.Context, projectID string) (string, string, error) {
	var taskID, sessionID string
	err := s.db.QueryRowContext(ctx, `SELECT task_id, session_id FROM active_tasks WHERE project_id=?`, projectID).Scan(&taskID, &sessionID)
	return taskID, sessionID, err
}

func loadTask(ctx context.Context, queryer sqlQueryer, projectID, taskID string) (TaskRecord, error) {
	var task TaskRecord
	var status, source, created, updated, policy, verificationKind string
	var completionInt, legacyInt int
	err := queryer.QueryRowContext(ctx, `SELECT project_id, task_id, goal, status, current_step, next_action,
		source_client, last_session_id, created_at, updated_at, git_head, diff_hash,
		completion_verified, completion_policy, latest_verification_event_id,
		latest_verification_ref, latest_verification_kind, latest_verification_scope,
		latest_error_event_id, latest_error_ref, legacy
		FROM tasks WHERE project_id=? AND task_id=?`, projectID, taskID).Scan(
		&task.ProjectID, &task.TaskID, &task.Goal, &status, &task.CurrentStep,
		&task.NextAction, &source, &task.LastSessionID, &created, &updated,
		&task.GitHead, &task.DiffHash, &completionInt, &policy,
		&task.LatestVerificationEventID, &task.LatestVerificationRef,
		&verificationKind, &task.LatestVerificationScope, &task.LatestErrorEventID,
		&task.LatestErrorRef, &legacyInt)
	if err != nil {
		return TaskRecord{}, err
	}
	task.Status = contracts.TaskStatus(status)
	task.SourceClient = contracts.HookClient(source)
	task.CompletionPolicy = contracts.CompletionPolicy(policy)
	task.LatestVerificationKind = contracts.VerificationKind(verificationKind)
	task.CompletionVerified = completionInt != 0
	task.Legacy = legacyInt != 0
	task.CreatedAt = parseTime(created)
	task.UpdatedAt = parseTime(updated)
	files, evidence, err := loadTaskFiles(ctx, queryer, projectID, taskID)
	if err != nil {
		return TaskRecord{}, err
	}
	task.ChangedFiles = files
	task.FileEvidence = evidence
	if task.ModulePaths, err = loadStringColumn(ctx, queryer, `SELECT module_path FROM task_modules WHERE project_id=? AND task_id=? ORDER BY module_path`, projectID, taskID); err != nil {
		return TaskRecord{}, err
	}
	if task.Dependencies, err = loadStringColumn(ctx, queryer, `SELECT dependency FROM task_dependencies WHERE project_id=? AND task_id=? ORDER BY dependency`, projectID, taskID); err != nil {
		return TaskRecord{}, err
	}
	return task, nil
}

func loadTaskFiles(ctx context.Context, queryer sqlQueryer, projectID, taskID string) ([]string, []TaskFileEvidence, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT path, strength FROM task_files WHERE project_id=? AND task_id=? ORDER BY path`, projectID, taskID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var paths []string
	var evidence []TaskFileEvidence
	for rows.Next() {
		var path, strength string
		if err := rows.Scan(&path, &strength); err != nil {
			return nil, nil, err
		}
		paths = append(paths, path)
		evidence = append(evidence, TaskFileEvidence{Path: path, Strength: strength})
	}
	return paths, evidence, rows.Err()
}

func loadStringColumn(ctx context.Context, queryer sqlQueryer, query string, args ...any) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func optionalStatusField(raw map[string]json.RawMessage) (contracts.TaskStatus, bool, error) {
	key := "status"
	if _, ok := raw[key]; !ok {
		key = "task_status"
	}
	value, present, err := optionalStringField(raw, key)
	if err != nil || !present {
		return "", present, err
	}
	status := contracts.TaskStatus(value)
	switch status {
	case contracts.TaskPlanned, contracts.TaskInProgress, contracts.TaskBlocked,
		contracts.TaskFailed, contracts.TaskCompleted, contracts.TaskInterrupted:
		return status, true, nil
	default:
		return "", true, fmt.Errorf("unsupported task status %q", value)
	}
}

func optionalStringField(raw map[string]json.RawMessage, key string) (string, bool, error) {
	value, ok := raw[key]
	if !ok {
		return "", false, nil
	}
	var result string
	if err := json.Unmarshal(value, &result); err != nil {
		return "", true, fmt.Errorf("decode %s: %w", key, err)
	}
	return strings.TrimSpace(result), true, nil
}

func optionalStringSliceField(raw map[string]json.RawMessage, key string) ([]string, bool, error) {
	value, ok := raw[key]
	if !ok {
		return nil, false, nil
	}
	var result []string
	if err := json.Unmarshal(value, &result); err != nil {
		return nil, true, fmt.Errorf("decode %s: %w", key, err)
	}
	return uniqueTaskValues(result), true, nil
}

func stringField(raw map[string]json.RawMessage, key string) string {
	value, ok := raw[key]
	if !ok {
		return ""
	}
	var result string
	if json.Unmarshal(value, &result) != nil {
		return ""
	}
	return strings.TrimSpace(result)
}

func uniqueTaskValues(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = boundedTaskText(value, 1024)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func classifyTaskPath(path string) string {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	base := filepath.Base(lower)
	if strings.HasPrefix(lower, ".baron/") || strings.HasPrefix(lower, ".git/") ||
		strings.HasPrefix(base, "generated") || strings.HasSuffix(base, ".gen.go") ||
		strings.HasSuffix(base, ".generated.ts") || strings.HasSuffix(base, ".lock") ||
		base == "package-lock.json" || base == "pnpm-lock.yaml" || base == "yarn.lock" ||
		base == "go.sum" || base == "cargo.lock" || strings.HasSuffix(lower, ".md") {
		return "weak"
	}
	return "strong"
}

func boundedTaskText(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}
