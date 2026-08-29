package continuity

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/storage"
	_ "modernc.org/sqlite"
)

type syncBackend struct {
	available bool
	seen      map[string]int
}

func (b *syncBackend) Health(context.Context) error { return nil }
func (b *syncBackend) EnsureIdentity(context.Context, contracts.IdentitySpec) (contracts.Identity, error) {
	return contracts.Identity{}, nil
}
func (b *syncBackend) EnsureProjectAgent(context.Context, contracts.IsolationContext, string) (contracts.ProjectBinding, error) {
	return contracts.ProjectBinding{}, nil
}
func (b *syncBackend) Capture(_ context.Context, _ contracts.IsolationContext, record contracts.MemoryRecord, key string) (contracts.MemoryReceipt, error) {
	if !b.available {
		return contracts.MemoryReceipt{}, errors.New("network down")
	}
	if b.seen == nil {
		b.seen = map[string]int{}
	}
	b.seen[key]++
	return contracts.MemoryReceipt{RequestID: key, IdempotencyKey: key, ContentHash: record.ContentHash}, nil
}
func (b *syncBackend) Search(context.Context, contracts.IsolationContext, contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	return nil, nil
}

func TestOfflineQueueFlushesRedactedRecordsExactlyOnce(t *testing.T) {
	root := t.TempDir()
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	projectID := "prj-sync-12345678"
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "sync"}); err != nil {
		t.Fatal(err)
	}
	backend := &syncBackend{}
	syncer := NewSyncer(store, backend, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"}, []string{"sk-secret"})
	queued, err := syncer.QueueCapture(context.Background(), contracts.MemoryRecord{ProjectID: projectID, Content: "result sk-secret", SourceClient: contracts.ClientCodex}, "idem-1")
	if err != nil || !queued {
		t.Fatalf("queue failed: %v %v", queued, err)
	}
	if err := syncer.QueueCaptureDuplicate(context.Background(), contracts.MemoryRecord{ProjectID: projectID, Content: "result sk-secret", SourceClient: contracts.ClientDSH}, "idem-1"); err != nil {
		t.Fatal(err)
	}
	count, err := store.QueueCount(context.Background(), projectID, "pending")
	if err != nil || count != 1 {
		t.Fatalf("pending queue count=%d err=%v", count, err)
	}
	items, err := store.DueQueue(context.Background(), projectID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || strings.Contains(string(items[0].Payload), "sk-secret") {
		t.Fatalf("queue payload was not redacted: %#v", items)
	}
	if _, err := syncer.Flush(context.Background(), 10); err == nil {
		t.Fatal("offline flush should report a classified delivery failure")
	}
	backend.available = true
	time.Sleep(35 * time.Millisecond)
	if delivered, err := syncer.Flush(context.Background(), 10); err != nil || delivered != 1 {
		t.Fatalf("online flush delivered=%d err=%v", delivered, err)
	}
	if delivered, err := syncer.Flush(context.Background(), 10); err != nil || delivered != 0 {
		t.Fatalf("second flush delivered=%d err=%v", delivered, err)
	}
	if backend.seen["idem-1"] != 1 {
		t.Fatalf("semantic duplicate delivery count=%d", backend.seen["idem-1"])
	}
	items, err = store.DueQueue(context.Background(), projectID, 10)
	if err != nil || len(items) != 0 {
		t.Fatalf("delivered queue item became due again: items=%#v err=%v", items, err)
	}
	allItems, err := store.ListQueue(context.Background(), projectID, "", 10)
	if err != nil || len(allItems) != 1 {
		t.Fatalf("queue diagnostic listing failed: %#v err=%v", allItems, err)
	}
	receipt, err := store.GetQueueReceipt(context.Background(), allItems[0].QueueID)
	if err != nil || receipt.RequestID != "idem-1" || receipt.Operation != storage.QueueOperationMemoryCapture || receipt.ReceiptID == "" {
		t.Fatalf("delivery receipt was not durable: %#v err=%v", receipt, err)
	}
}

func TestTypedKnowledgeQueueOperationUsesHandlerAndIsDeliveredOnce(t *testing.T) {
	root := t.TempDir()
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	projectID := "prj-typed-12345678"
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "typed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueSync(context.Background(), storage.QueueItem{ProjectID: projectID, Operation: storage.QueueOperationWikiIngest, IdempotencyKey: "wiki-1", Payload: []byte(`{"project_id":"prj-typed-12345678"}`)}); err != nil {
		t.Fatal(err)
	}
	syncer := NewSyncer(store, nil, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"}, nil)
	seen := 0
	syncer.SetQueueOperationHandler(func(_ context.Context, item storage.QueueItem) (string, error) {
		seen++
		if item.Operation != storage.QueueOperationWikiIngest {
			t.Fatalf("unexpected typed operation: %#v", item)
		}
		return "wiki-request-1", nil
	})
	delivered, err := syncer.Flush(context.Background(), 10)
	if err != nil || delivered != 1 || seen != 1 {
		t.Fatalf("typed operation delivered=%d seen=%d err=%v", delivered, seen, err)
	}
	delivered, err = syncer.Flush(context.Background(), 10)
	if err != nil || delivered != 0 || seen != 1 {
		t.Fatalf("typed operation was not exactly once: delivered=%d seen=%d err=%v", delivered, seen, err)
	}
	items, err := store.ListQueue(context.Background(), projectID, "", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("typed queue item disappeared: %#v err=%v", items, err)
	}
	receipt, err := store.GetQueueReceipt(context.Background(), items[0].QueueID)
	if err != nil || receipt.Operation != storage.QueueOperationWikiIngest || receipt.RequestID != "wiki-request-1" {
		t.Fatalf("typed queue receipt missing: %#v err=%v", receipt, err)
	}
}

func TestMemoryCaptureWithoutBackendFailsOpenInsteadOfPanicking(t *testing.T) {
	root := t.TempDir()
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	projectID := "prj-memory-no-backend-12345678"
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "memory-no-backend"}); err != nil {
		t.Fatal(err)
	}
	syncer := NewSyncer(store, nil, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"}, nil)
	syncer.SetQueueOperationHandler(func(context.Context, storage.QueueItem) (string, error) {
		return "typed-handler", nil
	})
	if _, err := syncer.QueueCapture(context.Background(), contracts.MemoryRecord{ProjectID: projectID, Content: "queued without backend", SourceClient: contracts.ClientCodex}, "no-backend-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := syncer.Flush(context.Background(), 10); err == nil || !strings.Contains(err.Error(), "memory backend") {
		t.Fatalf("missing memory backend was not reported safely: %v", err)
	}
}

