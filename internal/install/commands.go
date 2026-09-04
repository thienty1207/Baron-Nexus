package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

type CommandRunner interface {
	LookPath(name string) (string, error)
	Run(context.Context, string, ...string) (string, error)
}

// EnvironmentCommandRunner is an optional hardening boundary for commands
// that must not inherit the caller's complete environment. The map contains
// only explicitly allowlisted values; implementations must not log it.
type EnvironmentCommandRunner interface {
	RunWithEnvironment(context.Context, map[string]string, string, ...string) (string, error)
}

// WorkingDirectoryEnvironmentCommandRunner extends the isolated environment
// boundary with a private working directory for tools that emit artifacts
// relative to their current directory, such as Strix.
type WorkingDirectoryEnvironmentCommandRunner interface {
	RunWithEnvironmentInDir(context.Context, map[string]string, string, string, ...string) (string, error)
}

const (
	LatestDependencySelector    = "latest"
	EmbeddedCodexAdapterVersion = "0.1.0"
)

func InstallDSH(ctx context.Context, runner CommandRunner, version string) error {
	_, err := InstallDSHWithVersion(ctx, runner, version)
	return err
}

func InstallDSHLatestWithReport(ctx context.Context, runner CommandRunner, reporters ...ProgressReporter) (DependencyReport, error) {
	return EnsureNPMDependencyLatest(ctx, runner, NPMDependencySpec{
		Name: "DSH", Package: "@deepseek-ai/dsh", Command: "dsh",
	}, reporters...)
}

