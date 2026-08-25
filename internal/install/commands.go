package install

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type CommandRunner interface {
	LookPath(name string) (string, error)
	Run(context.Context, string, ...string) (string, error)
}

const (
	PinnedDSHVersion          = "0.1.1-rc.2"
	PinnedSuperpowersVersion  = "0.1.1"
	PinnedMCPClientVersion    = "0.0.1-rc.1"
	PinnedReverseSkillCommit  = "1bc4d63ee9a5268419170f4c5fb4a4e59e0e815c"
	PinnedReverseSkillPackage = "https://github.com/dhicoc/dsh-reverse-skill.git#" + PinnedReverseSkillCommit
	PinnedCodexAdapterVersion = "0.1.0"
)

func InstallDSH(ctx context.Context, runner CommandRunner, version string) error {
	if runner == nil {
		return errors.New("Node/npm runner is not configured")
	}
	if _, err := runner.LookPath("npm"); err != nil {
		return errors.New("Node/npm is required for DeepSeek Harness initialization")
	}
	if version == "" {
		version = PinnedDSHVersion
	}
	if _, err := runner.Run(ctx, "npm", "install", "--global", "@deepseek-ai/dsh@"+version); err != nil {
		return fmt.Errorf("install @deepseek-ai/dsh@%s: %w", version, err)
	}
	if _, err := runner.LookPath("dsh"); err != nil {
		return errors.New("DeepSeek Harness package installed but dsh is not on PATH")
	}
	if _, err := runner.Run(ctx, "dsh", "--version"); err != nil {
		return fmt.Errorf("verify dsh installation: %w", err)
	}
	return nil
}

// InstallDSHPlugins uses the upstream DSH plugin mechanism. Direct npm
// installation would leave packages as ordinary dependencies and would not
// activate their profile bundles.
func InstallDSHPlugins(ctx context.Context, runner CommandRunner, dshVersion string) error {
	if runner == nil {
		return errors.New("Node/pnpm runner is not configured")
	}
	if _, err := runner.LookPath("pnpm"); err != nil {
		return errors.New("pnpm is required for DSH plugin installation")
	}
	if _, err := runner.LookPath("uvx"); err != nil {
		return errors.New("uv/uvx is required for the mandatory DuckDuckGo MCP")
	}
	if dshVersion == "" {
		dshVersion = PinnedDSHVersion
	}
	plugins := []string{
		"superpowers-dsh@" + PinnedSuperpowersVersion,
		PinnedReverseSkillPackage,
		"@deepseek-ai/dsh-mcp-client@" + PinnedMCPClientVersion,
	}
	for _, profile := range []string{"web", "headless"} {
		for _, plugin := range plugins {
			if _, err := runner.Run(ctx, "dsh", "plugin", "--profile", profile, "add", plugin); err != nil {
				return fmt.Errorf("install DSH plugin %s into %s profile: %w", plugin, profile, err)
			}
		}
		dump, err := runner.Run(ctx, "dsh", "--profile", profile, "--dump-config")
		if err != nil {
			return fmt.Errorf("verify DSH %s profile composition", profile)
		}
		for _, marker := range []string{"superpowers-dsh", "dsh-reverse-skill"} {
			if !strings.Contains(dump, marker) {
				return fmt.Errorf("DSH %s profile composition did not contain %s", profile, marker)
			}
		}
	}
	return nil
}

func VerifyDSHProfile(ctx context.Context, runner CommandRunner) error {
	if runner == nil {
		return errors.New("Node/pnpm runner is not configured")
	}
	for _, profile := range []string{"web", "headless"} {
		dump, err := runner.Run(ctx, "dsh", "--profile", profile, "--dump-config")
		if err != nil {
			return fmt.Errorf("verify DSH %s profile composition", profile)
		}
		for _, marker := range []string{"superpowers-dsh", "dsh-reverse-skill", "baron-dsh-adapter", "baron-ddg-search", "ddg-search"} {
			if !strings.Contains(dump, marker) {
				return fmt.Errorf("DSH %s profile composition did not contain %s", profile, marker)
			}
		}
	}
	return nil
}

// ProbeDSHStartup performs a bounded headless startup check. DSH's web
// command is expected to keep serving after it has started, so a clean
// context deadline is treated as liveness success; immediate process errors
// remain failures. Output is intentionally discarded by the caller and is
// never included in the returned error because DSH may echo provider details.
func ProbeDSHStartup(ctx context.Context, runner CommandRunner) error {
	if runner == nil {
		return errors.New("Node/DSH runner is not configured")
	}
	if _, err := runner.LookPath("dsh"); err != nil {
		return errors.New("DeepSeek Harness is not installed")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	_, err := runner.Run(probeCtx, "dsh", "web", "--no-open")
	if err == nil {
		return nil
	}
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) && !errors.Is(ctx.Err(), context.Canceled) {
		return nil
	}
	return errors.New("DSH headless startup probe failed; rerun baron deepseek-harness init after checking the DSH runtime")
}

func InstallCodex(ctx context.Context, runner CommandRunner, version string) error {
	_, err := InstallCodexWithSource(ctx, runner, version)
	return err
}

// InstallCodexWithSource verifies or installs the pinned Codex CLI and
// reports whether Baron reused an existing binary or used the official npm
// package path. The source is safe for receipts and never contains a command
// output or credential.
func InstallCodexWithSource(ctx context.Context, runner CommandRunner, version string) (string, error) {
	if runner == nil {
		return "", errors.New("Node/npm runner is not configured")
	}
	if version == "" {
		version = "0.149.0"
	}
	if _, codexErr := runner.LookPath("codex"); codexErr == nil {
		if output, verifyErr := runner.Run(ctx, "codex", "--version"); verifyErr == nil && versionInOutput(output, version) {
			return "existing:codex", nil
		}
		if _, npmErr := runner.LookPath("npm"); npmErr != nil {
			return "", fmt.Errorf("Codex CLI is present but not pinned to %s; npm is unavailable to install the pinned version", version)
		}
	} else if _, npmErr := runner.LookPath("npm"); npmErr != nil {
		return "", errors.New("Node/npm is required when the pinned Codex CLI is not already installed")
	}
	if _, err := runner.Run(ctx, "npm", "install", "--global", "@openai/codex@"+version); err != nil {
		return "", fmt.Errorf("install @openai/codex@%s: %w", version, err)
	}
	if _, err := runner.LookPath("codex"); err != nil {
		return "", errors.New("Codex package installed but codex is not on PATH")
	}
	output, err := runner.Run(ctx, "codex", "--version")
	if err != nil {
		return "", fmt.Errorf("verify Codex installation: %w", err)
	}
	if !versionInOutput(output, version) {
		return "", fmt.Errorf("verify Codex installation: expected pinned version %s", version)
	}
	return "npm:@openai/codex", nil
}

func versionInOutput(output, version string) bool {
	for _, token := range strings.Fields(output) {
		token = strings.Trim(token, "()[]{}:,;\"")
		token = strings.TrimPrefix(token, "v")
		if token == version {
			return true
		}
	}
	return false
}