func TestTypedKnowledgeQueueRetriesOutageThenDeliversExactlyOnce(t *testing.T) {
	root := t.TempDir()
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	projectID := "prj-typed-outage-12345678"
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "typed-outage"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueSync(context.Background(), storage.QueueItem{ProjectID: projectID, Operation: storage.QueueOperationWikiIngest, IdempotencyKey: "wiki-outage-1", Payload: []byte(`{"project_id":"prj-typed-outage-12345678"}`)}); err != nil {
		t.Fatal(err)
	}
	syncer := NewSyncer(store, nil, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"}, nil)
	available := false
	deliveries := 0
	syncer.SetQueueOperationHandler(func(context.Context, storage.QueueItem) (string, error) {
		if !available {
			return "", errors.New("knowledge service unavailable")
		}
		deliveries++
		return "wiki-outage-request", nil
	})
	if _, err := syncer.Flush(context.Background(), 10); err == nil {
		t.Fatal("knowledge outage did not produce a retryable error")
	}
	available = true
	time.Sleep(100 * time.Millisecond)
	if delivered, err := syncer.Flush(context.Background(), 10); err != nil || delivered != 1 || deliveries != 1 {
		t.Fatalf("knowledge retry delivery=%d deliveries=%d err=%v", delivered, deliveries, err)
	}
	if delivered, err := syncer.Flush(context.Background(), 10); err != nil || delivered != 0 || deliveries != 1 {
		t.Fatalf("knowledge retry was not exactly once: delivered=%d deliveries=%d err=%v", delivered, deliveries, err)
	}
}

func TestQueueOperationPersistsIsolatedRedactedSummary(t *testing.T) {
	root := t.TempDir()
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	projectID := "prj-summary-12345678"
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "summary"}); err != nil {
		t.Fatal(err)
	}
	syncer := NewSyncer(store, nil, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"}, []string{"custom-secret"})
	queued, err := syncer.QueueOperation(context.Background(), storage.QueueOperationScenarioUpdate, "summary-1", map[string]any{"summary": "decision custom-secret", "changed_files": []string{"auth.go"}})
	if err != nil || !queued {
		t.Fatalf("summary operation was not queued: queued=%v err=%v", queued, err)
	}
	items, err := store.DueQueue(context.Background(), projectID, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("summary queue item missing: %#v err=%v", items, err)
	}
	if items[0].Operation != storage.QueueOperationScenarioUpdate || strings.Contains(string(items[0].Payload), "custom-secret") || !strings.Contains(string(items[0].Payload), "[REDACTED]") {
		t.Fatalf("summary payload was not isolated/redacted: %#v", items[0])
	}
	if _, err := syncer.QueueOperation(context.Background(), "arbitrary", "bad", nil); err == nil {
		t.Fatal("arbitrary queue operation was accepted")
	}
}

