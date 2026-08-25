package continuity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/storage"
)

type Syncer struct {
	store            *storage.Store
	backend          contracts.MemoryBackend
	isolation        contracts.IsolationContext
	secrets          []string
	clock            func() time.Time
	flushBudget      time.Duration
	operationHandler func(context.Context, storage.QueueItem) (string, error)
}

// QueueOperationHandler handles non-memory typed operations such as Wiki
// ingest and CodeGraph sync. Keeping it optional preserves the core memory
// syncer contract for adapters that only know about L0 capture.
type QueueOperationHandler func(context.Context, storage.QueueItem) (string, error)

type queuedCapture struct {
	Isolation contracts.IsolationContext `json:"isolation"`
	Record    contracts.MemoryRecord     `json:"record"`
	Key       string                     `json:"idempotency_key"`
}

func NewSyncer(store *storage.Store, backend contracts.MemoryBackend, isolation contracts.IsolationContext, secrets []string) *Syncer {
	return &Syncer{store: store, backend: backend, isolation: isolation, secrets: append([]string(nil), secrets...), clock: time.Now, flushBudget: 5 * time.Second}
}

func (s *Syncer) SetQueueOperationHandler(handler QueueOperationHandler) {
	if s != nil {
		s.operationHandler = handler
	}
}

// SetFlushBudget bounds a repair/hook flush even when a provider keeps a
// connection open. A non-positive value restores the safe five-second default.
func (s *Syncer) SetFlushBudget(budget time.Duration) {
	if s == nil {
		return
	}
	if budget <= 0 {
		budget = 5 * time.Second
	}
	s.flushBudget = budget
}

// SetClock makes retry scheduling deterministic for local tests and keeps all
// queue timestamps sourced from the same clock as the rest of continuity.
func (s *Syncer) SetClock(clock func() time.Time) {
	if s != nil && clock != nil {
		s.clock = clock
	}
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
	return s.store.EnqueueSync(ctx, storage.QueueItem{ProjectID: s.isolation.ProjectID, IdempotencyKey: idempotencyKey, Operation: storage.QueueOperationMemoryCapture, Payload: payload})
}

// QueueCaptureDuplicate is kept as a named testable alias to make duplicate
// delivery intent explicit at call sites; storage idempotency remains the
// authority.
func (s *Syncer) QueueCaptureDuplicate(ctx context.Context, record contracts.MemoryRecord, idempotencyKey string) error {
	_, err := s.QueueCapture(ctx, record, idempotencyKey)
	return err
}

// QueueOperation persists a typed non-memory operation with the same project
// isolation and idempotency guarantees as memory capture. The operation is
// intentionally allowlisted so adapters cannot smuggle arbitrary HTTP work
// into the repair queue.
func (s *Syncer) QueueOperation(ctx context.Context, operation, idempotencyKey string, payload map[string]any) (bool, error) {
	if s == nil || s.store == nil {
		return false, errors.New("memory syncer has no local store")
	}
	if !isQueueOperation(operation) || operation == storage.QueueOperationMemoryCapture {
		return false, fmt.Errorf("unsupported typed queue operation %q", operation)
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return false, errors.New("typed queue idempotency key is required")
	}
	copyPayload := make(map[string]any, len(payload)+4)
	for key, value := range payload {
		copyPayload[key] = value
	}
	copyPayload["project_id"] = s.isolation.ProjectID
	copyPayload["team_id"] = s.isolation.TeamID
	copyPayload["agent_id"] = s.isolation.AgentID
	copyPayload["user_id"] = s.isolation.UserID
	encoded, err := json.Marshal(copyPayload)
	if err != nil {
		return false, err
	}
	encoded = []byte(config.Redact(string(encoded), s.secrets))
	return s.store.EnqueueSync(ctx, storage.QueueItem{ProjectID: s.isolation.ProjectID, Operation: operation, IdempotencyKey: idempotencyKey, Payload: encoded})
}

