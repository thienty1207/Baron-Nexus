package uninstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/install"
)

var sensitiveEnvironmentAssignment = regexp.MustCompile(`(?i)(^|[^a-z0-9_])(export[ \t]+)?(DEEPSEEK_API_KEY|OPENAI_API_KEY|MEMORY_LLM_API_KEY|PROXY_UPSTREAM_API_KEY|OPENAI_BASE_URL|DEEPSEEK_BASE_URL|DSH_HOME|CODEX_HOME|BARON_TENCENT_[a-z0-9_]+)[ \t]*=`)

var sensitiveEnvironmentCommand = regexp.MustCompile(`(?i)^[ \t]*(setx[ \t]+|set[ \t]+(-[a-z]+[ \t]+)*|set-item[ \t]+env:)(DEEPSEEK_API_KEY|OPENAI_API_KEY|MEMORY_LLM_API_KEY|PROXY_UPSTREAM_API_KEY|OPENAI_BASE_URL|DEEPSEEK_BASE_URL|DSH_HOME|CODEX_HOME|BARON_TENCENT_[a-z0-9_]+)([ \t=]|$)`)

var sensitiveEnvironmentNames = []string{
	"DEEPSEEK_API_KEY",
	"OPENAI_API_KEY",
	"MEMORY_LLM_API_KEY",
	"PROXY_UPSTREAM_API_KEY",
	"OPENAI_BASE_URL",
	"DEEPSEEK_BASE_URL",
	"DSH_HOME",
	"CODEX_HOME",
}

var linuxPurgePackages = []string{
	"docker-ce",
	"docker-ce-cli",
	"docker-ce-rootless-extras",
	"containerd.io",
	"docker-buildx-plugin",
	"docker-compose-plugin",
	"docker.io",
	"docker-compose",
	"docker-compose-v2",
	"containerd",
	"runc",
	"nodejs",
	"npm",
}

var linuxPurgePaths = []string{
	"/var/lib/docker",
	"/var/lib/containerd",
	"/etc/docker",
	"/etc/containerd",
	"/etc/apt/keyrings/docker.asc",
	"/etc/apt/keyrings/docker.gpg",
	"/etc/apt/keyrings/nodesource.gpg",
	"/etc/apt/sources.list.d/docker.list",
	"/etc/apt/sources.list.d/docker.sources",
	"/etc/apt/sources.list.d/nodesource.list",
	"/etc/apt/sources.list.d/nodesource.sources",
	"/usr/local/lib/node_modules/@deepseek-ai/dsh",
	"/usr/local/lib/node_modules/@openai/codex",
	"/usr/local/lib/node_modules/pnpm",
	"/usr/lib/node_modules/@deepseek-ai/dsh",
	"/usr/lib/node_modules/@openai/codex",
	"/usr/lib/node_modules/pnpm",
	"/usr/local/bin/dsh",
	"/usr/local/bin/codex",
	"/usr/local/bin/pnpm",
	"/usr/local/bin/pnpx",
	"/usr/bin/dsh",
	"/usr/bin/codex",
	"/usr/bin/pnpm",
	"/usr/bin/pnpx",
}

// DefaultEnvironmentFiles returns the small set of shell/profile files Baron
// may have been used with. It intentionally does not scan the whole home
// directory, because arbitrary recursive edits would risk unrelated secrets.
func DefaultEnvironmentFiles(home string) []string {
	home = filepath.Clean(strings.TrimSpace(home))
	if home == "." || home == "" {
		return nil
	}
	paths := []string{
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".bash_login"),
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".zprofile"),
		filepath.Join(home, ".zshenv"),
		filepath.Join(home, ".config", "fish", "config.fish"),
		filepath.Join(home, ".config", "environment.d", "baron.conf"),
		filepath.Join(home, ".config", "baron", "env"),
		filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
	}
	if runtime.GOOS == "windows" {
		paths = append(paths,
			filepath.Join(home, ".config", "powershell", "Microsoft.PowerShell_profile.ps1"),
		)
	}
	return uniquePaths(paths)
}

// DiscoverBaronSourceCheckouts only considers conventional direct children of
// the user's home. A directory is eligible for recursive deletion only when it
// is a real Baron-Nexus Git checkout with the expected upstream remote.
func DiscoverBaronSourceCheckouts(home string) []string {
	home = filepath.Clean(strings.TrimSpace(home))
	if home == "." || home == "" {
		return nil
	}
	var matches []string
	for _, name := range []string{"Baron-Nexus", "baron-nexus"} {
		candidate := filepath.Join(home, name)
		verified, err := isVerifiedBaronSourceCheckout(candidate, home)
		if err == nil && verified {
			matches = append(matches, candidate)
		}
	}
	return uniquePaths(matches)
}

