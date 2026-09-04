package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/install"
)

// BootstrapSteps keeps the first-run order explicit and independently
// testable. Each delegated initializer owns its persisted/idempotent state.
type BootstrapSteps struct {
	Preflight func(context.Context) error
	DSH       func() error
	Codex     func() error
	Tencent   func(context.Context) error
	Setup     func(context.Context) error
	Progress  install.ProgressReporter
	Timer     *bootstrapTimer
}

type bootstrapTimer struct {
	reporter install.ProgressReporter
	started  time.Time
	enabled  bool
	finished bool
}

func newBootstrapTimer(reporter install.ProgressReporter) *bootstrapTimer {
	return &bootstrapTimer{reporter: reporter, started: time.Now(), enabled: strings.TrimSpace(os.Getenv("BARON_INSTALL_TIMINGS")) == "1"}
}

func (t *bootstrapTimer) mark(label string) {
	if t == nil || !t.enabled || t.finished {
		return
	}
	reportProgressStep(t.reporter, fmt.Sprintf("Timing %s: %s", label, time.Since(t.started).Round(time.Millisecond)))
}

func (t *bootstrapTimer) finish() {
	if t == nil || !t.enabled || t.finished {
		return
	}
	t.finished = true
	reportProgressStep(t.reporter, fmt.Sprintf("Timing total: %s", time.Since(t.started).Round(time.Millisecond)))
}

func runBootstrap(ctx context.Context, steps BootstrapSteps) error {
	if steps.Preflight == nil {
		return errors.New("Baron bootstrap host preflight is not configured")
	}
	if err := runBootstrapStep(steps.Progress, "Verifying Baron bootstrap prerequisites", func() error {
		return steps.Preflight(ctx)
	}); err != nil {
		return fmt.Errorf("Baron bootstrap host preflight failed: %w", err)
	}
	steps.Timer.mark("host preflight")
	if steps.DSH == nil {
		return errors.New("Baron bootstrap DSH step is not configured")
	}
	if err := runBootstrapStep(steps.Progress, "Initializing DeepSeek Harness", steps.DSH); err != nil {
		return fmt.Errorf("Baron bootstrap DSH initialization failed: %w", err)
	}
	steps.Timer.mark("DSH")
	if steps.Codex == nil {
		return errors.New("Baron bootstrap Codex step is not configured")
	}
	if err := runBootstrapStep(steps.Progress, "Installing Codex CLI", steps.Codex); err != nil {
		return fmt.Errorf("Baron bootstrap Codex initialization failed: %w", err)
	}
	steps.Timer.mark("Codex")
	if steps.Tencent == nil {
		return errors.New("Baron bootstrap Tencent step is not configured")
	}
	if err := runBootstrapStep(steps.Progress, "Provisioning Tencent Memory", func() error {
		return steps.Tencent(ctx)
	}); err != nil {
		return fmt.Errorf("Baron bootstrap Tencent initialization failed: %w", err)
	}
	steps.Timer.mark("Tencent")
	if steps.Setup == nil {
		return errors.New("Baron bootstrap project setup step is not configured")
	}
	if err := runBootstrapStep(steps.Progress, "Setting up the current Baron project", func() error {
		return steps.Setup(ctx)
	}); err != nil {
		return fmt.Errorf("Baron bootstrap project setup failed: %w", err)
	}
	steps.Timer.mark("project setup")
	return nil
}

func runBootstrapStep(reporter install.ProgressReporter, label string, action func() error) error {
	reportProgressStep(reporter, label+"...")
	err := action()
	if err != nil {
		reportProgressStep(reporter, label+" failed.")
		return err
	}
	reportProgressStep(reporter, label+" complete.")
	return nil
}

func (a *App) installAndBootstrap(ctx context.Context, reporter install.ProgressReporter) (string, error) {
	timer := newBootstrapTimer(reporter)
	defer timer.finish()
	reportProgressStep(reporter, "Starting Baron install; dependency setup may take several minutes.")
	// Host authorization and dependency work must precede the release download
	// as well as the DSH/Tencent downloads. The first-run coordinator is the
	// only path that owns this ordering; the managed bundle coordinator is the
	// authoritative path when it is available.
	if err := a.preflightBootstrap(ctx, reporter); err != nil {
		return "", err
	}
	timer.mark("preflight")
	releaseMessage, err := a.installBaronBinary(false, reporter)
	if err != nil {
		return "", err
	}
	timer.mark("Baron")
	plan, err := a.discoverBootstrapPlan(ctx, reporter)
	if err != nil {
		return "", err
	}
	timer.mark("discovery")
	if err := runBootstrap(ctx, BootstrapSteps{
		Preflight: func(context.Context) error { return nil },
		DSH:       func() error { return a.dshInitWithPlan(ctx, plan, reporter) },
		Codex:     func() error { return a.codexInitWithPlan(ctx, plan, reporter) },
		Tencent:   a.TencentInit,
		Setup: func(ctx context.Context) error {
			_, err := a.SetupProject(ctx, "")
			return err
		},
		Progress: reporter,
		Timer:    timer,
	}); err != nil {
		return "", err
	}
	timer.mark("validation")
	return bootstrapCompletionMessage(releaseMessage), nil
}

func bootstrapCompletionMessage(releaseMessage string) string {
	message := releaseMessage + " Bootstrap complete."
	if notice := codexLoginNotice(); notice != "" {
		message += " ACTION REQUIRED: " + notice
	}
	return message
}

func (a *App) preflightBootstrap(ctx context.Context, reporter install.ProgressReporter) error {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		return fmt.Errorf("automatic Baron bootstrap supports Linux and Windows only; detected %s", runtime.GOOS)
	}
	var aptSession *install.AptSession
	if runtime.GOOS == "linux" {
		aptSession = &install.AptSession{}
		report, err := install.EnsureHostToolchain(ctx, a.commandRunner(), install.HostToolchainOptions{Progress: reporter, HTTPClient: a.HTTPClient, Apt: aptSession})
		if err != nil {
			return err
		}
		if !report.Ready {
			return errors.New("host dependency preflight did not report a ready Node/pnpm/uv toolchain")
		}
	}
	report, err := install.EnsureDocker(ctx, a.commandRunner(), install.DockerBootstrapOptions{Progress: reporter, HTTPClient: a.HTTPClient, Apt: aptSession, Refresh: true})
	if err != nil {
		return err
	}
	if !report.Ready {
		return errors.New("Docker host preflight did not report a ready runtime")
	}
	return nil
}

func reportProgressStep(reporter install.ProgressReporter, label string) {
	if reporter != nil {
		reporter.Step(label)
	}
}
