package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baron-shared-brain/baron/internal/install"
)

func TestBootstrapTimerIsOptIn(t *testing.T) {
	t.Setenv("BARON_INSTALL_TIMINGS", "1")
	var output bytes.Buffer
	timer := newBootstrapTimer(install.NewProgressReporter(&output))
	timer.mark("discovery")
	timer.finish()
	if output.Len() == 0 || !strings.Contains(output.String(), "Timing discovery") || !strings.Contains(output.String(), "Timing total") {
		t.Fatalf("timing output=%q", output.String())
	}
}

func TestRunBoundedDiscoveryLimitsConcurrentReadOnlyWork(t *testing.T) {
	var active int32
	var maximum int32
	tasks := make([]discoveryTask, 12)
	for index := range tasks {
		index := index
		tasks[index] = discoveryTask{Name: fmt.Sprintf("component-%02d", index), Run: func(ctx context.Context) (install.ComponentState, error) {
			current := atomic.AddInt32(&active, 1)
			for {
				old := atomic.LoadInt32(&maximum)
				if current <= old || atomic.CompareAndSwapInt32(&maximum, old, current) {
					break
				}
			}
			defer atomic.AddInt32(&active, -1)
			select {
			case <-time.After(10 * time.Millisecond):
				return install.ComponentState{Name: tasks[index].Name}, nil
			case <-ctx.Done():
				return install.ComponentState{}, ctx.Err()
			}
		}}
	}
	results, err := runBoundedDiscovery(context.Background(), tasks)
	if err != nil {
		t.Fatal(err)
	}
	if maximum > maxDiscoveryConcurrency {
		t.Fatalf("discovery concurrency=%d, want <=%d", maximum, maxDiscoveryConcurrency)
	}
	if len(results) != len(tasks) {
		t.Fatalf("discovery results=%d, want %d", len(results), len(tasks))
	}
}

func TestRunBoundedDiscoveryReturnsDeterministicFailureWithoutPartialPlan(t *testing.T) {
	tasks := []discoveryTask{
		{Name: "codex", Run: func(context.Context) (install.ComponentState, error) {
			return install.ComponentState{}, errors.New("codex registry unavailable")
		}},
	}
	results, err := runBoundedDiscovery(context.Background(), tasks)
	if err == nil || results != nil || err.Error() != "discover codex: upstream version query failed" {
		t.Fatalf("unexpected discovery failure/results: err=%v results=%#v", err, results)
	}
}