func resolvePurgeHome(options Options) (string, error) {
	home := filepath.Clean(strings.TrimSpace(options.HomeDir))
	if home == "." || home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory for full uninstall: %w", err)
		}
		home = filepath.Clean(home)
	}
	if err := rejectPurgeHomePath(home); err != nil {
		return "", fmt.Errorf("unsafe full uninstall home: %w", err)
	}
	info, err := os.Lstat(home)
	if err != nil {
		return "", fmt.Errorf("inspect full uninstall home: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("refusing full uninstall through unsafe home directory: %s", home)
	}
	return home, nil
}

func rejectPurgeHomePath(path string) error {
	clean := filepath.Clean(path)
	if clean == "." || clean == "" || !filepath.IsAbs(clean) || clean == filepath.VolumeName(clean)+string(filepath.Separator) {
		return fmt.Errorf("refusing recursive removal of dangerous path: %s", path)
	}
	return nil
}

func fullPurgeResources(options Options, home string) ([]string, error) {
	paths := []string{
		filepath.Join(home, ".npm"),
		filepath.Join(home, ".npmrc"),
		filepath.Join(home, ".pnpmrc"),
		filepath.Join(home, ".cache", "uv"),
		filepath.Join(home, ".cache", "pnpm"),
		filepath.Join(home, ".cache", "node-gyp"),
		filepath.Join(home, ".node-gyp"),
		filepath.Join(home, ".local", "share", "uv"),
		filepath.Join(home, ".local", "share", "pnpm"),
		filepath.Join(home, ".local", "share", "docker"),
		filepath.Join(home, ".local", "bin", "uv"),
		filepath.Join(home, ".local", "bin", "uvx"),
		filepath.Join(home, ".cargo", "bin", "uv"),
		filepath.Join(home, ".cargo", "bin", "uvx"),
		filepath.Join(home, ".config", "pnpm"),
		filepath.Join(home, ".docker"),
		filepath.Join(home, ".config", "docker"),
	}
	paths = append(paths, knownPurgeLauncherPaths(home)...)
	if targetGOOS(options) == "windows" {
		paths = append(paths,
			filepath.Join(home, "AppData", "Local", "Docker"),
			filepath.Join(home, "AppData", "Local", "DockerDesktop"),
			filepath.Join(home, "AppData", "Roaming", "Docker"),
			filepath.Join(home, "AppData", "Roaming", "Docker Desktop"),
			filepath.Join(home, "AppData", "Local", "uv"),
			filepath.Join(home, "AppData", "Local", "pnpm"),
			filepath.Join(home, "AppData", "Roaming", "npm"),
			filepath.Join(home, "AppData", "Roaming", "npm-cache"),
			filepath.Join(home, "AppData", "Local", "npm-cache"),
		)
	}
	paths = append(paths, options.SourceCheckouts...)

	validated := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" {
			continue
		}
		if !pathWithin(home, path) {
			return nil, fmt.Errorf("refusing full purge path outside home directory: %s", path)
		}
		validate := validatePurgePathWithin
		if isKnownPurgeLauncherPath(home, path) {
			validate = validateKnownPurgePathWithin
		}
		if err := validate(home, path); err != nil {
			return nil, err
		}
		if containsPath(validated, path) {
			continue
		}
		validated = append(validated, path)
	}

	for _, path := range options.SourceCheckouts {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" {
			continue
		}
		verified, err := isVerifiedBaronSourceCheckout(path, home)
		if err != nil {
			return nil, err
		}
		if !verified {
			return nil, fmt.Errorf("refusing to remove unverified Baron source checkout: %s", path)
		}
	}
	for _, path := range options.EnvironmentFiles {
		if err := validateEnvironmentFile(home, path); err != nil {
			return nil, err
		}
	}
	return validated, nil
}

func knownPurgeLauncherPaths(home string) []string {
	binDir := filepath.Join(home, ".local", "bin")
	return []string{
		filepath.Join(binDir, "dsh"),
		filepath.Join(binDir, "codex"),
		filepath.Join(binDir, "pnpm"),
		filepath.Join(binDir, "pnpx"),
	}
}