func (s *Syncer) Flush(ctx context.Context, limit int) (int, error) {
	if s == nil || s.store == nil || (s.backend == nil && s.operationHandler == nil) {
		return 0, errors.New("memory syncer is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	budget := s.flushBudget
	if budget <= 0 {
		budget = 5 * time.Second
	}
	flushCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if _, err := s.store.RequeueOversizedMemoryCaptures(flushCtx, s.isolation.ProjectID); err != nil {
		return 0, fmt.Errorf("repair oversized memory queue: %w", err)
	}
	items, err := s.store.DueQueue(flushCtx, s.isolation.ProjectID, limit)
	if err != nil {
		return 0, err
	}
	delivered := 0
	var lastErr error
	for _, item := range items {
		if err := flushCtx.Err(); err != nil {
			return delivered, err
		}
		claimed, claimErr := s.store.ClaimQueue(flushCtx, item.QueueID)
		if claimErr != nil {
			lastErr = claimErr
			continue
		}
		if !claimed {
			continue
		}
		if item.Operation != "" && item.Operation != storage.QueueOperationMemoryCapture {
			if s.operationHandler == nil {
				lastErr = s.retryQueueItem(flushCtx, item, "typed queue operation has no handler", false)
				continue
			}
			requestID, operationErr := s.operationHandler(flushCtx, item)
			if operationErr != nil {
				safeError := config.Redact(operationErr.Error(), s.secrets)
				lastErr = s.retryQueueItem(flushCtx, item, safeError, false)
				continue
			}
			if err := s.store.MarkDelivered(flushCtx, item.QueueID, requestID); err != nil {
				lastErr = err
				continue
			}
			delivered++
			continue
		}
		var queued queuedCapture
		if err := json.Unmarshal(item.Payload, &queued); err != nil {
			_ = s.store.MarkDeadLetter(flushCtx, item.QueueID, "invalid queued payload")
			lastErr = fmt.Errorf("decode queue item %s: %w", item.QueueID, err)
			continue
		}
		if queued.Isolation.ProjectID != s.isolation.ProjectID || queued.Isolation.TeamID != s.isolation.TeamID || queued.Isolation.AgentID != s.isolation.AgentID || queued.Isolation.UserID != s.isolation.UserID {
			_ = s.store.MarkDeadLetter(flushCtx, item.QueueID, "queued isolation context mismatch")
			lastErr = errors.New("queued isolation context mismatch")
			continue
		}
		receipt, captureErr := s.backend.Capture(flushCtx, queued.Isolation, queued.Record, queued.Key)
		if captureErr != nil {
			safeError := config.Redact(captureErr.Error(), s.secrets)
			lastErr = s.retryQueueItem(flushCtx, item, safeError, false)
			continue
		}
		if err := s.store.MarkDelivered(flushCtx, item.QueueID, receipt.RequestID); err != nil {
			lastErr = err
			continue
		}
		if syncErr := s.store.MarkMemorySync(flushCtx, s.isolation.ProjectID, s.clock().UTC()); syncErr != nil {
			lastErr = syncErr
		}
		delivered++
	}
	return delivered, lastErr
}

type retryPolicy struct {
	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration
}

func policyForOperation(operation string) retryPolicy {
	switch operation {
	case storage.QueueOperationCodeGraphSync:
		return retryPolicy{maxAttempts: 6, baseDelay: 100 * time.Millisecond, maxDelay: 5 * time.Second}
	case storage.QueueOperationWikiIngest, storage.QueueOperationSkillUpdate:
		return retryPolicy{maxAttempts: 5, baseDelay: 75 * time.Millisecond, maxDelay: 3 * time.Second}
	case storage.QueueOperationMetadataRepair:
		return retryPolicy{maxAttempts: 4, baseDelay: 75 * time.Millisecond, maxDelay: 2 * time.Second}
	case storage.QueueOperationCoreUpdate, storage.QueueOperationScenarioUpdate:
		return retryPolicy{maxAttempts: 5, baseDelay: 50 * time.Millisecond, maxDelay: 3 * time.Second}
	default:
		return retryPolicy{maxAttempts: 5, baseDelay: 25 * time.Millisecond, maxDelay: 2 * time.Second}
	}
}

func isQueueOperation(operation string) bool {
	switch operation {
	case storage.QueueOperationCoreUpdate, storage.QueueOperationScenarioUpdate,
		storage.QueueOperationWikiIngest, storage.QueueOperationCodeGraphSync,
		storage.QueueOperationSkillUpdate, storage.QueueOperationMetadataRepair:
		return true
	default:
		return false
	}
}

func (s *Syncer) retryQueueItem(ctx context.Context, item storage.QueueItem, message string, permanent bool) error {
	policy := policyForOperation(item.Operation)
	if permanent || item.Attempts+1 >= policy.maxAttempts {
		if err := s.store.MarkDeadLetter(ctx, item.QueueID, message); err != nil {
			return fmt.Errorf("mark queue item dead-letter: %w", err)
		}
		return fmt.Errorf("queue item %s moved to dead-letter after %d attempts: %s", item.QueueID, item.Attempts+1, message)
	}
	now := s.clock().UTC()
	next := now.Add(retryDelay(item.Attempts, policy, now))
	if err := s.store.MarkRetry(ctx, item.QueueID, message, next); err != nil {
		return fmt.Errorf("schedule queue retry: %w", err)
	}
	return errors.New(message)
}

func retryDelay(attempts int, policy retryPolicy, now time.Time) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 10 {
		attempts = 10
	}
	base := time.Duration(1<<attempts) * policy.baseDelay
	if base > policy.maxDelay {
		base = policy.maxDelay
	}
	// Small time-based jitter avoids synchronized retry storms while keeping
	// interactive retries bounded and deterministic enough for local tests.
	jitterWindow := base / 5
	if jitterWindow <= 0 {
		return base
	}
	return base + time.Duration(now.UnixNano()%int64(jitterWindow+1))
}
