package continuity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const checkpointLockStaleAfter = 5 * time.Minute

var checkpointProcessMu sync.Mutex

// withCheckpointLock protects the SQLite-to-checkpoint materialization
// boundary across separate Baron hook processes. O_CREATE|O_EXCL gives us a
// small portable lock primitive without requiring a platform-specific daemon;
// stale locks are recoverable after an interrupted process.
func (e *Engine) withCheckpointLock(ctx context.Context, fn func() error) error {
	if e == nil || e.checkpointPath == "" {
		return fn()
	}
	// Serialize same-process writers as well as the cross-process lock below.
	// This avoids platform-specific O_EXCL error mappings during goroutine
	// contention while the file remains the inter-process authority.
	checkpointProcessMu.Lock()
	defer checkpointProcessMu.Unlock()
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
		lockExists := os.IsExist(err)
		if !lockExists {
			// Windows can report ERROR_ACCESS_DENIED for O_EXCL when another
			// process already owns the path. Confirm the path before treating it
			// as a real permissions failure.
			if _, statErr := os.Stat(lockPath); statErr == nil {
				lockExists = true
			} else if os.IsNotExist(statErr) {
				continue
			} else {
				return fmt.Errorf("create checkpoint lock: %w", err)
			}
		}
		if !lockExists {
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
