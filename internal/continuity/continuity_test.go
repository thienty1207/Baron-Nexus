package continuity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/storage"
)

func TestCheckpointTracksLatestDurableWorkState(t *testing.T) {
	root := t.TempDir()
	store, err := storage.Open(filepath.Join(root, ".baron", "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	projectID := "prj-continuity-12345678"
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "continuity"}); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, projectID, "continuity", filepath.Join(root, ".baron", "checkpoint.json"))
	state := WorkState{Task: TaskState{Goal: "Implement continuity", Status: contracts.TaskInProgress}}
	for _, step := range []string{"create schema", "capture event", "materialize checkpoint"} {
		state.Task.CurrentStep = step
		state.Task.LastSuccessfulStep = "previous: " + step
		if err := engine.Save(context.Background(), state); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := engine.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Task.CurrentStep != "materialize checkpoint" {
		t.Fatalf("latest state not loaded: %#v", loaded)
	}
	data, err := os.ReadFile(filepath.Join(root, ".baron", "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint WorkState
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.Task.CurrentStep != loaded.Task.CurrentStep || checkpoint.SchemaVersion != 1 {
		t.Fatalf("checkpoint mismatch: %#v", checkpoint)
	}
}

func TestCheckpointPreservesTaskIdentityAndVerificationMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	projectID := "prj-task-metadata-12345678"
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "task metadata"}); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, projectID, "task metadata", filepath.Join(root, "checkpoint.json"))
	want := WorkState{
		ProjectID:    projectID,
		ActiveTaskID: "task-a",
		Task: TaskState{
			TaskID:                    "task-a",
			Status:                    contracts.TaskInProgress,
			CompletionPolicy:          contracts.CompletionPolicyCompletion,
			LatestVerificationEventID: "evt-verify-a",
			LatestVerificationKind:    contracts.VerificationUnit,
			LatestVerificationScope:   "internal/app",
			LatestErrorRef:            "evt-error-a",
		},
	}
	if err := engine.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := engine.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveTaskID != want.ActiveTaskID || got.Task.TaskID != want.Task.TaskID ||
		got.Task.CompletionPolicy != want.Task.CompletionPolicy ||
		got.Task.LatestVerificationEventID != want.Task.LatestVerificationEventID ||
		got.Task.LatestVerificationKind != want.Task.LatestVerificationKind ||
		got.Task.LatestVerificationScope != want.Task.LatestVerificationScope ||
		got.Task.LatestErrorRef != want.Task.LatestErrorRef {
		t.Fatalf("task metadata was not persisted: got=%#v want=%#v", got, want)
	}
}

func TestActiveSessionBecomesInterruptedButTaskRemainsInProgress(t *testing.T) {
	lastSeen := time.Now().Add(-10 * time.Minute)
	state := ClassifySession(contracts.SessionActive, lastSeen, time.Now(), time.Minute)
	if state != contracts.SessionInterrupted {
		t.Fatalf("expected interrupted session, got %s", state)
	}
	work := WorkState{Task: TaskState{Status: contracts.TaskInProgress}}
	if work.Task.Status == contracts.TaskCompleted {
		t.Fatal("interruption falsely completed task")
	}
}

func TestCleanSessionCloseDoesNotCompleteUnfinishedTask(t *testing.T) {
	state := ClassifySession(contracts.SessionCleanClosed, time.Now(), time.Now(), time.Minute)
	if state != contracts.SessionCleanClosed {
		t.Fatalf("clean session changed state: %s", state)
	}
	work := WorkState{Task: TaskState{Status: contracts.TaskInProgress}}
	work.SessionState = state
	if work.Task.Status != contracts.TaskInProgress {
		t.Fatalf("clean close changed task status: %s", work.Task.Status)
	}
}

func TestRepositoryEvidenceDetectsExternalDrift(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.name", "test")
	runGit(t, root, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "initial")
	before, err := InspectRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := InspectRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if before.GitHead != after.GitHead {
		t.Fatalf("working-tree edit changed HEAD: %s -> %s", before.GitHead, after.GitHead)
	}
	if len(after.ChangedFiles) != 1 || after.ChangedFiles[0] != "README.md" {
		t.Fatalf("drift not captured: %#v", after)
	}
	if before.DiffHash == after.DiffHash {
		t.Fatal("diff hash did not change after external edit")
	}
}

