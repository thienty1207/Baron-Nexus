package continuity

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/storage"
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
}