func TestInvalidQueuePayloadMovesToDeadLetterAndCanBeExplicitlyRequeued(t *testing.T) {
	root := t.TempDir()
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	projectID := "prj-dead-letter-12345678"
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "dead-letter"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueSync(context.Background(), storage.QueueItem{ProjectID: projectID, IdempotencyKey: "poison-1", Payload: []byte("not-json")}); err != nil {
		t.Fatal(err)
	}
	syncer := NewSyncer(store, &syncBackend{available: true}, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"}, nil)
	if _, err := syncer.Flush(context.Background(), 10); err == nil || !strings.Contains(err.Error(), "decode queue item") {
		t.Fatalf("poison payload did not report a decode failure: %v", err)
	}
	dead, err := store.QueueCount(context.Background(), projectID, "dead_letter")
	if err != nil || dead != 1 {
		t.Fatalf("poison payload was not dead-lettered: count=%d err=%v", dead, err)
	}
	items, err := store.ListQueue(context.Background(), projectID, "dead_letter", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("dead-letter payload was not preserved: %#v err=%v", items, err)
	}
	if err := store.RequeueDeadLetter(context.Background(), items[0].QueueID); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.QueueCount(context.Background(), projectID, "pending"); err != nil || pending != 1 {
		t.Fatalf("explicit dead-letter requeue failed: pending=%d err=%v", pending, err)
	}
}

func TestFlushAutomaticallyRepairsOversizedMemoryDeadLetters(t *testing.T) {
	root := t.TempDir()
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	projectID := "prj-auto-queue-repair-12345678"
	isolation := contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"}
	if err := store.RegisterProject(context.Background(), storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "auto-queue-repair"}); err != nil {
		t.Fatal(err)
	}
	backend := &syncBackend{available: true}
	syncer := NewSyncer(store, backend, isolation, nil)
	if queued, err := syncer.QueueCapture(context.Background(), contracts.MemoryRecord{ProjectID: projectID, Content: "recoverable oversized checkpoint", SourceClient: contracts.ClientDSH}, "auto-repair-1"); err != nil || !queued {
		t.Fatalf("queue capture failed: queued=%v err=%v", queued, err)
	}
	items, err := store.ListQueue(context.Background(), projectID, "pending", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("queued items=%#v err=%v", items, err)
	}
	if err := store.MarkDeadLetter(context.Background(), items[0].QueueID, `Tencent request POST /v3/conversation/add failed with HTTP 400: {"message":"messages.0.content: Too big: expected string to have <=8192 characters"}`); err != nil {
		t.Fatal(err)
	}
	delivered, err := syncer.Flush(context.Background(), 10)
	if err != nil || delivered != 1 {
		t.Fatalf("automatic oversized queue repair delivered=%d err=%v", delivered, err)
	}
	if dead, err := store.QueueCount(context.Background(), projectID, "dead_letter"); err != nil || dead != 0 {
		t.Fatalf("oversized queue item remained dead-lettered: count=%d err=%v", dead, err)
	}
}