func isKnownPurgeLauncherPath(home, path string) bool {
	for _, known := range knownPurgeLauncherPaths(home) {
		if samePath(known, path) {
			return true
		}
	}
	return false
}

func validatePurgePathWithin(home, path string) error {
	return validatePurgePathWithinMode(home, path, false)
}

func validateKnownPurgePathWithin(home, path string) error {
	return validatePurgePathWithinMode(home, path, true)
}

func validatePurgePathWithinMode(home, path string, allowKnownSymlink bool) error {
	if err := rejectDangerousPath(path); err != nil {
		return err
	}
	if !pathWithin(home, path) {
		return fmt.Errorf("refusing full purge path outside home directory: %s", path)
	}
	if err := validatePathComponentsWithFinalSymlink(home, path, allowKnownSymlink && isKnownPurgeLauncherPath(home, path)); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect full purge path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 && !(allowKnownSymlink && isKnownPurgeLauncherPath(home, path)) {
		return fmt.Errorf("refusing full purge symlink path: %s", path)
	}
	return nil
}

func validatePathComponents(parent, child string) error {
	return validatePathComponentsWithFinalSymlink(parent, child, false)
}

func validatePathComponentsWithFinalSymlink(parent, child string, allowFinalSymlink bool) error {
	relative, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil {
		return fmt.Errorf("resolve full purge path components: %w", err)
	}
	current := filepath.Clean(parent)
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect full purge path component %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if allowFinalSymlink && samePath(current, child) {
				return nil
			}
			return fmt.Errorf("refusing full purge through symlink path component: %s", current)
		}
		if !samePath(current, child) && !info.IsDir() {
			return fmt.Errorf("refusing full purge through non-directory path component: %s", current)
		}
	}
	return nil
}

func validateEnvironmentFile(home, path string) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return nil
	}
	if !pathWithin(home, path) {
		return fmt.Errorf("refusing to scrub environment file outside home directory: %s", path)
	}
	if err := validatePathComponents(home, path); err != nil {
		return err
	}
	if err := rejectDangerousPath(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect environment file %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to scrub unsafe environment file: %s", path)
	}
	return nil
}

