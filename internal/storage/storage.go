package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/baron-shared-brain/baron/internal/contracts"
	_ "modernc.org/sqlite"
)

const currentSchemaVersion = 2

type Store struct {
	db      *sql.DB
	writeMu sync.Mutex
}

type ProjectRecord struct {
	ProjectID string
	Root      string
	Name      string
	CreatedAt time.Time
}

type Event struct {
	EventID        string
	ProjectID      string
	SessionID      string
	Client         contracts.HookClient
	Type           contracts.EventType
	OccurredAt     time.Time
	Payload        json.RawMessage
	PayloadHash    string
	IdempotencyKey string
}

type Session struct {
	SessionID          string
	ProjectID          string
	Client             contracts.HookClient
	StartedAt          time.Time
	LastSeenAt         time.Time
	State              contracts.SessionState
	InterruptionReason string
}

type QueueItem struct {
	QueueID        string
	ProjectID      string
	IdempotencyKey string
	Payload        []byte
	Status         string
	Attempts       int
	NextRetryAt    time.Time
	LastError      string
	CreatedAt      time.Time
}

type HandoffReceipt struct {
	ReceiptID       string
	ProjectID       string
	SourceClient    contracts.HookClient
	TargetClient    contracts.HookClient
	SourceSessionID string
	TargetSessionID string
	CheckpointID    string
	CreatedAt       time.Time
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("state database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state database directory: %w", err)
	}
	dsn := path
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=busy_timeout%3d5000"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	for _, pragma := range []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL"} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure SQLite (%s): %w", pragma, err)
		}
	}
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_meta (schema_version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema metadata: %w", err)
	}
	var version int
	err = tx.QueryRowContext(ctx, `SELECT schema_version FROM schema_meta LIMIT 1`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		version = 0
	} else if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("database schema %d is newer than supported schema %d", version, currentSchemaVersion)
	}
	if version == 0 {
		for _, statement := range schemaStatements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply schema migration: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_meta(schema_version) VALUES (?)`, currentSchemaVersion); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	} else if version == 1 {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS handoff_receipts (
			receipt_id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(project_id) ON DELETE CASCADE,
			source_client TEXT NOT NULL,
			target_client TEXT NOT NULL,
			source_session_id TEXT NOT NULL,
			target_session_id TEXT NOT NULL,
			checkpoint_id TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`); err != nil {
			return fmt.Errorf("apply handoff receipt migration: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET schema_version=?`, currentSchemaVersion); err != nil {
			return fmt.Errorf("record handoff receipt migration: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema migration: %w", err)
	}
	return nil
}

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS projects (
		project_id TEXT PRIMARY KEY,
		root TEXT NOT NULL,
		name TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS sessions (
		session_id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES projects(project_id) ON DELETE CASCADE,
		client TEXT NOT NULL,
		started_at TEXT NOT NULL,
		last_seen_at TEXT NOT NULL,
		state TEXT NOT NULL,
		interruption_reason TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS events (
		event_id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES projects(project_id) ON DELETE CASCADE,
		session_id TEXT NOT NULL DEFAULT '',
		client TEXT NOT NULL,
		event_type TEXT NOT NULL,
		occurred_at TEXT NOT NULL,
		payload BLOB NOT NULL,
		payload_hash TEXT NOT NULL,
		idempotency_key TEXT NOT NULL,
		created_at TEXT NOT NULL,
		UNIQUE(project_id, idempotency_key)
	)`,
	`CREATE INDEX IF NOT EXISTS events_project_time ON events(project_id, occurred_at)`,
	`CREATE TABLE IF NOT EXISTS work_state (
		project_id TEXT PRIMARY KEY REFERENCES projects(project_id) ON DELETE CASCADE,
		state_json BLOB NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS sync_queue (
		queue_id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES projects(project_id) ON DELETE CASCADE,
		idempotency_key TEXT NOT NULL,
		payload BLOB NOT NULL,
		status TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		next_retry_at TEXT,
		last_error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(project_id, idempotency_key)
	)`,
	`CREATE INDEX IF NOT EXISTS sync_queue_due ON sync_queue(project_id, status, next_retry_at)`,
	`CREATE TABLE IF NOT EXISTS memory_receipts (
		idempotency_key TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES projects(project_id) ON DELETE CASCADE,
		request_id TEXT NOT NULL,
		delivered_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS locks (
		lock_name TEXT PRIMARY KEY,
		owner TEXT NOT NULL,
		acquired_at TEXT NOT NULL,
		expires_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS handoff_receipts (
		receipt_id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES projects(project_id) ON DELETE CASCADE,
		source_client TEXT NOT NULL,
		target_client TEXT NOT NULL,
		source_session_id TEXT NOT NULL,
		target_session_id TEXT NOT NULL,
		checkpoint_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		UNIQUE(project_id, source_session_id, target_session_id)
	)`,
}

func (s *Store) RegisterProject(ctx context.Context, project ProjectRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if strings.TrimSpace(project.ProjectID) == "" {
		return errors.New("project ID is required")
	}
	now := time.Now().UTC()
	if project.CreatedAt.IsZero() {
		project.CreatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO projects(project_id, root, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET root=excluded.root, name=excluded.name, updated_at=excluded.updated_at`,
		project.ProjectID, project.Root, project.Name, project.CreatedAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("register project: %w", err)
	}
	return nil
}

func (s *Store) InsertEvent(ctx context.Context, event Event) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if event.ProjectID == "" {
		return false, errors.New("event project ID is required")
	}
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
		sum := sha256.Sum256(event.Payload)
		event.PayloadHash = hex.EncodeToString(sum[:])
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO events(event_id, project_id, session_id, client, event_type, occurred_at, payload, payload_hash, idempotency_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, idempotency_key) DO NOTHING`,
		event.EventID, event.ProjectID, event.SessionID, event.Client, event.Type,
		event.OccurredAt.UTC().Format(time.RFC3339Nano), []byte(event.Payload), event.PayloadHash,
		event.IdempotencyKey, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("insert event: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (s *Store) CountEvents(ctx context.Context, projectID string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE project_id=?`, projectID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count events: %w", err)
	}
	return count, nil
}

func (s *Store) StartSession(ctx context.Context, session Session) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if session.SessionID == "" {
		session.SessionID = newID("ses")
	}
	if session.StartedAt.IsZero() {
		session.StartedAt = time.Now().UTC()
	}
	if session.LastSeenAt.IsZero() {
		session.LastSeenAt = session.StartedAt
	}
	if session.State == "" {
		session.State = contracts.SessionActive
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(session_id, project_id, client, started_at, last_seen_at, state, interruption_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET last_seen_at=excluded.last_seen_at, state=excluded.state, interruption_reason=excluded.interruption_reason`,
		session.SessionID, session.ProjectID, session.Client, session.StartedAt.UTC().Format(time.RFC3339Nano),
		session.LastSeenAt.UTC().Format(time.RFC3339Nano), session.State, session.InterruptionReason)
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	return nil
}

func (s *Store) UpdateSession(ctx context.Context, sessionID string, state contracts.SessionState, reason string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET state=?, interruption_reason=?, last_seen_at=? WHERE session_id=?`, state, reason, time.Now().UTC().Format(time.RFC3339Nano), sessionID)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	return nil
}

func (s *Store) PutWorkState(ctx context.Context, projectID string, state []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if projectID == "" {
		return errors.New("work state project ID is required")
	}
	if !json.Valid(state) {
		return errors.New("work state must be valid JSON")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO work_state(project_id, state_json, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET state_json=excluded.state_json, updated_at=excluded.updated_at`,
		projectID, state, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("write work state: %w", err)
	}
	return nil
}

func (s *Store) GetWorkState(ctx context.Context, projectID string) ([]byte, error) {
	var state []byte
	if err := s.db.QueryRowContext(ctx, `SELECT state_json FROM work_state WHERE project_id=?`, projectID).Scan(&state); err != nil {
		return nil, err
	}
	return state, nil
}

func (s *Store) EnqueueSync(ctx context.Context, item QueueItem) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if item.ProjectID == "" || item.IdempotencyKey == "" {
		return false, errors.New("queue project ID and idempotency key are required")
	}
	if item.QueueID == "" {
		item.QueueID = newID("que")
	}
	if item.Status == "" {
		item.Status = "pending"
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sync_queue(queue_id, project_id, idempotency_key, payload, status, attempts, next_retry_at, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, idempotency_key) DO NOTHING`,
		item.QueueID, item.ProjectID, item.IdempotencyKey, item.Payload, item.Status, item.Attempts,
		nullableTime(item.NextRetryAt), item.LastError, item.CreatedAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("enqueue sync: %w", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_queue WHERE project_id=? AND queue_id=?`, item.ProjectID, item.QueueID).Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}

func (s *Store) DueQueue(ctx context.Context, projectID string, limit int) ([]QueueItem, error) {
	if limit <= 0 {
		limit = 1
	}
	// A process killed after claiming a queue item leaves it in sending. The
	// bounded lease makes such work eligible again without allowing two live
	// workers to claim the same item.
	s.writeMu.Lock()
	_, recoverErr := s.db.ExecContext(ctx, `UPDATE sync_queue SET status='pending', updated_at=? WHERE project_id=? AND status='sending' AND updated_at <= ?`,
		time.Now().UTC().Format(time.RFC3339Nano), projectID, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano))
	s.writeMu.Unlock()
	if recoverErr != nil {
		return nil, fmt.Errorf("recover stale queue claims: %w", recoverErr)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx, `SELECT queue_id, project_id, idempotency_key, payload, status, attempts, COALESCE(next_retry_at, ''), last_error, created_at
		FROM sync_queue WHERE project_id=? AND status='pending' AND (next_retry_at IS NULL OR next_retry_at <= ?) ORDER BY created_at LIMIT ?`, projectID, now, limit)
	if err != nil {
		return nil, fmt.Errorf("read sync queue: %w", err)
	}
	defer rows.Close()
	var items []QueueItem
	for rows.Next() {
		var item QueueItem
		var nextRetry, created string
		if err := rows.Scan(&item.QueueID, &item.ProjectID, &item.IdempotencyKey, &item.Payload, &item.Status, &item.Attempts, &nextRetry, &item.LastError, &created); err != nil {
			return nil, err
		}
		item.NextRetryAt = parseTime(nextRetry)
		item.CreatedAt = parseTime(created)
		items = append(items, item)
	}
	return items, rows.Err()
}

// ClaimQueue atomically moves a pending item into a short-lived sending lease.
// A false result means another process already owns or delivered the item.
func (s *Store) ClaimQueue(ctx context.Context, queueID string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	result, err := s.db.ExecContext(ctx, `UPDATE sync_queue SET status='sending', updated_at=? WHERE queue_id=? AND status='pending'`, time.Now().UTC().Format(time.RFC3339Nano), queueID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s *Store) MarkDelivered(ctx context.Context, queueID, requestID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID, idempotency string
	if err := tx.QueryRowContext(ctx, `SELECT project_id, idempotency_key FROM sync_queue WHERE queue_id=?`, queueID).Scan(&projectID, &idempotency); err != nil {
		return fmt.Errorf("find queue item: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sync_queue SET status='delivered', updated_at=?, last_error='' WHERE queue_id=?`, time.Now().UTC().Format(time.RFC3339Nano), queueID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_receipts(idempotency_key, project_id, request_id, delivered_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(idempotency_key) DO UPDATE SET request_id=excluded.request_id, delivered_at=excluded.delivered_at`, idempotency, projectID, requestID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkRetry(ctx context.Context, queueID, message string, next time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE sync_queue SET status='pending', attempts=attempts+1, next_retry_at=?, last_error=?, updated_at=? WHERE queue_id=?`, next.UTC().Format(time.RFC3339Nano), message, time.Now().UTC().Format(time.RFC3339Nano), queueID)
	return err
}

func (s *Store) QueueCount(ctx context.Context, projectID, status string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_queue WHERE project_id=? AND status=?`, projectID, status).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) RecordHandoff(ctx context.Context, receipt HandoffReceipt) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if receipt.ReceiptID == "" {
		receipt.ReceiptID = newID("handoff")
	}
	if receipt.CreatedAt.IsZero() {
		receipt.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO handoff_receipts(receipt_id, project_id, source_client, target_client, source_session_id, target_session_id, checkpoint_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, source_session_id, target_session_id) DO NOTHING`,
		receipt.ReceiptID, receipt.ProjectID, receipt.SourceClient, receipt.TargetClient,
		receipt.SourceSessionID, receipt.TargetSessionID, receipt.CheckpointID, receipt.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("record handoff receipt: %w", err)
	}
	return nil
}

func (s *Store) HandoffCount(ctx context.Context, projectID string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM handoff_receipts WHERE project_id=?`, projectID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func newID(prefix string) string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(bytes)
}

// NewID creates a random, opaque local identifier for sessions, events, and
// queue items. It is exported so hook adapters do not invent their own ID
// format.
func NewID(prefix string) string {
	return newID(prefix)
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}
