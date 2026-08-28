package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/baron-shared-brain/baron/internal/install"
)

const maxDiscoveryConcurrency = 6

type discoveryTask struct {
	Name string
	Run  func(context.Context) (install.ComponentState, error)
}

type discoveryResult struct {
	Name  string
	State install.ComponentState
	Err   error
}

type BootstrapPlan struct {
	Components map[string]install.ComponentState
}

func (p BootstrapPlan) State(name string) (install.ComponentState, bool) {
	state, ok := p.Components[name]
	return state, ok
}

func runBoundedDiscovery(ctx context.Context, tasks []discoveryTask) (map[string]install.ComponentState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(tasks) == 0 {
		return map[string]install.ComponentState{}, nil
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	semaphore := make(chan struct{}, maxDiscoveryConcurrency)
	results := make(chan discoveryResult, len(tasks))
	var waitGroup sync.WaitGroup
	for _, task := range tasks {
		task := task
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			select {
			case semaphore <- struct{}{}:
			case <-workCtx.Done():
				results <- discoveryResult{Name: task.Name, Err: workCtx.Err()}
				return
			}
			defer func() { <-semaphore }()
			if task.Run == nil {
				results <- discoveryResult{Name: task.Name, Err: errors.New("discovery task is not configured")}
				cancel()
				return
			}
			state, err := task.Run(workCtx)
			results <- discoveryResult{Name: task.Name, State: state, Err: err}
			if err != nil {
				cancel()
			}
		}()
	}
	waitGroup.Wait()
	close(results)
	ordered := make([]discoveryResult, 0, len(tasks))
	for result := range results {
		ordered = append(ordered, result)
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	for _, result := range ordered {
		if result.Err == nil || errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded) {
			continue
		}
		return nil, fmt.Errorf("discover %s: upstream version query failed", result.Name)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, result := range ordered {
		if result.Err != nil {
			return nil, result.Err
		}
	}
	plan := make(map[string]install.ComponentState, len(ordered))
	for _, result := range ordered {
		plan[result.Name] = result.State
	}
	return plan, nil
}

func (a *App) discoverBootstrapPlan(ctx context.Context, reporter install.ProgressReporter) (BootstrapPlan, error) {
	reportProgressStep(reporter, "Checking latest dependency versions...")
	runner := a.commandRunner()
	results, err := runBoundedDiscovery(ctx, []discoveryTask{
		{Name: "codex", Run: func(ctx context.Context) (install.ComponentState, error) {
			return install.DiscoverNPMDependencyLatest(ctx, runner, install.NPMDependencySpec{Name: "Codex", Package: "@openai/codex", Command: "codex"})
		}},
		{Name: "dsh", Run: func(ctx context.Context) (install.ComponentState, error) {
			return install.DiscoverNPMDependencyLatest(ctx, runner, install.NPMDependencySpec{Name: "DSH", Package: "@deepseek-ai/dsh", Command: "dsh"})
		}},
	})
	if err != nil {
		return BootstrapPlan{}, err
	}
	return BootstrapPlan{Components: results}, nil
}