func isVerifiedBaronSourceCheckout(path, home string) (bool, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return false, nil
	}
	if !pathWithin(home, path) || samePath(home, path) {
		return false, fmt.Errorf("refusing to inspect source checkout outside home directory: %s", path)
	}
	if err := validatePathComponents(home, path); err != nil {
		return false, err
	}
	if !samePath(filepath.Base(path), "Baron-Nexus") {
		return false, nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("refusing to inspect unsafe Baron source checkout: %s", path)
	}
	gitDir := filepath.Join(path, ".git")
	gitInfo, err := os.Lstat(gitDir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if gitInfo.Mode()&os.ModeSymlink != 0 || !gitInfo.IsDir() {
		return false, fmt.Errorf("refusing to remove Baron checkout with unsafe .git directory: %s", gitDir)
	}
	configPath := filepath.Join(gitDir, "config")
	configInfo, err := os.Lstat(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if configInfo.Mode()&os.ModeSymlink != 0 || !configInfo.Mode().IsRegular() {
		return false, fmt.Errorf("refusing to inspect unsafe Git config: %s", configPath)
	}
	remote, err := os.ReadFile(configPath)
	if err != nil {
		return false, err
	}
	remoteText := strings.ToLower(string(remote))
	if !strings.Contains(remoteText, "github.com/thienty1207/baron-nexus") && !strings.Contains(remoteText, "github.com:thienty1207/baron-nexus") {
		return false, nil
	}
	for _, readme := range []string{"README.md", "README"} {
		readmePath := filepath.Join(path, readme)
		readmeInfo, readmeErr := os.Lstat(readmePath)
		if readmeErr == nil && readmeInfo.Mode()&os.ModeSymlink == 0 && readmeInfo.Mode().IsRegular() {
			return true, nil
		}
		if readmeErr != nil && !errors.Is(readmeErr, os.ErrNotExist) {
			return false, readmeErr
		}
	}
	return false, nil
}

func fullPurgePlanCommands(options Options) []string {
	commands := []string{
		"npm cache clean --force",
		"pnpm store prune",
		"docker system prune -a --volumes --force",
	}
	switch targetGOOS(options) {
	case "linux":
		commands = append(commands,
			"sudo -n systemctl stop docker docker.socket containerd",
			"sudo -n systemctl disable docker docker.socket containerd",
			"sudo -n apt-get purge -y docker-ce docker-ce-cli containerd.io nodejs npm",
			"sudo -n apt-get autoremove --purge -y",
			"sudo -n apt-get clean",
			"sudo -n rm -rf -- /var/lib/docker /var/lib/containerd",
		)
	case "windows":
		commands = append(commands,
			"winget uninstall --id Docker.DockerDesktop --silent --accept-source-agreements --disable-interactivity",
			"winget uninstall --id OpenJS.NodeJS --silent --accept-source-agreements --disable-interactivity",
			"winget uninstall --id OpenJS.NodeJS.LTS --silent --accept-source-agreements --disable-interactivity",
		)
	}
	return commands
}

func executeFullPurge(ctx context.Context, options Options, report *Report) {
	scrubProcessEnvironment(report)
	scrubEnvironmentFiles(options, report)
	if options.Runner == nil {
		report.Warnings = append(report.Warnings, "full host cleanup skipped: command runner is unavailable")
		return
	}
	purgeNPM(ctx, options.Runner, report)
	purgeDocker(ctx, options.Runner, report)
	switch targetGOOS(options) {
	case "linux":
		purgeLinuxHost(ctx, options.Runner, report)
	case "windows":
		purgeWindowsHost(ctx, options.Runner, report)
	default:
		report.Warnings = append(report.Warnings, "host dependency cleanup is unsupported on "+targetGOOS(options))
	}
}

func purgeNPM(ctx context.Context, runner install.CommandRunner, report *Report) {
	if _, err := runner.LookPath("npm"); err != nil {
		report.Skipped = append(report.Skipped, "npm global packages and cache (npm not found)")
	} else {
		if err := install.RemoveGlobalNPM(ctx, runner, "@deepseek-ai/dsh", "@openai/codex", "pnpm"); err != nil {
			report.Warnings = append(report.Warnings, "npm global package cleanup failed: "+err.Error())
		}
		if _, err := runner.Run(ctx, "npm", "cache", "clean", "--force"); err != nil {
			report.Warnings = append(report.Warnings, "npm cache cleanup failed")
		}
	}
	if _, err := runner.LookPath("pnpm"); err != nil {
		report.Skipped = append(report.Skipped, "pnpm store cleanup (pnpm not found)")
	} else if _, err := runner.Run(ctx, "pnpm", "store", "prune"); err != nil {
		report.Warnings = append(report.Warnings, "pnpm store cleanup failed")
	}
}

func purgeDocker(ctx context.Context, runner install.CommandRunner, report *Report) {
	if _, err := runner.LookPath("docker"); err != nil {
		report.Skipped = append(report.Skipped, "Docker objects (docker not found)")
		return
	}
	queries := []struct {
		args   []string
		remove string
		label  string
	}{
		{args: []string{"ps", "-aq"}, remove: "rm", label: "containers"},
		{args: []string{"volume", "ls", "-q"}, remove: "volume", label: "volumes"},
		{args: []string{"images", "-aq"}, remove: "rmi", label: "images"},
		{args: []string{"network", "ls", "--filter", "type=custom", "-q"}, remove: "network", label: "networks"},
	}
	for _, query := range queries {
		output, err := runDocker(ctx, runner, query.args...)
		if err != nil {
			report.Warnings = append(report.Warnings, "Docker "+query.label+" cleanup query failed")
			continue
		}
		ids := strings.Fields(output)
		if len(ids) == 0 {
			continue
		}
		args := append([]string{query.remove}, ids...)
		if query.remove == "rm" {
			args = append([]string{"rm", "-f"}, ids...)
		}
		if _, err := runDocker(ctx, runner, args...); err != nil {
			report.Warnings = append(report.Warnings, "Docker "+query.label+" cleanup failed")
		}
	}
	if _, err := runDocker(ctx, runner, "system", "prune", "-a", "--volumes", "--force"); err != nil {
		report.Warnings = append(report.Warnings, "Docker system cleanup failed")
	}
}

func purgeLinuxHost(ctx context.Context, runner install.CommandRunner, report *Report) {
	if _, err := runner.LookPath("sudo"); err != nil {
		report.Warnings = append(report.Warnings, "Linux host cleanup skipped: sudo is unavailable")
		return
	}
	for _, service := range []string{"docker", "docker.socket", "containerd"} {
		if _, err := install.RunSudo(ctx, runner, "systemctl", "stop", service); err != nil {
			report.Skipped = append(report.Skipped, "stop "+service+" service")
		}
		if _, err := install.RunSudo(ctx, runner, "systemctl", "disable", service); err != nil {
			report.Skipped = append(report.Skipped, "disable "+service+" service")
		}
	}

	installed := make([]string, 0, len(linuxPurgePackages))
	if _, err := runner.LookPath("dpkg-query"); err == nil {
		for _, packageName := range linuxPurgePackages {
			output, queryErr := runner.Run(ctx, "dpkg-query", "-W", "-f=${db:Status-Status}", packageName)
			if queryErr == nil && strings.TrimSpace(output) == "installed" {
				installed = append(installed, packageName)
			}
		}
	} else {
		report.Skipped = append(report.Skipped, "installed Debian package discovery (dpkg-query not found)")
	}
	if len(installed) > 0 {
		if _, err := install.RunSudo(ctx, runner, append([]string{"apt-get", "purge", "-y"}, installed...)...); err != nil {
			report.Warnings = append(report.Warnings, "Debian package purge failed")
		}
	}
	for _, args := range [][]string{
		{"apt-get", "autoremove", "--purge", "-y"},
		{"apt-get", "clean"},
	} {
		if _, err := install.RunSudo(ctx, runner, args...); err != nil {
			report.Warnings = append(report.Warnings, "Debian package cleanup failed")
		}
	}
	for _, path := range linuxPurgePaths {
		if _, err := install.RunSudo(ctx, runner, "rm", "-rf", "--", path); err != nil {
			report.Warnings = append(report.Warnings, "remove Linux host path failed: "+path)
		}
	}
}

func purgeWindowsHost(ctx context.Context, runner install.CommandRunner, report *Report) {
	if _, err := runner.LookPath("winget"); err != nil {
		report.Warnings = append(report.Warnings, "Windows host cleanup skipped: winget is unavailable")
		return
	}
	for _, id := range []string{"Docker.DockerDesktop", "OpenJS.NodeJS", "OpenJS.NodeJS.LTS"} {
		args := []string{"uninstall", "--id", id, "--silent", "--accept-source-agreements", "--disable-interactivity"}
		if _, err := runner.Run(ctx, "winget", args...); err != nil {
			report.Warnings = append(report.Warnings, "Windows package cleanup failed for "+id)
		}
	}
}

func scrubEnvironmentFiles(options Options, report *Report) {
	for _, path := range options.EnvironmentFiles {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" {
			continue
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			report.Warnings = append(report.Warnings, "inspect environment file failed: "+path)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			report.Warnings = append(report.Warnings, "environment file is not a safe regular file: "+path)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			report.Warnings = append(report.Warnings, "read environment file failed: "+path)
			continue
		}
		cleaned, changed := scrubSensitiveEnvironment(data)
		if !changed {
			continue
		}
		if err := config.AtomicWriteFile(path, cleaned, info.Mode().Perm()); err != nil {
			report.Warnings = append(report.Warnings, "scrub environment file failed: "+path)
			continue
		}
		report.Removed = append(report.Removed, path+" (known sensitive entries)")
	}
}

