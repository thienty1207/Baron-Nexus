package continuity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const checkpointLockStaleAfter = 5 * time.Minute

// withCheckpointLock protects the SQLite-to-checkpoint materialization
// boundary across separate Baron hook processes. O_CREATE|O_EXCL gives us a
// small portable lock primitive without requiring a platform-specific daemon;
// stale locks are recoverable after an interrupted process.
func (e *Engine) withCheckpointLock(ctx context.Context, fn func() error) error {
	if e == nil || e.checkpointPath == "" {
		return fn()
	}
	lockDir := filepath.Join(filepath.Dir(e.checkpointPath), "runtime", "locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return fmt.Errorf("create checkpoint lock directory: %w", err)
	}
	lockPath := filepath.Join(lockDir, "checkpoint.lock")
	for {
		file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, _ = file.WriteString(strconv.Itoa(os.Getpid()) + "\n")
			_ = file.Sync()
			closeErr := file.Close()
			if closeErr != nil {
				_ = os.Remove(lockPath)
				return closeErr
			}
			defer os.Remove(lockPath)
			return fn()
		}
		if !os.IsExist(err) {
			return fmt.Errorf("create checkpoint lock: %w", err)
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) >= checkpointLockStaleAfter {
			if removeErr := os.Remove(lockPath); removeErr == nil {
				continue
			}
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
