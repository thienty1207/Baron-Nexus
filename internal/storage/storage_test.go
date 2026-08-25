package storage

import (
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
)

func TestSQLiteJournalIsWALAndDuplicateSafe(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: "prj-a-12345678", Root: "/tmp/a", Name: "A"}); err != nil {
		t.Fatal(err)
	}
	event := Event{
		EventID: "evt-1", ProjectID: "prj-a-12345678", SessionID: "ses-1",
		Client: contracts.ClientCodex, Type: contracts.EventToolFinished,
		OccurredAt: time.Now().UTC(), Payload: json.RawMessage(`{"command":"go test ./..."}`),
		IdempotencyKey: "idem-1",
	}
	const deliveries = 100
	var wg sync.WaitGroup
	inserted := make(chan bool, deliveries)
	for i := 0; i < deliveries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, insertErr := store.InsertEvent(ctx, event)
			if insertErr != nil {
				t.Errorf("insert event: %v", insertErr)
				return
			}
			inserted <- ok
		}()
	}
	wg.Wait()
	close(inserted)
	insertedCount := 0
	for value := range inserted {
		if value {
			insertedCount++
		}
	}
	if insertedCount != 1 {
		t.Fatalf("expected exactly one inserted duplicate event, got %d", insertedCount)
	}
	count, err := store.CountEvents(ctx, "prj-a-12345678")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one event, got %d", count)
	}
	var journalMode, foreignKeys string
	if err := store.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" || foreignKeys != "1" {
		t.Fatalf("SQLite durability settings incorrect: journal=%s foreign_keys=%s", journalMode, foreignKeys)
	}
}

func TestQueueIsDurableAndTransitionsExactlyOnce(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	projectID := "prj-queue-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: "/tmp/q", Name: "queue"}); err != nil {
		t.Fatal(err)
	}
	first, err := store.EnqueueSync(ctx, QueueItem{ProjectID: projectID, IdempotencyKey: "memory-1", Operation: QueueOperationWikiIngest, Payload: []byte("redacted")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.EnqueueSync(ctx, QueueItem{ProjectID: projectID, IdempotencyKey: "memory-1", Payload: []byte("redacted")})
	if err != nil {
		t.Fatal(err)
	}
	if !first || second {
		t.Fatalf("queue idempotency result wrong: first=%v second=%v", first, second)
	}
	due, err := store.DueQueue(ctx, projectID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("expected one due queue item, got %d", len(due))
	}
	if due[0].Operation != QueueOperationWikiIngest {
		t.Fatalf("typed queue operation was not durable: %#v", due[0])
	}
	if err := store.MarkDelivered(ctx, due[0].QueueID, "remote-1"); err != nil {
		t.Fatal(err)
	}
	due, err = store.DueQueue(ctx, projectID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("delivered item remained due: %#v", due)
	}
	count, err := store.QueueCount(ctx, projectID, "delivered")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one delivered item, got %d", count)
	}
}

func TestConcurrentEventsRemainProjectScoped(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, projectID := range []string{"prj-a-12345678", "prj-b-12345678"} {
		if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: "/tmp/" + projectID, Name: projectID}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for projectIndex := 0; projectIndex < 2; projectIndex++ {
		projectID := fmt.Sprintf("prj-%c-12345678", 'a'+projectIndex)
		for index := 0; index < 100; index++ {
			wg.Add(1)
			go func(projectID string, index int) {
				defer wg.Done()
				_, insertErr := store.InsertEvent(ctx, Event{
					EventID: fmt.Sprintf("evt-%s-%d", projectID, index), ProjectID: projectID,
					SessionID: "session", Client: contracts.ClientDSH, Type: contracts.EventFileChanged,
					OccurredAt: time.Now().UTC(), Payload: json.RawMessage(`{"file":"x"}`),
					IdempotencyKey: fmt.Sprintf("%s-%d", projectID, index),
				})
				if insertErr != nil {
					t.Errorf("insert %s/%d: %v", projectID, index, insertErr)
				}
			}(projectID, index)
		}
	}
	wg.Wait()
	for _, projectID := range []string{"prj-a-12345678", "prj-b-12345678"} {
		count, countErr := store.CountEvents(ctx, projectID)
		if countErr != nil {
			t.Fatal(countErr)
		}
		if count != 100 {
			t.Fatalf("project %s event count=%d", projectID, count)
		}
	}
}