func scrubSensitiveEnvironment(data []byte) ([]byte, bool) {
	parts := strings.SplitAfter(string(data), "\n")
	kept := make([]string, 0, len(parts))
	changed := false
	for _, part := range parts {
		line := strings.TrimSuffix(strings.TrimSuffix(part, "\n"), "\r")
		if sensitiveEnvironmentAssignment.MatchString(line) || sensitiveEnvironmentCommand.MatchString(line) {
			changed = true
			continue
		}
		kept = append(kept, part)
	}
	return []byte(strings.Join(kept, "")), changed
}

func scrubProcessEnvironment(report *Report) {
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || !isSensitiveEnvironmentName(name) {
			continue
		}
		_ = os.Unsetenv(name)
		report.Skipped = append(report.Skipped, name+" environment override (unset in the parent shell or restart the shell)")
	}
}

func isSensitiveEnvironmentName(name string) bool {
	name = strings.ToUpper(strings.TrimSpace(name))
	for _, known := range sensitiveEnvironmentNames {
		if name == known {
			return true
		}
	}
	return strings.HasPrefix(name, "BARON_TENCENT_")
}

func targetGOOS(options Options) string {
	if strings.TrimSpace(options.GOOS) != "" {
		return strings.ToLower(strings.TrimSpace(options.GOOS))
	}
	return runtime.GOOS
}

func uniquePaths(paths []string) []string {
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" || containsPath(unique, path) {
			continue
		}
		unique = append(unique, path)
	}
	return unique
}