func InstallDSHWithVersion(ctx context.Context, runner CommandRunner, version string) (string, error) {
	if runner == nil {
		return "", errors.New("Node/npm runner is not configured")
	}
	if version == "" || version == LatestDependencySelector {
		report, err := InstallDSHLatestWithReport(ctx, runner)
		if err != nil {
			return "", err
		}
		return report.State.LocalVersion, nil
	}
	if _, err := runner.LookPath("npm"); err != nil {
		return "", errors.New("Node/npm is required for DeepSeek Harness initialization")
	}
	expectedVersion, err := NormalizeVersion(version)
	if err != nil {
		return "", fmt.Errorf("invalid DSH version %q: %w", version, err)
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
	if reported != expectedVersion {
		return "", fmt.Errorf("verify dsh installation: expected version %s", expectedVersion)
	}
	return reported, nil
}

// DSHPluginReport describes whether Baron had to change any managed profile.
type DSHPluginReport struct {
	Changed bool
}

// InstallDSHPlugins uses the upstream DSH plugin mechanism. Direct npm
// installation would leave packages as ordinary dependencies and would not
// activate their profile bundles.
func InstallDSHPlugins(ctx context.Context, runner CommandRunner, version string) error {
	_, err := InstallDSHPluginsWithReport(ctx, runner, version)
	return err
}

func InstallDSHPluginsWithReport(ctx context.Context, runner CommandRunner, _ string, reporters ...ProgressReporter) (DSHPluginReport, error) {
	reporter := firstProgressReporter(reporters...)
	if runner == nil {
		return DSHPluginReport{}, errors.New("Node/pnpm runner is not configured")
	}
	if _, err := runner.LookPath("pnpm"); err != nil {
		return DSHPluginReport{}, errors.New("pnpm is required for DSH plugin installation")
	}
	if _, err := runner.LookPath("uvx"); err != nil {
		return DSHPluginReport{}, errors.New("uv/uvx is required for the mandatory DuckDuckGo MCP")
	}
	plugins := []struct {
		packageName string
		fallback    string
		marker      string
		dependency  string
	}{
		{packageName: "superpowers-dsh", fallback: "superpowers-dsh@" + LatestDependencySelector, marker: "superpowers-dsh"},
		{fallback: "https://github.com/dhicoc/dsh-reverse-skill.git", marker: "dsh-reverse-skill"},
		{packageName: "@deepseek-ai/dsh-mcp-client", fallback: "@deepseek-ai/dsh-mcp-client@" + LatestDependencySelector, marker: "dsh-mcp-client", dependency: "@deepseek-ai/dsh-mcp-client"},
	}
	report := DSHPluginReport{}
	latestPlugins := map[string]string{}
	for _, profile := range []string{"web", "headless"} {
		dump, err := runner.Run(ctx, "dsh", "--profile", profile, "--dump-config")
		if err != nil {
			return DSHPluginReport{}, fmt.Errorf("inspect DSH %s profile composition: %w", profile, err)
		}
		profileChanged := false
		for _, plugin := range plugins {
			present := DSHProfileHasMarker(dump, plugin.marker)
			localVersion := dshProfileMarkerVersion(dump, plugin.marker)
			if plugin.dependency != "" && !present {
				dependencyOutput, dependencyErr := runner.Run(ctx, "dsh", "plugin", "--profile", profile, "list", "--depth", "0", "--json")
				if dependencyErr != nil {
					return DSHPluginReport{}, fmt.Errorf("inspect DSH %s profile dependencies: %w", profile, dependencyErr)
				}
				dependencyVersion, dependencyPresent, parseErr := dshProfileDependency(dependencyOutput, plugin.dependency)
				if parseErr != nil {
					return DSHPluginReport{}, fmt.Errorf("parse DSH %s profile dependencies: %w", profile, parseErr)
				}
				if dependencyPresent {
					present = true
					localVersion = dependencyVersion
				}
			}
			latestVersion := ""
			if plugin.packageName != "" && (!present || localVersion != "") && commandAvailable(runner, "npm") {
				cached, cachedOK := latestPlugins[plugin.packageName]
				if cachedOK {
					latestVersion = cached
				} else {
					latestOutput, latestErr := runner.Run(ctx, "npm", "view", plugin.packageName, "version")
					if latestErr != nil {
						return DSHPluginReport{}, fmt.Errorf("check latest DSH plugin %s version", plugin.packageName)
					}
					var normalizeErr error
					latestVersion, normalizeErr = NormalizeVersion(latestOutput)
					if normalizeErr != nil {
						return DSHPluginReport{}, fmt.Errorf("latest DSH plugin %s version is unknown", plugin.packageName)
					}
					latestPlugins[plugin.packageName] = latestVersion
				}
			}
			if present && (localVersion == "" || latestVersion == "" || localVersion == latestVersion) {
				continue
			}
			spec := plugin.fallback
			if latestVersion != "" {
				spec = plugin.packageName + "@" + latestVersion
			}
			reportStep(reporter, fmt.Sprintf("Installing DSH plugin %s in %s profile...", spec, profile))
			if _, err := runner.Run(ctx, "dsh", "plugin", "--profile", profile, "add", spec); err != nil {
				return DSHPluginReport{}, fmt.Errorf("install DSH plugin %s into %s profile: %w", spec, profile, err)
			}
			report.Changed = true
			profileChanged = true
		}
		if profileChanged {
			dump, err = runner.Run(ctx, "dsh", "--profile", profile, "--dump-config")
			if err != nil {
				return DSHPluginReport{}, fmt.Errorf("verify DSH %s profile composition: %w", profile, err)
			}
		}
		// dsh-mcp-client is a dependency consumed by the profile's MCP patch,
		// not a DSH bundle. DSH therefore lists it in package.json but omits it
		// from --dump-config's composed bundle tree.
		for _, marker := range []string{"superpowers-dsh", "dsh-reverse-skill"} {
			if !DSHProfileHasMarker(dump, marker) {
				return DSHPluginReport{}, fmt.Errorf("DSH %s profile composition did not contain %s", profile, marker)
			}
		}
	}
	return report, nil
}

func dshProfileMarkerVersion(dump, marker string) string {
	for _, line := range strings.Split(dump, "\n") {
		if !DSHProfileHasMarker(line, marker) {
			continue
		}
		if version, err := NormalizeVersion(line); err == nil {
			return version
		}
	}
	return ""
}

type dshProfilePackage struct {
	Dependencies map[string]struct {
		Version string `json:"version"`
	} `json:"dependencies"`
}

func dshProfileDependency(output, packageName string) (string, bool, error) {
	var profiles []dshProfilePackage
	if err := json.Unmarshal([]byte(output), &profiles); err != nil {
		var profile dshProfilePackage
		if singleErr := json.Unmarshal([]byte(output), &profile); singleErr != nil {
			return "", false, fmt.Errorf("DSH dependency list is not valid JSON")
		}
		profiles = []dshProfilePackage{profile}
	}
	for _, profile := range profiles {
		dependency, ok := profile.Dependencies[packageName]
		if ok {
			return strings.TrimSpace(dependency.Version), true, nil
		}
	}
	return "", false, nil
}

// DSHProfileHasMarker matches a plugin identity token from DSH's text, JSON,
// or YAML dump without treating arbitrary paths or similarly named text as a
// managed plugin entry.
func DSHProfileHasMarker(dump, marker string) bool {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return false
	}
	tokens := strings.FieldsFunc(dump, func(value rune) bool {
		return unicode.IsSpace(value) || strings.ContainsRune("\"'[]{}(),:=", value)
	})
	for _, token := range tokens {
		if token == marker || strings.HasPrefix(token, marker+"@") {
			return true
		}
		if strings.HasPrefix(token, "@") && (strings.HasSuffix(token, "/"+marker) || strings.Contains(token, "/"+marker+"@")) {
			return true
		}
	}
	return false
}

