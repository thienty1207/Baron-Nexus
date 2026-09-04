// Package uninstall removes Baron-owned state without following arbitrary
// paths from a mutable configuration file.
package uninstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/install"
	"github.com/baron-shared-brain/baron/internal/permissions"
	"github.com/baron-shared-brain/baron/internal/project"
)

type Options struct {
	GlobalPath           string
	DSHConfigPath        string
	DSHHome              string
	DSHCredentialPath    string
	DSHProfilePatchPaths []string
	CodexHome            string
	CodexHooksPath       string
	CodexAdapterPath     string
	PermissionsDirectory string
	TencentInstallPath   string
	Receipts             []string
	ProjectRoots         []string
	ExecutablePath       string
	PurgeShared          bool
	PurgeAll             bool
	HomeDir              string
	SourceCheckouts      []string
	EnvironmentFiles     []string
	ManagedRuntime       *config.ManagedRuntimeState
	GOOS                 string
	Runner               install.CommandRunner
	RemoveExecutable     func(string) error
}

type Plan struct {
	Resources []string
	Commands  []string
}

type Report struct {
	Removed   []string
	Skipped   []string
	Preserved []string
	Failed    []string
	Warnings  []string
}

func (r Report) String() string {
	var builder strings.Builder
	if len(r.Failed) == 0 {
		builder.WriteString("Baron uninstall complete.\n")
	} else {
		builder.WriteString("Baron uninstall completed with failures.\n")
	}
	for _, path := range r.Removed {
		fmt.Fprintf(&builder, "  removed %s\n", path)
	}
	for _, path := range r.Skipped {
		fmt.Fprintf(&builder, "  skipped %s\n", path)
	}
	for _, path := range r.Preserved {
		fmt.Fprintf(&builder, "  preserved %s\n", path)
	}
	for _, path := range r.Failed {
		fmt.Fprintf(&builder, "  failed %s\n", path)
	}
	for _, warning := range r.Warnings {
		fmt.Fprintf(&builder, "  warning %s\n", warning)
	}
	return strings.TrimRight(builder.String(), "\n")
}

