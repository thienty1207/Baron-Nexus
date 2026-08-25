package app

import (
	"context"
	"errors"
	"fmt"
	"runtime"

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
}

func runBootstrap(ctx context.Context, steps BootstrapSteps) error {
	if steps.Preflight == nil {
		return errors.New("Baron bootstrap host preflight is not configured")
	}
	if err := steps.Preflight(ctx); err != nil {
		return fmt.Errorf("Baron bootstrap host preflight failed: %w", err)
	}
	if steps.DSH == nil {
		return errors.New("Baron bootstrap DSH step is not configured")
	}
	if err := steps.DSH(); err != nil {
		return fmt.Errorf("Baron bootstrap DSH initialization failed: %w", err)
	}
	if steps.Codex == nil {
		return errors.New("Baron bootstrap Codex step is not configured")
	}
	if err := steps.Codex(); err != nil {
		return fmt.Errorf("Baron bootstrap Codex initialization failed: %w", err)
	}
	if steps.Tencent == nil {
		return errors.New("Baron bootstrap Tencent step is not configured")
	}
	if err := steps.Tencent(ctx); err != nil {
		return fmt.Errorf("Baron bootstrap Tencent initialization failed: %w", err)
	}
	if steps.Setup == nil {
		return errors.New("Baron bootstrap project setup step is not configured")
	}
	if err := steps.Setup(ctx); err != nil {
		return fmt.Errorf("Baron bootstrap project setup failed: %w", err)
	}
	return nil
}

func (a *App) installAndBootstrap(ctx context.Context) (string, error) {
	releaseMessage, err := a.installBaronBinary(true)
	if err != nil {
		return "", err
	}
	if err := runBootstrap(ctx, BootstrapSteps{
		Preflight: a.preflightBootstrap,
		DSH:       a.DSHInit,
		Codex:     a.CodexInit,
		Tencent:   a.TencentInit,
		Setup: func(ctx context.Context) error {
			_, err := a.SetupProject(ctx, "")
			return err
		},
	}); err != nil {
		return "", err
	}
	return bootstrapCompletionMessage(releaseMessage), nil
}

func bootstrapCompletionMessage(releaseMessage string) string {
	message := releaseMessage + " Bootstrap complete."
	if notice := codexLoginNotice(); notice != "" {
		message += " ACTION REQUIRED: " + notice
	}
	return message
}

func (a *App) preflightBootstrap(ctx context.Context) error {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		return fmt.Errorf("automatic Baron bootstrap supports Linux and Windows only; detected %s", runtime.GOOS)
	}
	report, err := install.EnsureDocker(ctx, a.commandRunner(), install.DockerBootstrapOptions{})
	if err != nil {
		return err
	}
	if !report.Ready {
		return errors.New("Docker host preflight did not report a ready runtime")
	}
	return nil
}