func TestConcurrentCodeGraphSyncAndCheckpointRemainDurable(t *testing.T) {
	root := t.TempDir()
	store, err := storage.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	projectID := "prj-concurrent-12345678"
	if err := store.RegisterProject(ctx, storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "concurrent"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertKnowledgeRegistry(ctx, storage.KnowledgeRegistry{ProjectID: projectID, TeamID: "team", UserID: "user", AgentID: "agent", CodeGraphID: "graph-1", CodeGraphStatus: "pending"}); err != nil {
		t.Fatal(err)
	}
	isolation := contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"}
	syncer := NewSyncer(store, nil, isolation, nil)
	if _, err := syncer.QueueOperation(ctx, storage.QueueOperationCodeGraphSync, "graph-sync-1", map[string]any{"code_graph_id": "graph-1"}); err != nil {
		t.Fatal(err)
	}
	var handlerCalls int
	syncer.SetQueueOperationHandler(func(_ context.Context, item storage.QueueItem) (string, error) {
		if item.Operation != storage.QueueOperationCodeGraphSync {
			return "", errors.New("unexpected operation")
		}
		handlerCalls++
		registry, readErr := store.GetKnowledgeRegistry(ctx, projectID)
		if readErr != nil {
			return "", readErr
		}
		registry.CodeGraphStatus = "ready"
		registry.CodeGraphCommit = "abc123"
		if writeErr := store.UpsertKnowledgeRegistry(ctx, registry); writeErr != nil {
			return "", writeErr
		}
		return "graph-request-1", nil
	})
	engine := NewEngine(store, projectID, "concurrent", filepath.Join(root, "checkpoint.json"))
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(2)
	var flushDelivered int
	var flushErr error
	var saveErr error
	go func() {
		defer group.Done()
		<-start
		for index := 0; index < 20; index++ {
			if err := engine.Save(ctx, WorkState{ProjectID: projectID, ProjectName: "concurrent", SessionID: "session", SessionState: contracts.SessionActive, Task: TaskState{Goal: "sync graph", Status: contracts.TaskInProgress, CurrentStep: "checkpoint", NextAction: "continue"}}); err != nil {
				saveErr = err
				return
			}
		}
	}()
	go func() {
		defer group.Done()
		<-start
		flushDelivered, flushErr = syncer.Flush(ctx, 10)
	}()
	close(start)
	group.Wait()
	if saveErr != nil || flushErr != nil || flushDelivered != 1 || handlerCalls != 1 {
		t.Fatalf("concurrent graph delivery failed: delivered=%d calls=%d save_err=%v flush_err=%v", flushDelivered, handlerCalls, saveErr, flushErr)
	}
	if err := engine.Save(ctx, WorkState{ProjectID: projectID, ProjectName: "concurrent", SessionID: "session", SessionState: contracts.SessionActive, Task: TaskState{Goal: "sync graph", Status: contracts.TaskInProgress, CurrentStep: "final checkpoint", NextAction: "verify graph"}}); err != nil {
		t.Fatal(err)
	}
	state, err := engine.Load(ctx)
	if err != nil || state.Task.CurrentStep != "final checkpoint" {
		t.Fatalf("checkpoint was not durable after concurrent sync: %#v err=%v", state, err)
	}
	registry, err := store.GetKnowledgeRegistry(ctx, projectID)
	if err != nil || registry.CodeGraphStatus != "ready" || registry.CodeGraphCommit != "abc123" {
		t.Fatalf("CodeGraph freshness was not durable: %#v err=%v", registry, err)
	}
	items, err := store.ListQueue(ctx, projectID, "delivered", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("delivered CodeGraph queue item missing: %#v err=%v", items, err)
	}
	if receipt, receiptErr := store.GetQueueReceipt(ctx, items[0].QueueID); receiptErr != nil || receipt.RequestID != "graph-request-1" {
		t.Fatalf("CodeGraph delivery receipt missing: %#v err=%v", receipt, receiptErr)
	}
}

func TestAbruptProcessRestartRecoversCheckpointAndQueuedKnowledge(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "state.db")
	checkpointPath := filepath.Join(root, "checkpoint.json")
	projectID := "prj-power-loss-12345678"
	ctx := context.Background()
	store, err := storage.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterProject(ctx, storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "power-loss"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	engine := NewEngine(store, projectID, "power-loss", checkpointPath)
	if err := engine.Save(ctx, WorkState{
		ProjectID: projectID, ProjectName: "power-loss", SessionID: "session-before-kill", SessionState: contracts.SessionActive,
		Task: TaskState{Goal: "finish authentication", Status: contracts.TaskInProgress, CurrentStep: "refresh flow", NextAction: "rerun integration test"},
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	syncer := NewSyncer(store, nil, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"}, nil)
	if _, err := syncer.QueueOperation(ctx, storage.QueueOperationWikiIngest, "wiki-after-kill", map[string]any{"wiki_id": "wiki-1"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	due, err := store.DueQueue(ctx, projectID, 1)
	if err != nil || len(due) != 1 {
		store.Close()
		t.Fatalf("due queue=%#v err=%v", due, err)
	}
	claimed, err := store.ClaimQueue(ctx, due[0].QueueID)
	if err != nil || !claimed {
		store.Close()
		t.Fatalf("simulated interrupted claim=%v err=%v", claimed, err)
	}
	// Closing the store models the process disappearing after ClaimQueue but
	// before delivery/receipt. The checkpoint and SQLite WAL are the durable
	// recovery boundary; no task completion is invented.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// Age the claimed row after the simulated process exit. This uses a
	// separate SQLite handle because the continuity package intentionally has
	// no access to storage internals.
	rawDB, err := sql.Open("sqlite", databasePath+"?_pragma=busy_timeout%3d5000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.ExecContext(ctx, `UPDATE sync_queue SET updated_at=? WHERE project_id=? AND idempotency_key=?`, time.Now().UTC().Add(-2*time.Minute).Format(time.RFC3339Nano), projectID, "wiki-after-kill"); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := storage.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	recovered, err := restarted.RecoverStaleQueueClaims(ctx, projectID, time.Now().UTC().Add(-time.Minute))
	if err != nil || recovered != 1 {
		t.Fatalf("interrupted queue lease was not recovered: recovered=%d err=%v", recovered, err)
	}
	restartedEngine := NewEngine(restarted, projectID, "power-loss", checkpointPath)
	state, err := restartedEngine.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Task.Status != contracts.TaskInProgress || state.Task.CurrentStep != "refresh flow" || state.Task.CompletionVerified {
		t.Fatalf("restart invented or lost task state: %#v", state.Task)
	}
	newSyncer := NewSyncer(restarted, nil, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"}, nil)
	deliveries := 0
	newSyncer.SetQueueOperationHandler(func(_ context.Context, item storage.QueueItem) (string, error) {
		if item.Operation != storage.QueueOperationWikiIngest {
			return "", errors.New("unexpected recovery operation")
		}
		deliveries++
		return "wiki-recovered-request", nil
	})
	delivered, err := newSyncer.Flush(ctx, 1)
	if err != nil || delivered != 1 || deliveries != 1 {
		t.Fatalf("recovered knowledge queue delivery=%d deliveries=%d err=%v", delivered, deliveries, err)
	}
	if pending, countErr := restarted.QueueCount(ctx, projectID, "pending"); countErr != nil || pending != 0 {
		t.Fatalf("recovered queue remained pending: count=%d err=%v", pending, countErr)
	}
}

// TestSIGKILLQueueClaimChild is the controlled child used by the parent fault
// test below. It deliberately dies after ClaimQueue and before delivery.
func TestSIGKILLQueueClaimChild(t *testing.T) {
	if os.Getenv("BARON_SIGKILL_QUEUE_CHILD") != "1" {
		return
	}
	store, err := storage.Open(os.Getenv("BARON_SIGKILL_QUEUE_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	due, err := store.DueQueue(context.Background(), os.Getenv("BARON_SIGKILL_QUEUE_PROJECT"), 1)
	if err != nil || len(due) != 1 {
		t.Fatalf("child due queue=%#v err=%v", due, err)
	}
	claimed, err := store.ClaimQueue(context.Background(), due[0].QueueID)
	if err != nil || !claimed {
		t.Fatalf("child could not claim queue item: claimed=%v err=%v", claimed, err)
	}
	_, _ = fmt.Fprintln(os.Stdout, "baron-child-claimed")
	_ = os.Stdout.Sync()
	select {}
}

func TestSIGKILLAtRemoteDeliveryRecoversPendingQueueExactlyOnce(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "state.db")
	projectID := "prj-sigkill-12345678"
	ctx := context.Background()
	store, err := storage.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterProject(ctx, storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "sigkill"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := store.EnqueueSync(ctx, storage.QueueItem{ProjectID: projectID, Operation: storage.QueueOperationWikiIngest, IdempotencyKey: "sigkill-wiki-1", Payload: []byte(`{"project_id":"prj-sigkill-12345678","wiki_id":"wiki-1"}`)}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(os.Args[0], "-test.run=TestSIGKILLQueueClaimChild")
	command.Env = append(os.Environ(), "BARON_SIGKILL_QUEUE_CHILD=1", "BARON_SIGKILL_QUEUE_DB="+databasePath, "BARON_SIGKILL_QUEUE_PROJECT="+projectID)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	claimedSignal := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "baron-child-claimed" {
				claimedSignal <- true
				return
			}
		}
		claimedSignal <- false
	}()
	select {
	case claimed := <-claimedSignal:
		if !claimed {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatal("child exited before the controlled queue claim")
		}
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal("timed out waiting for the child queue claim")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("SIGKILL child unexpectedly exited successfully")
	}

	rawDB, err := sql.Open("sqlite", databasePath+"?_pragma=busy_timeout%3d5000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.ExecContext(ctx, `UPDATE sync_queue SET updated_at=? WHERE project_id=? AND idempotency_key=?`, time.Now().UTC().Add(-2*time.Minute).Format(time.RFC3339Nano), projectID, "sigkill-wiki-1"); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := storage.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	recovered, err := restarted.RecoverStaleQueueClaims(ctx, projectID, time.Now().UTC().Add(-time.Minute))
	if err != nil || recovered != 1 {
		t.Fatalf("SIGKILL queue lease was not recovered: recovered=%d err=%v", recovered, err)
	}
	deliveries := 0
	syncer := NewSyncer(restarted, nil, contracts.IsolationContext{ProjectID: projectID, TeamID: "team", AgentID: "agent", UserID: "user"}, nil)
	syncer.SetQueueOperationHandler(func(_ context.Context, item storage.QueueItem) (string, error) {
		if item.Operation != storage.QueueOperationWikiIngest {
			return "", errors.New("unexpected operation")
		}
		deliveries++
		return "wiki-sigkill-request", nil
	})
	delivered, err := syncer.Flush(ctx, 1)
	if err != nil || delivered != 1 || deliveries != 1 {
		t.Fatalf("recovered SIGKILL delivery=%d handler_calls=%d err=%v", delivered, deliveries, err)
	}
	if deliveredAgain, err := syncer.Flush(ctx, 1); err != nil || deliveredAgain != 0 || deliveries != 1 {
		t.Fatalf("recovered queue duplicated delivery=%d handler_calls=%d err=%v", deliveredAgain, deliveries, err)
	}
	items, err := restarted.ListQueue(ctx, projectID, "delivered", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("SIGKILL queue receipt state=%#v err=%v", items, err)
	}
	if receipt, err := restarted.GetQueueReceipt(ctx, items[0].QueueID); err != nil || receipt.RequestID != "wiki-sigkill-request" {
		t.Fatalf("SIGKILL delivery receipt=%#v err=%v", receipt, err)
	}
}