func BuildPlan(options Options) (Plan, error) {
	globalPath := filepath.Clean(strings.TrimSpace(options.GlobalPath))
	if globalPath == "." || globalPath == "" {
		return Plan{}, errors.New("Baron global state path is required")
	}
	if err := rejectDangerousPath(globalPath); err != nil {
		return Plan{}, err
	}
	globalDir := filepath.Dir(globalPath)
	plan := Plan{}
	add := func(path string) {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || path == "." {
			return
		}
		if !containsPath(plan.Resources, path) {
			plan.Resources = append(plan.Resources, path)
		}
	}
	add(globalPath)
	for _, path := range []string{
		options.DSHConfigPath,
		options.CodexAdapterPath,
		filepath.Join(globalDir, "dsh.json"),
		filepath.Join(globalDir, "dsh-adapter"),
		filepath.Join(globalDir, "codex-adapter"),
	} {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if !pathWithin(globalDir, path) {
			return Plan{}, fmt.Errorf("refusing to remove Baron path outside global config: %s", path)
		}
		add(path)
	}
	for _, path := range options.Receipts {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if !pathWithin(filepath.Join(globalDir, "receipts"), path) {
			return Plan{}, fmt.Errorf("refusing to remove receipt outside Baron receipts directory: %s", path)
		}
		add(path)
	}
	if options.PermissionsDirectory != "" {
		if err := permissions.ValidateDirectory(options.PermissionsDirectory); err != nil {
			return Plan{}, fmt.Errorf("refusing to remove permission launcher directory: %w", err)
		}
		paths := permissions.Paths(options.PermissionsDirectory)
		add(paths.DSH)
		add(paths.Codex)
	}

	dshHome := filepath.Clean(strings.TrimSpace(options.DSHHome))
	if dshHome != "." && dshHome != "" {
		if err := rejectDangerousPath(dshHome); err != nil {
			return Plan{}, err
		}
		// DSH_HOME is user-owned. Baron only removes its credential key and
		// profile blocks below; the home itself is never a purge target.
		for _, path := range options.DSHProfilePatchPaths {
			if !pathWithin(dshHome, path) {
				return Plan{}, fmt.Errorf("refusing to remove DSH patch outside DSH_HOME: %s", path)
			}
			add(path)
		}
		if options.DSHCredentialPath != "" && !pathWithin(dshHome, options.DSHCredentialPath) {
			return Plan{}, fmt.Errorf("refusing to remove DSH credentials outside DSH_HOME: %s", options.DSHCredentialPath)
		}
	}

	codexHome := filepath.Clean(strings.TrimSpace(options.CodexHome))
	if codexHome != "." && codexHome != "" {
		if err := rejectDangerousPath(codexHome); err != nil {
			return Plan{}, err
		}
		// CODEX_HOME is user-owned. Baron only removes its hook entries.
		if options.CodexHooksPath != "" && !pathWithin(codexHome, options.CodexHooksPath) {
			return Plan{}, fmt.Errorf("refusing to remove Codex hooks outside CODEX_HOME: %s", options.CodexHooksPath)
		}
	}
	if options.CodexHooksPath != "" {
		if codexHome == "." || codexHome == "" || !pathWithin(codexHome, options.CodexHooksPath) {
			return Plan{}, fmt.Errorf("refusing to remove Codex hooks without a safe CODEX_HOME: %s", options.CodexHooksPath)
		}
		add(options.CodexHooksPath)
	}
	for _, root := range options.ProjectRoots {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "." || root == "" {
			continue
		}
		if err := rejectDangerousPath(root); err != nil {
			return Plan{}, err
		}
		managed, err := isBaronProjectRoot(root)
		if err != nil {
			return Plan{}, err
		}
		if !managed {
			continue
		}
		add(filepath.Join(root, ".baron"))
		add(filepath.Join(root, ".gitignore"))
	}
	if options.TencentInstallPath != "" {
		root := filepath.Clean(options.TencentInstallPath)
		if err := rejectDangerousPath(root); err != nil {
			return Plan{}, err
		}
		if !pathWithin(globalDir, root) {
			if _, err := install.ReadTencentDeploymentManifest(root); err != nil {
				return Plan{}, fmt.Errorf("refusing to remove unverified Tencent deployment path: %s", root)
			}
		}
		add(root)
	}
	if options.ExecutablePath != "" {
		if err := rejectDangerousPath(options.ExecutablePath); err != nil {
			return Plan{}, err
		}
		add(options.ExecutablePath)
	}
	if options.PurgeAll {
		if options.ManagedRuntime != nil {
			targets, targetErr := ManagedPurgeTargets(*options.ManagedRuntime)
			if targetErr != nil {
				return Plan{}, targetErr
			}
			for _, target := range targets {
				add(target.Path)
			}
			plan.Commands = append(plan.Commands, "remove Baron-owned managed runtime targets only")
		} else {
			plan.Commands = append(plan.Commands, "preserve unverified external runtimes and Docker objects")
		}
	}
	return plan, nil
}

func isBaronProjectRoot(root string) (bool, error) {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("refusing to inspect unsafe Baron project root: %s", root)
	}
	baronDir := filepath.Join(root, ".baron")
	info, err = os.Lstat(baronDir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("refusing to remove unsafe Baron project state: %s", baronDir)
	}
	metadata := filepath.Join(baronDir, "project.toml")
	info, err = os.Lstat(metadata)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("refusing to remove unsafe Baron project metadata: %s", metadata)
	}
	return true, nil
}

func (p Plan) String() string {
	var builder strings.Builder
	builder.WriteString("Baron uninstall plan:\n")
	if len(p.Resources) == 0 {
		builder.WriteString("  No Baron filesystem resources are registered.\n")
	} else {
		for _, path := range p.Resources {
			fmt.Fprintf(&builder, "  remove %s\n", path)
		}
	}
	for _, command := range p.Commands {
		fmt.Fprintf(&builder, "  run %s\n", command)
	}
	return strings.TrimRight(builder.String(), "\n")
}

