package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type RemoteRecallCache struct {
	ProjectID   string
	SessionID   string
	Fingerprint string
	QueryHash   string
	Snapshot    []byte
	ReceiptID   string
	UpdatedAt   time.Time
}

func (s *Store) PutRemoteRecallCache(ctx context.Context, cache RemoteRecallCache) error {
	if strings.TrimSpace(cache.ProjectID) == "" || strings.TrimSpace(cache.Fingerprint) == "" || strings.TrimSpace(cache.QueryHash) == "" {
		return errors.New("remote recall cache project, fingerprint, and query hash are required")
	}
	if len(cache.Snapshot) == 0 || !json.Valid(cache.Snapshot) {
		return errors.New("remote recall cache snapshot must be valid JSON")
	}
	if cache.UpdatedAt.IsZero() {
		cache.UpdatedAt = time.Now().UTC()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO remote_recall_cache(project_id, session_id, fingerprint, query_hash, snapshot, receipt_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, session_id, fingerprint, query_hash) DO UPDATE SET
		 snapshot=excluded.snapshot, receipt_id=excluded.receipt_id, updated_at=excluded.updated_at`,
		cache.ProjectID, cache.SessionID, cache.Fingerprint, cache.QueryHash, cache.Snapshot,
		cache.ReceiptID, cache.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("write remote recall cache: %w", err)
	}
	return nil
}

func (s *Store) GetRemoteRecallCache(ctx context.Context, projectID, sessionID, fingerprint, queryHash string) (RemoteRecallCache, error) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(fingerprint) == "" || strings.TrimSpace(queryHash) == "" {
		return RemoteRecallCache{}, errors.New("remote recall cache project, fingerprint, and query hash are required")
	}
	var cache RemoteRecallCache
	var updated string
	err := s.db.QueryRowContext(ctx, `SELECT project_id, session_id, fingerprint, query_hash, snapshot, receipt_id, updated_at
		FROM remote_recall_cache WHERE project_id=? AND session_id=? AND fingerprint=? AND query_hash=?`,
		projectID, sessionID, fingerprint, queryHash).Scan(
		&cache.ProjectID, &cache.SessionID, &cache.Fingerprint, &cache.QueryHash,
		&cache.Snapshot, &cache.ReceiptID, &updated)
	if err != nil {
		return RemoteRecallCache{}, err
	}
	cache.UpdatedAt = parseTime(updated)
	return cache, nil
}

func IsMissingRemoteRecallCache(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
