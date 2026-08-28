package install

import (
	"context"
	"errors"
	"sync"
)

// AptSession deduplicates metadata refreshes within one bootstrap cycle. It
// is deliberately scoped to a single call graph and never persists package
// freshness across runs.
type AptSession struct {
	mu        sync.Mutex
	refreshed bool
}

func (s *AptSession) invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.refreshed = false
	s.mu.Unlock()
}

func refreshAptMetadata(ctx context.Context, runner CommandRunner, reporter ProgressReporter, session *AptSession, label string) error {
	if session != nil {
		session.mu.Lock()
		defer session.mu.Unlock()
		if session.refreshed {
			return nil
		}
	}
	if _, err := runSudoProgress(ctx, runner, reporter, "Refreshing apt metadata for "+label, "apt-get", "update"); err != nil {
		return errors.New("apt metadata refresh failed")
	}
	if session != nil {
		session.refreshed = true
	}
	return nil
}
