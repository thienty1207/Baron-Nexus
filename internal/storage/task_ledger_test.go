package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

func TestTaskLedgerMigrationAddsTablesToExistingSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"task_files", "task_modules", "task_dependencies", "task_verifications", "active_tasks", "tasks"} {
		if _, err := raw.Exec("DROP TABLE " + table); err != nil {
			raw.Close()
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec("UPDATE schema_meta SET schema_version=6"); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, table := range []string{"tasks", "task_files", "task_modules", "task_dependencies", "task_verifications", "active_tasks"} {
		var count int
		if err := store.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("migration did not restore %s", table)
		}
	}
}

func TestRemoteRecallCacheIsScopedByRecoveryFingerprint(t *testing.T) {
	store := openTaskTestStore(t)
	ctx := context.Background()
	projectID := "prj-recall-cache-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: t.TempDir(), Name: "recall cache"}); err != nil {
		t.Fatal(err)
	}
	want := RemoteRecallCache{
		ProjectID: projectID, SessionID: "session-a", Fingerprint: "fingerprint-a",
		QueryHash: "query-a", Snapshot: []byte(`{"records":[{"id":"historical-a"}]}`), ReceiptID: "receipt-a",
	}
	if err := store.PutRemoteRecallCache(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRemoteRecallCache(ctx, projectID, "session-a", "fingerprint-a", "query-a")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Snapshot) != string(want.Snapshot) || got.ReceiptID != want.ReceiptID {
		t.Fatalf("recall cache mismatch: got=%#v want=%#v", got, want)
	}
	if _, err := store.GetRemoteRecallCache(ctx, projectID, "session-a", "fingerprint-b", "query-a"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("changed recovery fingerprint reused cache: %v", err)
	}
}

func TestTaskLedgerProjectsLifecycleAndVerificationScope(t *testing.T) {
	store := openTaskTestStore(t)
	ctx := context.Background()
	projectID := "prj-task-ledger-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: t.TempDir(), Name: "task ledger"}); err != nil {
		t.Fatal(err)
	}

	started := taskEvent(projectID, contracts.EventTaskStarted, "task-a", "start-a", map[string]any{
		"goal":              "implement ledger",
		"status":            "planned",
		"source_client":     "codex",
		"session_id":        "session-a",
		"git_head":          "head-a",
		"diff_hash":         "diff-a",
		"changed_files":     []string{"internal/app/app.go", "README.md"},
		"module_paths":      []string{"internal/app"},
		"dependencies":      []string{"modernc.org/sqlite"},
		"completion_policy": "completion",
	})
	inserted, err := store.InsertEvent(ctx, started)
	if err != nil || !inserted {
		t.Fatalf("task start inserted=%v err=%v", inserted, err)
	}
	task, err := store.GetTask(ctx, projectID, "task-a")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != contracts.TaskPlanned || task.CompletionPolicy != contracts.CompletionPolicyCompletion {
		t.Fatalf("planned task projection mismatch: %#v", task)
	}
	if len(task.ChangedFiles) != 2 || len(task.ModulePaths) != 1 || len(task.Dependencies) != 1 {
		t.Fatalf("task scope was not projected: %#v", task)
	}

	updated := taskEvent(projectID, contracts.EventTaskUpdated, "task-a", "update-a", map[string]any{
		"status":       "in_progress",
		"current_step": "implementing transaction",
		"next_action":  "add projection tests",
	})
	if inserted, err := store.InsertEvent(ctx, updated); err != nil || !inserted {
		t.Fatalf("task update inserted=%v err=%v", inserted, err)
	}

	if _, err := store.InsertEvent(ctx, taskEvent(projectID, contracts.EventTestFinished, "", "test-unit-a", map[string]any{
		"command":   "go test ./internal/app",
		"exit_code": 0,
	})); err != nil {
		t.Fatal(err)
	}
	verifiedUnit := taskEvent(projectID, contracts.EventTaskVerified, "task-a", "verify-unit-a", map[string]any{
		"verification_ref":   "test-unit-a",
		"verification_kind":  "unit",
		"verification_scope": "internal/app",
		"git_head":           "head-a",
		"diff_hash":          "diff-a",
		"exit_code":          0,
	})
	if inserted, err := store.InsertEvent(ctx, verifiedUnit); err != nil || !inserted {
		t.Fatalf("unit verification inserted=%v err=%v", inserted, err)
	}
	task, err = store.GetTask(ctx, projectID, "task-a")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != contracts.TaskInProgress || task.CompletionVerified || task.LatestVerificationKind != contracts.VerificationUnit {
		t.Fatalf("unit verification incorrectly completed task: %#v", task)
	}

	completedAttempt := taskEvent(projectID, contracts.EventTaskCompleted, "task-a", "complete-before-policy", map[string]any{
		"verification_ref": "test-unit-a",
		"git_head":         "head-a",
		"diff_hash":        "diff-a",
	})
	if inserted, err := store.InsertEvent(ctx, completedAttempt); err != nil || !inserted {
		t.Fatalf("unverified completion should remain journaled: inserted=%v err=%v", inserted, err)
	}
	task, err = store.GetTask(ctx, projectID, "task-a")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status == contracts.TaskCompleted || task.CompletionVerified {
		t.Fatalf("unit evidence promoted completion: %#v", task)
	}

	if _, err := store.InsertEvent(ctx, taskEvent(projectID, contracts.EventTestFinished, "", "test-completion-a", map[string]any{
		"command":   "go test ./...",
		"exit_code": 0,
	})); err != nil {
		t.Fatal(err)
	}
	verifiedCompletion := taskEvent(projectID, contracts.EventTaskVerified, "task-a", "verify-completion-a", map[string]any{
		"verification_ref":   "test-completion-a",
		"verification_kind":  "completion",
		"verification_scope": "repository",
		"git_head":           "head-a",
		"diff_hash":          "diff-a",
		"exit_code":          0,
	})
	if inserted, err := store.InsertEvent(ctx, verifiedCompletion); err != nil || !inserted {
		t.Fatalf("completion verification inserted=%v err=%v", inserted, err)
	}
	if inserted, err := store.InsertEvent(ctx, taskEvent(projectID, contracts.EventTaskCompleted, "task-a", "complete-a", map[string]any{
		"verification_ref": "test-completion-a",
		"git_head":         "head-a",
		"diff_hash":        "diff-a",
	})); err != nil || !inserted {
		t.Fatalf("verified completion inserted=%v err=%v", inserted, err)
	}
	task, err = store.GetTask(ctx, projectID, "task-a")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != contracts.TaskCompleted || !task.CompletionVerified {
		t.Fatalf("completion policy did not promote task: %#v", task)
	}
}