func TestConcurrentEnginesLeaveAValidCheckpoint(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-lock-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "lock"}); err != nil {
		t.Fatal(err)
	}
	checkpoint := filepath.Join(root, "checkpoint.json")
	engines := []*Engine{
		NewEngine(store, projectID, "lock", checkpoint),
		NewEngine(store, projectID, "lock", checkpoint),
	}
	var group sync.WaitGroup
	for index := 0; index < 40; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			state := WorkState{ProjectID: projectID, Task: TaskState{Goal: "goal", CurrentStep: "step", NextAction: "next"}, SessionID: "session"}
			state.LatestTest = TestEvidence{Command: "go test", Summary: "writer"}
			if saveErr := engines[index%len(engines)].Save(context.Background(), state); saveErr != nil {
				t.Errorf("concurrent save %d: %v", index, saveErr)
			}
		}(index)
	}
	group.Wait()
	if _, err := os.Stat(checkpoint); err != nil {
		t.Fatal(err)
	}
	state, err := engines[0].Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.ProjectID != projectID || state.Task.Goal != "goal" {
		t.Fatalf("checkpoint was not a valid latest state: %#v", state)
	}
}

func TestSaveLocalPersistsSQLiteWithoutCheckpointLock(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-local-save-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "local save"}); err != nil {
		t.Fatal(err)
	}
	checkpoint := filepath.Join(root, "checkpoint.json")
	engine := NewEngine(store, projectID, "local save", checkpoint)
	if err := engine.Save(context.Background(), WorkState{ProjectID: projectID, Task: TaskState{Goal: "initial checkpoint"}}); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, "runtime", "locks", "checkpoint.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("active process\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := engine.SaveLocal(context.Background(), WorkState{ProjectID: projectID, Task: TaskState{CurrentStep: "hot local state"}}); err != nil {
		t.Fatal(err)
	}
	state, err := engine.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Task.CurrentStep != "hot local state" {
		t.Fatalf("local SQLite state was not updated: %#v", state.Task)
	}
	checkpointData, err := os.ReadFile(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(checkpointData, []byte("hot local state")) {
		t.Fatal("SaveLocal unexpectedly materialized the checkpoint")
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("SaveLocal changed the checkpoint lock: %v", err)
	}
}

func TestConcurrentSaveLocalWritersKeepSQLiteStateValid(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-local-save-concurrent-12345678"
	databasePath := filepath.Join(root, "state.db")
	first, err := storage.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "local save concurrent"}); err != nil {
		t.Fatal(err)
	}
	stores := []*storage.Store{first}
	for index := 0; index < 3; index++ {
		store, openErr := storage.Open(databasePath)
		if openErr != nil {
			t.Fatal(openErr)
		}
		stores = append(stores, store)
		defer store.Close()
	}
	engines := make([]*Engine, 0, len(stores))
	for _, store := range stores {
		engines = append(engines, NewEngine(store, projectID, "local save concurrent", filepath.Join(root, "checkpoint.json")))
	}
	var group sync.WaitGroup
	for index := 0; index < 80; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			state := WorkState{ProjectID: projectID, Task: TaskState{Goal: "concurrent local goal", CurrentStep: fmt.Sprintf("step-%d", index)}}
			if saveErr := engines[index%len(engines)].SaveLocal(context.Background(), state); saveErr != nil {
				t.Errorf("concurrent local save %d: %v", index, saveErr)
			}
		}(index)
	}
	group.Wait()
	state, err := engines[0].Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Task.Goal != "concurrent local goal" || state.Task.CurrentStep == "" {
		t.Fatalf("concurrent local saves lost canonical state: %#v", state.Task)
	}
}

func TestStaleCheckpointLockIsRecoverable(t *testing.T) {
	root := t.TempDir()
	projectID := "prj-stale-lock-12345678"
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "stale"}); err != nil {
		t.Fatal(err)
	}
	checkpoint := filepath.Join(root, "checkpoint.json")
	lockPath := filepath.Join(root, "runtime", "locks", "checkpoint.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("dead process\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-checkpointLockStaleAfter - time.Second)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	if err := NewEngine(store, projectID, "stale", checkpoint).Save(context.Background(), WorkState{ProjectID: projectID}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("stale checkpoint lock remained: %v", err)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
