package continuity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/storage"
)

type Syncer struct {
	store     *storage.Store
	backend   contracts.MemoryBackend
	isolation contracts.IsolationContext
	secrets   []string
	clock     func() time.Time
}

type queuedCapture struct {
	Isolation contracts.IsolationContext `json:"isolation"`
	Record    contracts.MemoryRecord     `json:"record"`
	Key       string                     `json:"idempotency_key"`
}

func NewSyncer(store *storage.Store, backend contracts.MemoryBackend, isolation contracts.IsolationContext, secrets []string) *Syncer {
	return &Syncer{store: store, backend: backend, isolation: isolation, secrets: append([]string(nil), secrets...), clock: time.Now}
}

func (s *Syncer) QueueCapture(ctx context.Context, record contracts.MemoryRecord, idempotencyKey string) (bool, error) {
	if s == nil || s.store == nil {
		return false, errors.New("memory syncer has no local store")
	}
	if idempotencyKey == "" {
		return false, errors.New("memory capture idempotency key is required")
	}
	if record.ProjectID != "" && record.ProjectID != s.isolation.ProjectID {
		return false, errors.New("memory record project identity mismatch")
	}
	record.ProjectID = s.isolation.ProjectID
	record = PrepareMemoryRecord(record, s.secrets)
	queuedIsolation := s.isolation
	if record.SessionID != "" {
		queuedIsolation.SessionID = record.SessionID
	}
	payload, err := json.Marshal(queuedCapture{Isolation: queuedIsolation, Record: record, Key: idempotencyKey})
	if err != nil {
		return false, err
	}
	return s.store.EnqueueSync(ctx, storage.QueueItem{ProjectID: s.isolation.ProjectID, IdempotencyKey: idempotencyKey, Payload: payload})
}

// QueueCaptureDuplicate is kept as a named testable alias to make duplicate
// delivery intent explicit at call sites; storage idempotency remains the
// authority.
func (s *Syncer) QueueCaptureDuplicate(ctx context.Context, record contracts.MemoryRecord, idempotencyKey string) error {
	_, err := s.QueueCapture(ctx, record, idempotencyKey)
	return err
}

func (s *Syncer) Flush(ctx context.Context, limit int) (int, error) {
	if s == nil || s.store == nil || s.backend == nil {
		return 0, errors.New("memory syncer is not configured")
	}
	items, err := s.store.DueQueue(ctx, s.isolation.ProjectID, limit)
	if err != nil {
		return 0, err
	}
	delivered := 0
	var lastErr error
	for _, item := range items {
		claimed, claimErr := s.store.ClaimQueue(ctx, item.QueueID)
		if claimErr != nil {
			lastErr = claimErr
			continue
		}
		if !claimed {
			continue
		}
		var queued queuedCapture
		if err := json.Unmarshal(item.Payload, &queued); err != nil {
			_ = s.store.MarkRetry(ctx, item.QueueID, "invalid queued payload", s.clock().UTC().Add(retryDelay(item.Attempts)))
			lastErr = fmt.Errorf("decode queue item %s: %w", item.QueueID, err)
			continue
		}
		if queued.Isolation.ProjectID != s.isolation.ProjectID || queued.Isolation.TeamID != s.isolation.TeamID || queued.Isolation.AgentID != s.isolation.AgentID || queued.Isolation.UserID != s.isolation.UserID {
			_ = s.store.MarkRetry(ctx, item.QueueID, "queued isolation context mismatch", s.clock().UTC().Add(retryDelay(item.Attempts)))
			lastErr = errors.New("queued isolation context mismatch")
			continue
		}
		receipt, captureErr := s.backend.Capture(ctx, queued.Isolation, queued.Record, queued.Key)
		if captureErr != nil {
			safeError := config.Redact(captureErr.Error(), s.secrets)
			_ = s.store.MarkRetry(ctx, item.QueueID, safeError, s.clock().UTC().Add(retryDelay(item.Attempts)))
			lastErr = errors.New(safeError)
			continue
		}
		if err := s.store.MarkDelivered(ctx, item.QueueID, receipt.RequestID); err != nil {
			lastErr = err
			continue
		}
		delivered++
	}
	return delivered, lastErr
}

func retryDelay(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 6 {
		attempts = 6
	}
	base := time.Duration(1<<attempts) * 25 * time.Millisecond
	// Small time-based jitter avoids synchronized retry storms while keeping
	// interactive retries bounded and deterministic enough for local tests.
	jitterWindow := base / 5
	if jitterWindow <= 0 {
		return base
	}
	return base + time.Duration(time.Now().UnixNano()%int64(jitterWindow+1))
}
