package install

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type CommandRunner interface {
	LookPath(name string) (string, error)
	Run(context.Context, string, ...string) (string, error)
}

const (
	LatestDependencySelector    = "latest"
	EmbeddedCodexAdapterVersion = "0.1.0"
)

func InstallDSH(ctx context.Context, runner CommandRunner, version string) error {
	_, err := InstallDSHWithVersion(ctx, runner, version)
	return err
}

func InstallDSHWithVersion(ctx context.Context, runner CommandRunner, version string) (string, error) {
	if runner == nil {
		return "", errors.New("Node/npm runner is not configured")
	}
	if _, err := runner.LookPath("npm"); err != nil {
		return "", errors.New("Node/npm is required for DeepSeek Harness initialization")
	}
	if version == "" {
		version = LatestDependencySelector
	}
	if err := installGlobalNPM(ctx, runner, "@deepseek-ai/dsh@"+version); err != nil {
		return "", fmt.Errorf("install @deepseek-ai/dsh@%s: %w", version, err)
	}
	if _, err := runner.LookPath("dsh"); err != nil {
		return "", errors.New("DeepSeek Harness package installed but dsh is not on PATH")
	}
	output, err := runner.Run(ctx, "dsh", "--version")
	if err != nil {
		return "", fmt.Errorf("verify dsh installation: %w", err)
	}
	reported := reportedVersion(output)
	if reported == "" {
		return "", errors.New("verify dsh installation: dsh did not report a semantic version")
	}
	return reported, nil
}

// InstallDSHPlugins uses the upstream DSH plugin mechanism. Direct npm
// installation would leave packages as ordinary dependencies and would not
// activate their profile bundles.
func InstallDSHPlugins(ctx context.Context, runner CommandRunner, _ string) error {
	if runner == nil {
		return errors.New("Node/pnpm runner is not configured")
	}
	if _, err := runner.LookPath("pnpm"); err != nil {
		return errors.New("pnpm is required for DSH plugin installation")
	}
	if _, err := runner.LookPath("uvx"); err != nil {
		return errors.New("uv/uvx is required for the mandatory DuckDuckGo MCP")
	}
	plugins := []string{
		"superpowers-dsh@" + LatestDependencySelector,
		"https://github.com/dhicoc/dsh-reverse-skill.git",
		"@deepseek-ai/dsh-mcp-client@" + LatestDependencySelector,
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

// InstallCodexWithSource verifies or installs the selected Codex CLI release and
// reports whether Baron reused an existing binary or used the official npm
// package path. The source is safe for receipts and never contains a command
// output or credential.
func InstallCodexWithSource(ctx context.Context, runner CommandRunner, version string) (string, error) {
	source, _, err := InstallCodexWithVersion(ctx, runner, version)
	return source, err
}

func InstallCodexWithVersion(ctx context.Context, runner CommandRunner, version string) (string, string, error) {
	if runner == nil {
		return "", "", errors.New("Node/npm runner is not configured")
	}
	if version == "" {
		version = LatestDependencySelector
	}
	latest := version == LatestDependencySelector
	if !latest {
		if _, codexErr := runner.LookPath("codex"); codexErr == nil {
			if output, verifyErr := runner.Run(ctx, "codex", "--version"); verifyErr == nil && versionInOutput(output, version) {
				return "existing:codex", version, nil
			}
			if _, npmErr := runner.LookPath("npm"); npmErr != nil {
				return "", "", fmt.Errorf("Codex CLI is present but not %s; npm is unavailable to install the requested version", version)
			}
		} else if _, npmErr := runner.LookPath("npm"); npmErr != nil {
			return "", "", fmt.Errorf("Node/npm is required when Codex CLI is not already installed at %s", version)
		}
	} else if _, npmErr := runner.LookPath("npm"); npmErr != nil {
		return "", "", errors.New("Node/npm is required to refresh Codex CLI to latest")
	}
	if err := installGlobalNPM(ctx, runner, "@openai/codex@"+version); err != nil {
		return "", "", fmt.Errorf("install @openai/codex@%s: %w", version, err)
	}
	if _, err := runner.LookPath("codex"); err != nil {
		return "", "", errors.New("Codex package installed but codex is not on PATH")
	}
	output, err := runner.Run(ctx, "codex", "--version")
	if err != nil {
		return "", "", fmt.Errorf("verify Codex installation: %w", err)
	}
	reported := reportedVersion(output)
	if reported == "" {
		return "", "", errors.New("verify Codex installation: codex did not report a semantic version")
	}
	if !latest && !versionInOutput(output, version) {
		return "", "", fmt.Errorf("verify Codex installation: expected version %s", version)
	}
	return "npm:@openai/codex", reported, nil
}

// installGlobalNPM first honors a user-managed Node installation. On a fresh
// Ubuntu/Debian host, the automatic Node bootstrap may leave npm's global
// prefix root-owned; in that case retry the same package operation through
// native sudo so the user does not need a manual npm-prefix repair.
func installGlobalNPM(ctx context.Context, runner CommandRunner, packageSpec string) error {
	args := []string{"install", "--global", packageSpec}
	if _, err := runner.Run(ctx, "npm", args...); err == nil {
		return nil
	} else {
		if _, sudoErr := runner.LookPath("sudo"); sudoErr == nil {
			if _, retryErr := runSudo(ctx, runner, append([]string{"npm"}, args...)...); retryErr == nil {
				return nil
			}
		}
		return err
	}
}

var reportedVersionPattern = regexp.MustCompile(`(?:^|[^0-9])v?([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)`)

func reportedVersion(output string) string {
	match := reportedVersionPattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return ""
	}
	return strings.TrimPrefix(match[1], "v")
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