func TestConcurrentQueueClaimsSelectOneDeliveryOwner(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	projectID := "prj-claim-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: "/tmp/claim", Name: "claim"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueSync(ctx, QueueItem{ProjectID: projectID, IdempotencyKey: "claim-1", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	due, err := store.DueQueue(ctx, projectID, 1)
	if err != nil || len(due) != 1 {
		t.Fatalf("due queue=%#v err=%v", due, err)
	}
	var wg sync.WaitGroup
	claimed := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, claimErr := store.ClaimQueue(ctx, due[0].QueueID)
			if claimErr != nil {
				t.Errorf("claim queue: %v", claimErr)
			}
			claimed <- ok
		}()
	}
	wg.Wait()
	close(claimed)
	owners := 0
	for ok := range claimed {
		if ok {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("expected one queue owner, got %d", owners)
	}
}

func TestStaleQueueClaimCanBeRecoveredWithoutTouchingActiveWork(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	projectID := "prj-queue-recovery-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: "/tmp/queue-recovery", Name: "queue-recovery"}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"stale-1", "active-1"} {
		if _, err := store.EnqueueSync(ctx, QueueItem{ProjectID: projectID, IdempotencyKey: key, Payload: []byte(`{"project_id":"prj-queue-recovery-12345678"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	due, err := store.DueQueue(ctx, projectID, 10)
	if err != nil || len(due) != 2 {
		t.Fatalf("due queue=%#v err=%v", due, err)
	}
	for _, item := range due {
		claimed, claimErr := store.ClaimQueue(ctx, item.QueueID)
		if claimErr != nil || !claimed {
			t.Fatalf("claim %s=%v err=%v", item.IdempotencyKey, claimed, claimErr)
		}
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE sync_queue SET updated_at=? WHERE idempotency_key=?`, time.Now().UTC().Add(-2*time.Minute).Format(time.RFC3339Nano), "stale-1"); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.RecoverStaleQueueClaims(ctx, projectID, time.Now().UTC().Add(-time.Minute))
	if err != nil || recovered != 1 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	items, err := store.ListQueue(ctx, projectID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, item := range items {
		statuses[item.IdempotencyKey] = item.Status
	}
	if statuses["stale-1"] != "pending" || statuses["active-1"] != "sending" {
		t.Fatalf("stale recovery touched the wrong lease: %#v", statuses)
	}
}

func TestRequeueOversizedMemoryCapturesLeavesOtherDeadLettersUntouched(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	projectID := "prj-queue-repair-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: "/tmp/queue-repair", Name: "queue-repair"}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"oversized", "poison"} {
		if _, err := store.EnqueueSync(ctx, QueueItem{ProjectID: projectID, IdempotencyKey: key, Payload: []byte(`{"record":"preserve"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.ListQueue(ctx, projectID, "pending", 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("pending queue=%#v err=%v", items, err)
	}
	if err := store.MarkDeadLetter(ctx, items[0].QueueID, `Tencent request POST /v3/conversation/add failed with HTTP 400: {"message":"messages.0.content: Too big: expected string to have <=8192 characters"}`); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeadLetter(ctx, items[1].QueueID, "invalid queued payload"); err != nil {
		t.Fatal(err)
	}
	requeued, err := store.RequeueOversizedMemoryCaptures(ctx, projectID)
	if err != nil || requeued != 1 {
		t.Fatalf("requeued=%d err=%v", requeued, err)
	}
	remaining, err := store.ListQueue(ctx, projectID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	statusByKey := map[string]QueueItem{}
	for _, item := range remaining {
		statusByKey[item.IdempotencyKey] = item
	}
	if statusByKey["oversized"].Status != "pending" || statusByKey["oversized"].Attempts != 0 || statusByKey["oversized"].LastError != "" {
		t.Fatalf("oversized item was not reset for repair: %#v", statusByKey["oversized"])
	}
	if statusByKey["poison"].Status != "dead_letter" {
		t.Fatalf("non-size dead letter was requeued: %#v", statusByKey["poison"])
	}
}

func TestHandoffReceiptIsDurableAndProjectScoped(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	projectID := "prj-handoff-receipt-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: "/tmp/handoff", Name: "handoff"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordHandoff(ctx, HandoffReceipt{ReceiptID: "handoff-1", ProjectID: projectID, SourceClient: contracts.ClientCodex, TargetClient: contracts.ClientDSH, SourceSessionID: "old", TargetSessionID: "new", CheckpointID: "checkpoint-1"}); err != nil {
		t.Fatal(err)
	}
	count, err := store.HandoffCount(ctx, projectID)
	if err != nil || count != 1 {
		t.Fatalf("handoff count=%d err=%v", count, err)
	}
}

func TestKnowledgeRegistryIsDurableAndProjectScoped(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	projectID := "prj-knowledge-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: "/tmp/knowledge", Name: "knowledge"}); err != nil {
		t.Fatal(err)
	}
	want := KnowledgeRegistry{
		ProjectID: projectID, TeamID: "team-a", UserID: "user-a", AgentID: "agent-a",
		WikiID: "wiki-a", CodeGraphID: "graph-a", WikiMetadataID: "meta-wiki",
		CodeGraphMetadataID: "meta-graph", ServiceURL: "http://knowledge/v3",
		Repository: "https://example.com/repo.git", Branch: "main", LastSyncCommit: "abc123",
		WikiStatus: "ready", CodeGraphStatus: "ready", WikiIngestStatus: "ready",
		CodeGraphSyncStatus: "ready", WikiIngestVersion: "wiki-v2", CodeGraphCommit: "abc123",
		LastMemorySyncAt: time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC), ConflictStatus: "none", SupersededBy: "",
		LastError: "",
	}
	if err := store.UpsertKnowledgeRegistry(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetKnowledgeRegistry(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID != want.ProjectID || got.WikiID != want.WikiID || got.CodeGraphID != want.CodeGraphID || got.LastSyncCommit != want.LastSyncCommit || got.Repository != want.Repository || got.WikiIngestVersion != want.WikiIngestVersion || got.CodeGraphCommit != want.CodeGraphCommit || !got.LastMemorySyncAt.Equal(want.LastMemorySyncAt) || got.ConflictStatus != want.ConflictStatus {
		t.Fatalf("registry round trip mismatch: got=%#v want=%#v", got, want)
	}
	if err := store.UpsertKnowledgeRegistry(ctx, KnowledgeRegistry{ProjectID: projectID, TeamID: "team-a", UserID: "user-a", AgentID: "agent-a", WikiID: "wiki-new"}); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetKnowledgeRegistry(ctx, projectID)
	if err != nil || got.WikiID != "wiki-new" {
		t.Fatalf("registry upsert did not replace mapping: got=%#v err=%v", got, err)
	}
}

func TestSQLiteMultiProcessTenThousandEvents(t *testing.T) {
	if os.Getenv("BARON_STORAGE_HELPER") == "1" {
		t.Helper()
		path := os.Getenv("BARON_STORAGE_DB")
		projectID := os.Getenv("BARON_STORAGE_PROJECT")
		worker := os.Getenv("BARON_STORAGE_WORKER")
		store, err := openWithRetry(path)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		for index := 0; index < 1000; index++ {
			if _, err := store.InsertEvent(context.Background(), Event{
				EventID: fmt.Sprintf("evt-%s-%d", worker, index), ProjectID: projectID, SessionID: "session-" + worker,
				Client: contracts.ClientCodex, Type: contracts.EventToolFinished, OccurredAt: time.Now().UTC(),
				Payload: json.RawMessage(`{"command":"go test"}`), IdempotencyKey: fmt.Sprintf("multi-%s-%d", worker, index),
			}); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	path := filepath.Join(t.TempDir(), "state.db")
	projectID := "prj-multiprocess-12345678"
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterProject(context.Background(), ProjectRecord{ProjectID: projectID, Root: "/tmp/multiprocess", Name: "multiprocess"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	commands := make([]*exec.Cmd, 0, 10)
	for worker := 0; worker < 10; worker++ {
		command := exec.Command(os.Args[0], "-test.run=TestSQLiteMultiProcessTenThousandEvents", "--")
		command.Env = append(os.Environ(), "BARON_STORAGE_HELPER=1", "BARON_STORAGE_DB="+path, "BARON_STORAGE_PROJECT="+projectID, fmt.Sprintf("BARON_STORAGE_WORKER=%d", worker))
		commands = append(commands, command)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("multi-process writer failed: %v", err)
		}
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	count, err := store.CountEvents(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 10000 {
		t.Fatalf("multi-process event count=%d want 10000", count)
	}
}

func openWithRetry(path string) (*Store, error) {
	var lastErr error
	for attempt := 0; attempt < 40; attempt++ {
		store, err := Open(path)
		if err == nil {
			return store, nil
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	return nil, lastErr
}