func Execute(ctx context.Context, options Options) (Report, error) {
	plan, err := BuildPlan(options)
	if err != nil {
		return Report{}, err
	}
	report := Report{}
	if options.CodexHooksPath != "" {
		changed, removeErr := install.RemoveCodexHooks(options.CodexHooksPath, "baron")
		if removeErr != nil {
			return report, fmt.Errorf("remove Baron Codex hooks: %w", removeErr)
		}
		if changed {
			report.Removed = append(report.Removed, options.CodexHooksPath+" (Baron hooks)")
		} else {
			report.Skipped = append(report.Skipped, options.CodexHooksPath+" (no Baron hooks)")
		}
	}
	for _, root := range options.ProjectRoots {
		managed, managedErr := isBaronProjectRoot(filepath.Clean(strings.TrimSpace(root)))
		if managedErr != nil {
			return report, managedErr
		}
		if !managed {
			continue
		}
		changed, removeErr := project.RemoveGitignoreRules(root)
		if removeErr != nil {
			return report, fmt.Errorf("remove Baron .gitignore rules: %w", removeErr)
		}
		path := filepath.Join(root, ".gitignore")
		if changed {
			report.Removed = append(report.Removed, path+" (Baron rules)")
		} else {
			report.Skipped = append(report.Skipped, path+" (no Baron rules)")
		}
	}
	for _, path := range options.DSHProfilePatchPaths {
		changed, removeErr := install.RemoveDSHProfilePatch(path)
		if removeErr != nil {
			return report, fmt.Errorf("remove Baron DSH patch: %w", removeErr)
		}
		if changed {
			report.Removed = append(report.Removed, path+" (Baron patch)")
		} else {
			report.Skipped = append(report.Skipped, path+" (no Baron patch)")
		}
	}
	if options.DSHCredentialPath != "" {
		changed, removeErr := install.RemoveDSHProviderKeyAt(options.DSHCredentialPath)
		if removeErr != nil {
			return report, fmt.Errorf("remove DeepSeek API key: %w", removeErr)
		}
		if changed {
			report.Removed = append(report.Removed, options.DSHCredentialPath+" (DeepSeek API key)")
		} else {
			report.Skipped = append(report.Skipped, options.DSHCredentialPath+" (no DeepSeek API key)")
		}
	}
	for _, path := range append([]string{options.DSHConfigPath, options.CodexHooksPath}, options.DSHProfilePatchPaths...) {
		if err := removeBaronBackups(path, &report); err != nil {
			return report, err
		}
	}
	// System-wide npm packages cannot be attributed to Baron without a
	// receipt. Preserve them rather than uninstalling another project's tools.
	report.Preserved = append(report.Preserved, "system-wide npm packages (ownership receipt unavailable)")

	if options.TencentInstallPath != "" && options.Runner != nil {
		removeTencent(ctx, options, &report)
	}
	managedPaths := map[string]struct{}{}
	if options.PurgeAll {
		if options.ManagedRuntime != nil {
			targets, targetErr := ManagedPurgeTargets(*options.ManagedRuntime)
			if targetErr != nil {
				return report, targetErr
			}
			purgeReport := PurgeManagedRuntime(ctx, PurgeOptions{
				Root:              options.ManagedRuntime.Root,
				LauncherDirectory: options.ManagedRuntime.LauncherDirectory,
				Targets:           targets,
			})
			report.Removed = append(report.Removed, purgeReport.Removed...)
			report.Skipped = append(report.Skipped, purgeReport.Skipped...)
			report.Preserved = append(report.Preserved, purgeReport.Preserved...)
			report.Failed = append(report.Failed, purgeReport.Failed...)
			for _, target := range targets {
				managedPaths[filepath.Clean(target.Path)] = struct{}{}
			}
		}
	}
	for _, path := range plan.Resources {
		if _, managed := managedPaths[filepath.Clean(path)]; managed {
			continue
		}
		if path == options.CodexHooksPath || path == options.DSHCredentialPath || containsPath(options.DSHProfilePatchPaths, path) || isPermissionLauncher(path, options) || isProjectGitignore(path, options) {
			continue
		}
		if err := removePurgePath(path, "", &report); err != nil {
			return report, err
		}
	}
	if err := removeBaronBackups(options.GlobalPath, &report); err != nil {
		return report, err
	}
	if err := removeBaronBackups(options.ExecutablePath, &report); err != nil {
		return report, err
	}
	if options.PermissionsDirectory != "" {
		status, disableErr := permissions.Disable(options.PermissionsDirectory)
		if disableErr != nil {
			report.Warnings = append(report.Warnings, "permission launcher cleanup skipped: "+disableErr.Error())
		} else {
			if !status.DSHEnabled && !status.CodexEnabled {
				report.Removed = append(report.Removed, options.PermissionsDirectory+" (auto-accept launchers)")
			}
		}
	}
	removeEmptyParents(plan, &report)
	if options.ExecutablePath != "" {
		if err := removeExecutable(options); err != nil {
			return report, err
		}
		report.Removed = append(report.Removed, options.ExecutablePath+" (Baron binary)")
	}
	if len(report.Failed) > 0 {
		return report, fmt.Errorf("Baron uninstall completed with failures")
	}
	if len(report.Warnings) > 0 {
		return report, fmt.Errorf("Baron uninstall completed with warnings")
	}
	return report, nil
}