func TestTaskUpdatedRequiresExistingTaskAndTaskStartedIsIdempotent(t *testing.T) {
	store := openTaskTestStore(t)
	ctx := context.Background()
	projectID := "prj-task-validation-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: t.TempDir(), Name: "task validation"}); err != nil {
		t.Fatal(err)
	}
	_, err := store.InsertEvent(ctx, taskEvent(projectID, contracts.EventTaskUpdated, "missing", "missing-update", map[string]any{
		"status": "planned",
	}))
	if err == nil {
		t.Fatal("unknown task_updated unexpectedly succeeded")
	}
	if _, getErr := store.GetTask(ctx, projectID, "missing"); !errors.Is(getErr, sql.ErrNoRows) {
		t.Fatalf("unknown update created a task: %v", getErr)
	}
	count, err := store.CountEvents(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rejected task update was journaled as accepted event: %d", count)
	}

	started := taskEvent(projectID, contracts.EventTaskStarted, "task-b", "start-b", map[string]any{
		"goal": "planned task",
	})
	if inserted, err := store.InsertEvent(ctx, started); err != nil || !inserted {
		t.Fatalf("first task start inserted=%v err=%v", inserted, err)
	}
	started.EventID = "evt-start-b-duplicate"
	if inserted, err := store.InsertEvent(ctx, started); err != nil || inserted {
		t.Fatalf("duplicate task start inserted=%v err=%v", inserted, err)
	}
	count, err = store.CountEvents(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate task start changed event count: %d", count)
	}
}

func TestTaskLedgerKeepsInterruptedAndFailedTasksUnresolved(t *testing.T) {
	store := openTaskTestStore(t)
	ctx := context.Background()
	projectID := "prj-task-status-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: t.TempDir(), Name: "task status"}); err != nil {
		t.Fatal(err)
	}
	for index, eventType := range []contracts.EventType{contracts.EventTaskFailed, contracts.EventTaskBlocked, contracts.EventTaskInterrupted} {
		taskID := "task-status-" + string(rune('a'+index))
		if _, err := store.InsertEvent(ctx, taskEvent(projectID, contracts.EventTaskStarted, taskID, "start-"+taskID, map[string]any{})); err != nil {
			t.Fatal(err)
		}
		if _, err := store.InsertEvent(ctx, taskEvent(projectID, eventType, taskID, "state-"+taskID, map[string]any{"latest_error_ref": "error-" + taskID})); err != nil {
			t.Fatal(err)
		}
		task, err := store.GetTask(ctx, projectID, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == contracts.TaskCompleted || task.CompletionVerified {
			t.Fatalf("unresolved event promoted task %s: %#v", taskID, task)
		}
	}
}

func openTaskTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func taskEvent(projectID string, eventType contracts.EventType, taskID, idempotency string, payload map[string]any) Event {
	payload["task_id"] = taskID
	data, _ := json.Marshal(payload)
	return Event{
		EventID:        "evt-" + idempotency,
		ProjectID:      projectID,
		SessionID:      "session-task",
		Client:         contracts.ClientCodex,
		Type:           eventType,
		OccurredAt:     time.Now().UTC(),
		Payload:        data,
		IdempotencyKey: idempotency,
	}
}
