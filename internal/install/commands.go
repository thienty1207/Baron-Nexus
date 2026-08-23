package install

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	for _, plugin := range plugins {
		if _, err := runner.Run(ctx, "dsh", "plugin", "--profile", "web", "add", plugin); err != nil {
			return fmt.Errorf("install DSH plugin %s: %w", plugin, err)
		}
	}
	dump, err := runner.Run(ctx, "dsh", "--profile", "web", "--dump-config")
	if err != nil {
		return errors.New("verify DSH profile composition")
	}
	for _, marker := range []string{"superpowers-dsh", "dsh-reverse-skill"} {
		if !strings.Contains(dump, marker) {
			return fmt.Errorf("DSH profile composition did not contain %s", marker)
		}
	}
	return nil
}

func VerifyDSHProfile(ctx context.Context, runner CommandRunner) error {
	if runner == nil {
		return errors.New("Node/pnpm runner is not configured")
	}
	dump, err := runner.Run(ctx, "dsh", "--profile", "web", "--dump-config")
	if err != nil {
		return errors.New("verify DSH profile composition")
	}
	for _, marker := range []string{"superpowers-dsh", "dsh-reverse-skill", "baron-dsh-adapter", "baron-ddg-search", "ddg-search"} {
		if !strings.Contains(dump, marker) {
			return fmt.Errorf("DSH profile composition did not contain %s", marker)
		}
	}
	return nil
}

func InstallCodex(ctx context.Context, runner CommandRunner, version string) error {
	if runner == nil {
		return errors.New("Node/npm runner is not configured")
	}
	if _, err := runner.LookPath("npm"); err != nil {
		return errors.New("Node/npm is required for Codex CLI initialization")
	}
	if version == "" {
		version = "0.149.0"
	}
	if _, err := runner.Run(ctx, "npm", "install", "--global", "@openai/codex@"+version); err != nil {
		return fmt.Errorf("install @openai/codex@%s: %w", version, err)
	}
	if _, err := runner.LookPath("codex"); err != nil {
		return errors.New("Codex package installed but codex is not on PATH")
	}
	if _, err := runner.Run(ctx, "codex", "--version"); err != nil {
		return fmt.Errorf("verify Codex installation: %w", err)
	}
	return nil
}