func removeTencent(ctx context.Context, options Options, report *Report) {
	root := filepath.Clean(options.TencentInstallPath)
	manifest, manifestErr := install.ReadTencentDeploymentManifest(root)
	if manifestErr != nil {
		report.Preserved = append(report.Preserved, "Tencent Docker objects (deployment manifest unavailable)")
		return
	}
	for _, name := range []string{"docker-compose.yml", "compose.yaml", "compose.yml"} {
		composePath := filepath.Join(root, "deploy", "global-images", name)
		info, err := os.Lstat(composePath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			report.Warnings = append(report.Warnings, "Tencent compose file is not safe: "+composePath)
			break
		}
		if _, err := runDocker(ctx, options.Runner, "compose", "-f", composePath, "down", "--remove-orphans"); err != nil {
			report.Warnings = append(report.Warnings, "Tencent Docker Compose cleanup failed")
		}
		break
	}
	for _, container := range []string{"tdai-memory-core", "tdai-memory-hub", "tdai-proxy"} {
		if len(manifest.ContainerImageDigests[container]) == 0 {
			report.Preserved = append(report.Preserved, "Docker container "+container+" (manifest ownership unresolved)")
			continue
		}
		labels, err := runDocker(ctx, options.Runner, "inspect", "--format={{json .Config.Labels}}", container)
		if err != nil {
			report.Skipped = append(report.Skipped, "Docker container "+container+" (not present or unavailable)")
			continue
		}
		if !isManagedTencentContainer(labels, root) {
			report.Preserved = append(report.Preserved, "Docker container "+container+" (ownership label mismatch)")
			continue
		}
		if _, err := runDocker(ctx, options.Runner, "rm", "-f", container); err != nil {
			report.Warnings = append(report.Warnings, "Docker container cleanup failed for "+container)
		}
	}
}

func isManagedTencentContainer(output, root string) bool {
	var labels map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.SplitN(output, "\n", 2)[0])), &labels); err != nil {
		return false
	}
	workingDirectory := strings.TrimSpace(labels["com.docker.compose.project.working_dir"])
	if workingDirectory != "" && samePath(workingDirectory, root) {
		return true
	}
	configFiles := strings.TrimSpace(labels["com.docker.compose.project.config_files"])
	if configFiles == "" {
		return false
	}
	for _, file := range strings.Split(configFiles, string(os.PathListSeparator)) {
		if pathWithin(root, strings.TrimSpace(file)) || samePath(filepath.Dir(strings.TrimSpace(file)), filepath.Join(root, "deploy", "global-images")) {
			return true
		}
	}
	return false
}