func VerifyDSHProfile(ctx context.Context, runner CommandRunner) error {
	if runner == nil {
		return errors.New("Node/pnpm runner is not configured")
	}
	for _, profile := range []string{"web", "headless"} {
		dump, err := runner.Run(ctx, "dsh", "--profile", profile, "--dump-config")
		if err != nil {
			return fmt.Errorf("verify DSH %s profile composition: %w", profile, err)
		}
		for _, marker := range []string{"superpowers-dsh", "dsh-reverse-skill", "baron-dsh-adapter", "baron-ddg-search", "ddg-search"} {
			if !DSHProfileHasMarker(dump, marker) {
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

func InstallCodexLatestWithReport(ctx context.Context, runner CommandRunner, reporters ...ProgressReporter) (DependencyReport, error) {
	if runner == nil {
		return DependencyReport{}, errors.New("Node/npm runner is not configured")
	}
	if _, err := runner.LookPath("npm"); err != nil {
		// Native Codex installs (for example the Windows desktop/standalone
		// install) do not necessarily ship with Node/npm. Hooks can still be
		// repaired safely when the existing Codex binary identifies itself.
		report, fallbackErr := ReuseInstalledCodex(ctx, runner)
		if fallbackErr != nil {
			return DependencyReport{}, errors.New("Node/npm is required to refresh Codex CLI to latest")
		}
		reportStep(firstProgressReporter(reporters...), fmt.Sprintf("Node/npm unavailable; keeping existing Codex %s and refreshing Baron hooks.", report.State.LocalVersion))
		return report, nil
	}
	return EnsureNPMDependencyLatest(ctx, runner, NPMDependencySpec{
		Name: "Codex", Package: "@openai/codex", Command: "codex",
	}, reporters...)
}

// ReuseInstalledCodex verifies an already-installed Codex CLI without
// requiring npm. It is used only for hook/adapter repair when refreshing the
// npm package is unavailable.
func ReuseInstalledCodex(ctx context.Context, runner CommandRunner) (DependencyReport, error) {
	if runner == nil {
		return DependencyReport{}, errors.New("Codex runner is not configured")
	}
	if _, err := runner.LookPath("codex"); err != nil {
		return DependencyReport{}, errors.New("Codex CLI is not installed")
	}
	output, err := runner.Run(ctx, "codex", "--version")
	if err != nil {
		return DependencyReport{}, errors.New("verify installed Codex CLI")
	}
	version, err := NormalizeVersion(output)
	if err != nil {
		return DependencyReport{}, fmt.Errorf("verify installed Codex CLI: %w", err)
	}
	return DependencyReport{
		State:  ComponentState{Name: "Codex", Installed: true, LocalVersion: version},
		Source: "existing:codex",
	}, nil
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
	if latest {
		report, err := InstallCodexLatestWithReport(ctx, runner)
		if err != nil {
			return "", "", err
		}
		return report.Source, report.State.LocalVersion, nil
	}
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

// RemoveGlobalNPM removes the listed globally installed packages. It first
// honors the user's npm prefix and retries through the same sudo boundary used
// by installation when the prefix is root-owned.
func RemoveGlobalNPM(ctx context.Context, runner CommandRunner, packages ...string) error {
	if runner == nil {
		return errors.New("Node/npm runner is not configured")
	}
	if len(packages) == 0 {
		return nil
	}
	args := append([]string{"uninstall", "--global"}, packages...)
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
	for _, match := range reportedVersionPattern.FindAllStringSubmatchIndex(output, -1) {
		if len(match) < 4 {
			continue
		}
		start, end := match[2], match[3]
		if start > 0 {
			previous := output[start-1]
			if previous == 'v' {
				if start > 1 && isVersionContinuation(output[start-2]) {
					continue
				}
			} else if isVersionContinuation(previous) {
				continue
			}
		}
		if end < len(output) && isVersionContinuation(output[end]) {
			continue
		}
		return output[start:end]
	}
	return ""
}

func isVersionContinuation(value byte) bool {
	return value == '.' || value == '-' || value == '+' ||
		(value >= '0' && value <= '9') ||
		(value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z')
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