func runDocker(ctx context.Context, runner install.CommandRunner, args ...string) (string, error) {
	output, err := runner.Run(ctx, "docker", args...)
	if err == nil {
		return output, nil
	}
	if _, sudoErr := runner.LookPath("sudo"); sudoErr != nil {
		return output, err
	}
	return install.RunSudo(ctx, runner, append([]string{"docker"}, args...)...)
}

func removePath(path string, report *Report) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		report.Skipped = append(report.Skipped, path+" (already absent)")
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to remove symlink path: %s", path)
	}
	if info.IsDir() {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	report.Removed = append(report.Removed, path)
	return nil
}

func removePurgePath(path, home string, report *Report) error {
	_ = home
	return removePath(path, report)
}

func removeBaronBackups(path string, report *Report) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	for _, pattern := range []string{path + ".baron-backup-*", path + ".baron-update-backup-*"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		for _, match := range matches {
			info, err := os.Lstat(match)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return fmt.Errorf("refusing to remove unsafe Baron backup: %s", match)
			}
			if err := os.Remove(match); err != nil {
				return fmt.Errorf("remove Baron backup %s: %w", match, err)
			}
			report.Removed = append(report.Removed, match)
		}
	}
	return nil
}

func removeExecutable(options Options) error {
	path := filepath.Clean(options.ExecutablePath)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to remove unsafe Baron executable path: %s", path)
	}
	remover := options.RemoveExecutable
	if remover == nil {
		remover = os.Remove
	}
	if err := remover(path); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return fmt.Errorf("remove Baron executable: %w", err)
	} else {
		return scheduleWindowsExecutableRemoval(path, err)
	}
}

func shouldSkipSharedChild(path string, options Options) bool {
	if !options.PurgeShared && !options.PurgeAll {
		return false
	}
	return path == filepath.Clean(options.DSHCredentialPath) || path == filepath.Clean(options.CodexHooksPath)
}

func isPermissionLauncher(path string, options Options) bool {
	if options.PermissionsDirectory == "" {
		return false
	}
	paths := permissions.Paths(options.PermissionsDirectory)
	return samePath(path, paths.DSH) || samePath(path, paths.Codex)
}

func removeEmptyParents(plan Plan, report *Report) {
	directories := make([]string, 0, 3)
	if len(plan.Resources) > 0 {
		globalDir := filepath.Dir(plan.Resources[0])
		if !samePath(filepath.Base(globalDir), "baron") {
			return
		}
		directories = append(directories, filepath.Join(globalDir, "receipts"), filepath.Join(globalDir, "bin"), globalDir)
	}
	for _, path := range directories {
		if err := rejectDangerousPath(path); err != nil {
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			continue
		}
		if os.Remove(path) == nil {
			report.Removed = append(report.Removed, path+" (empty directory)")
		}
	}
}

func isProjectGitignore(path string, options Options) bool {
	for _, root := range options.ProjectRoots {
		if samePath(path, filepath.Join(root, ".gitignore")) {
			return true
		}
	}
	return false
}

func rejectDangerousPath(path string) error {
	clean := filepath.Clean(path)
	if clean == "." || clean == "" || !filepath.IsAbs(clean) || clean == filepath.VolumeName(clean)+string(filepath.Separator) {
		return fmt.Errorf("refusing recursive removal of dangerous path: %s", path)
	}
	if home, err := os.UserHomeDir(); err == nil && samePath(clean, home) {
		return fmt.Errorf("refusing recursive removal of the home directory: %s", path)
	}
	return nil
}

func pathWithin(parent, child string) bool {
	parent, child = filepath.Clean(parent), filepath.Clean(child)
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		relative = strings.ToLower(relative)
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "."
}

func pathsOverlap(left, right string) bool {
	return samePath(left, right) || pathWithin(left, right) || pathWithin(right, left)
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if samePath(path, want) {
			return true
		}
	}
	return false
}
