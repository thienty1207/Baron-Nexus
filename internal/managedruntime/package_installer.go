package managedruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
)

// NativeComponentInstaller installs package-backed components into the
// generation supplied by Manager. It never uses a shell and never forwards
// the caller's complete environment to npm or uv.
type NativeComponentInstaller struct{}

func (NativeComponentInstaller) Install(ctx context.Context, component ComponentPlan, artifactPath, destination, generation string, reporter ProgressReporter) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(artifactPath) == "" || strings.TrimSpace(destination) == "" || strings.TrimSpace(generation) == "" {
		return errors.New("package installer paths are required")
	}
	if err := validatePackageInstallDestination(generation, destination); err != nil {
		return err
	}
	info, err := os.Lstat(artifactPath)
	if err != nil {
		return fmt.Errorf("inspect verified package artifact: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("verified package artifact is not a regular file")
	}
	if reporter != nil {
		reporter.Step(fmt.Sprintf("Installing %s package %s", component.ID, component.Package))
	}
	switch component.EffectiveInstallMethod() {
	case InstallMethodNPM:
		if err := installNPMArtifact(ctx, component, artifactPath, destination, generation, reporter); err != nil {
			return err
		}
	case InstallMethodPNPM:
		if err := installPNPMArtifact(ctx, component, artifactPath, destination, generation, reporter); err != nil {
			return err
		}
	case InstallMethodUVTool:
		if err := installUVToolArtifact(ctx, component, artifactPath, destination, generation, reporter); err != nil {
			return err
		}
	default:
		return fmt.Errorf("native package installer does not support %s", component.EffectiveInstallMethod())
	}
	if _, err := FindExecutableNamed(destination, componentExecutableNamesForPlan(component)...); err != nil {
		return fmt.Errorf("verify %s package entry point: %w", component.ID, err)
	}
	if reporter != nil {
		reporter.Step(fmt.Sprintf("%s package staged and verified", component.ID))
	}
	return nil
}

func installNPMArtifact(ctx context.Context, component ComponentPlan, artifactPath, destination, generation string, reporter ProgressReporter) error {
	npm, err := findGenerationExecutableForPlan(generation, ComponentPlan{ID: ComponentNPM, EntryPoint: "npm"}, []ComponentID{ComponentNPM, ComponentNode})
	if err != nil {
		return fmt.Errorf("resolve managed npm for %s: %w", component.ID, err)
	}
	toolArtifact, cleanup, err := materializeToolArtifact(artifactPath, component.URL, generation)
	if err != nil {
		return fmt.Errorf("prepare %s package artifact: %w", component.ID, err)
	}
	defer cleanup()
	args := npmInstallArgs(destination, toolArtifact)
	if err := runManagedExecutableWithProgress(ctx, npm, args, packageEnvironment(generation, destination, "npm"), reporter, fmt.Sprintf("Resolving %s dependencies", component.ID)); err != nil {
		return fmt.Errorf("npm install for %s failed: %w", component.ID, err)
	}
	packagePath := filepath.Join(destination, "node_modules")
	for _, part := range strings.Split(component.Package, "/") {
		packagePath = filepath.Join(packagePath, part)
	}
	if info, statErr := os.Stat(packagePath); statErr != nil || !info.IsDir() {
		if statErr == nil {
			statErr = errors.New("installed package directory is not a directory")
		}
		return fmt.Errorf("npm install for %s did not produce package %s: %w", component.ID, component.Package, statErr)
	}
	return nil
}

func npmInstallArgs(destination, artifact string) []string {
	// DSH has peer dependencies that must be hoisted for its ESM imports to
	// resolve. Avoid legacy-peer-deps and shallow layouts; a lockfile is not
	// authoritative here because the catalog pins the verified top-level
	// artifact while npm resolves its transitive runtime dependencies.
	return []string{
		"install", "--prefix", destination,
		"--ignore-scripts", "--no-audit", "--no-fund", "--no-update-notifier",
		"--package-lock=false", "--install-strategy=hoisted", "--omit=dev", artifact,
	}
}

func installPNPMArtifact(ctx context.Context, component ComponentPlan, artifactPath, destination, generation string, reporter ProgressReporter) error {
	pnpm, err := findGenerationExecutableForPlan(generation, ComponentPlan{ID: ComponentPNPM, EntryPoint: "pnpm"}, []ComponentID{ComponentPNPM})
	if err != nil {
		return fmt.Errorf("resolve managed pnpm for %s: %w", component.ID, err)
	}
	toolArtifact, cleanup, err := materializeToolArtifact(artifactPath, component.URL, generation)
	if err != nil {
		return fmt.Errorf("prepare %s package artifact: %w", component.ID, err)
	}
	defer cleanup()
	if err := runManagedExecutableWithProgress(ctx, pnpm, pnpmInstallArgs(destination, toolArtifact, generation), packageEnvironment(generation, destination, "pnpm"), reporter, fmt.Sprintf("Resolving %s dependencies", component.ID)); err != nil {
		return fmt.Errorf("pnpm install for %s failed: %w", component.ID, err)
	}
	packagePath := filepath.Join(destination, "node_modules")
	for _, part := range strings.Split(component.Package, "/") {
		packagePath = filepath.Join(packagePath, part)
	}
	if info, statErr := os.Stat(packagePath); statErr != nil || !info.IsDir() {
		if statErr == nil {
			statErr = errors.New("installed package directory is not a directory")
		}
		return fmt.Errorf("pnpm install for %s did not produce package %s: %w", component.ID, component.Package, statErr)
	}
	return nil
}

func pnpmInstallArgs(destination, artifact, generation string) []string {
	return []string{
		"add", "--dir", destination, "--prod", "--ignore-scripts", "--no-lockfile",
		"--reporter=append-only", "--node-linker=hoisted", "--store-dir", filepath.Join(generation, ".pnpm-store"), artifact,
	}
}

func installUVToolArtifact(ctx context.Context, component ComponentPlan, artifactPath, destination, generation string, reporter ProgressReporter) error {
	uv, err := findGenerationExecutableForPlan(generation, ComponentPlan{ID: ComponentUV, EntryPoint: "uv"}, []ComponentID{ComponentUV})
	if err != nil {
		return fmt.Errorf("resolve managed uv for %s: %w", component.ID, err)
	}
	python, err := findGenerationExecutableForPlan(generation, ComponentPlan{ID: ComponentPython, EntryPoint: "python"}, []ComponentID{ComponentPython})
	if err != nil {
		return fmt.Errorf("resolve managed Python for %s: %w", component.ID, err)
	}
	toolArtifact, cleanup, err := materializeToolArtifact(artifactPath, component.URL, generation)
	if err != nil {
		return fmt.Errorf("prepare %s package artifact: %w", component.ID, err)
	}
	defer cleanup()
	args := uvToolInstallArgs(toolArtifact, python)
	if err := runManagedExecutableWithProgress(ctx, uv, args, packageEnvironment(generation, destination, "uv"), reporter, fmt.Sprintf("Installing %s package", component.ID)); err != nil {
		return fmt.Errorf("uv tool install for %s failed: %w", component.ID, err)
	}
	return nil
}

func uvToolInstallArgs(artifactPath, python string) []string {
	// uv infers the package identity and version from the verified wheel. Adding
	// a second requirement (for example strix-agent==1.6.1) makes uv reject the
	// local artifact when its filename/source is already authoritative.
	return []string{"tool", "install", "--python", python, "--force", artifactPath}
}

func toolArtifactFilename(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || parsed.Path == "" || strings.HasSuffix(parsed.Path, "/") || path.Clean(parsed.Path) != parsed.Path {
		return "", errors.New("package artifact URL does not contain a file name")
	}
	filename, err := url.PathUnescape(path.Base(parsed.Path))
	if err != nil || filename == "" || filename == "." || filename == ".." || strings.ContainsAny(filename, "/\\") || strings.ContainsRune(filename, 0) {
		return "", errors.New("package artifact file name is unsafe")
	}
	lower := strings.ToLower(filename)
	if !strings.HasSuffix(lower, ".whl") && !strings.HasSuffix(lower, ".tgz") && !strings.HasSuffix(lower, ".tar.gz") && !strings.HasSuffix(lower, ".zip") && !strings.HasSuffix(lower, ".tar") {
		return "", errors.New("package artifact file name has no supported archive extension")
	}
	for _, char := range filename {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			continue
		}
		switch char {
		case '.', '-', '_', '+':
		default:
			return "", errors.New("package artifact file name contains an unsupported character")
		}
	}
	return filename, nil
}

func materializeToolArtifact(source, rawURL, generation string) (string, func(), error) {
	filename, err := toolArtifactFilename(rawURL)
	if err != nil {
		return "", func() {}, err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return "", func() {}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", func() {}, errors.New("verified package artifact is not a regular file")
	}
	directory, err := os.MkdirTemp(generation, ".baron-artifact-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	destination := filepath.Join(directory, filename)
	input, err := os.Open(source)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		_ = input.Close()
		cleanup()
		return "", func() {}, err
	}
	_, copyErr := io.Copy(output, input)
	inputCloseErr := input.Close()
	outputSyncErr := output.Sync()
	outputCloseErr := output.Close()
	if copyErr != nil || inputCloseErr != nil || outputSyncErr != nil || outputCloseErr != nil {
		cleanup()
		if copyErr != nil {
			return "", func() {}, copyErr
		}
		if inputCloseErr != nil {
			return "", func() {}, inputCloseErr
		}
		if outputSyncErr != nil {
			return "", func() {}, outputSyncErr
		}
		return "", func() {}, outputCloseErr
	}
	return destination, cleanup, nil
}

func findGenerationExecutable(generation string, names []string, components []ComponentID) (string, error) {
	for _, component := range components {
		root := filepath.Join(generation, string(component))
		if candidate, err := FindExecutableNamed(root, names...); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("no managed dependency executable was found")
}

func findGenerationExecutableForPlan(generation string, component ComponentPlan, components []ComponentID) (string, error) {
	names := componentExecutableNamesForPlan(component)
	for _, component := range components {
		root := filepath.Join(generation, string(component))
		if candidate, err := FindExecutableNamed(root, names...); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("no managed dependency executable was found")
}

func componentExecutableNamesForPlan(component ComponentPlan) []string {
	if strings.TrimSpace(component.EntryPoint) != "" {
		return executableAliases(component.EntryPoint)
	}
	return componentExecutableNames(component.ID)
}

func executableAliases(name string) []string {
	name = strings.TrimSpace(name)
	if runtime.GOOS != "windows" {
		return []string{name}
	}
	return []string{name, name + ".cmd", name + ".exe", name + ".bat"}
}

func runManagedExecutable(ctx context.Context, executable string, args []string, environment []string) error {
	return runManagedExecutableWithProgress(ctx, executable, args, environment, nil, "")
}

func runManagedExecutableWithProgress(ctx context.Context, executable string, args []string, environment []string, reporter ProgressReporter, label string) error {
	return runManagedExecutableWithProgressInterval(ctx, executable, args, environment, reporter, label, 5*time.Second)
}

func runManagedExecutableWithProgressInterval(ctx context.Context, executable string, args []string, environment []string, reporter ProgressReporter, label string, interval time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var command *exec.Cmd
	if runtime.GOOS == "windows" && (strings.HasSuffix(strings.ToLower(executable), ".cmd") || strings.HasSuffix(strings.ToLower(executable), ".bat")) {
		command = exec.CommandContext(ctx, "cmd.exe", append([]string{"/d", "/c", executable}, args...)...)
	} else {
		command = exec.CommandContext(ctx, executable, args...)
	}
	command.Env = environment
	if reporter == nil || interval <= 0 {
		output, err := command.CombinedOutput()
		return formatManagedCommandResult(output, err)
	}
	if strings.TrimSpace(label) == "" {
		label = "Managed dependency operation"
	}
	reporter.Step(label + " in progress...")
	type commandResult struct {
		output []byte
		err    error
	}
	done := make(chan commandResult, 1)
	go func() {
		output, err := command.CombinedOutput()
		done <- commandResult{output: output, err: err}
	}()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case result := <-done:
			return formatManagedCommandResult(result.output, result.err)
		case <-ticker.C:
			reporter.Step(label + " still running...")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func formatManagedCommandResult(output []byte, err error) error {
	if err != nil {
		detail := strings.TrimSpace(config.Redact(string(output), nil))
		if detail != "" {
			if len(detail) > 4096 {
				detail = detail[:4096] + "...[truncated]"
			}
			return fmt.Errorf("%w: %s", err, detail)
		}
		return err
	}
	return nil
}

func packageEnvironment(generation, destination, mode string) []string {
	allowed := []string{"HOME", "USERPROFILE", "SystemRoot", "WINDIR", "TEMP", "TMP", "LANG", "LC_ALL", "TZ"}
	values := make([]string, 0, len(allowed)+10)
	for _, key := range allowed {
		if value, ok := os.LookupEnv(key); ok {
			values = append(values, key+"="+value)
		}
	}
	paths := []string{destination, filepath.Join(destination, "bin"), filepath.Join(destination, "Scripts")}
	for _, component := range []ComponentID{ComponentUV, ComponentPython, ComponentNode, ComponentNPM, ComponentPNPM, ComponentStrix, ComponentDSH, ComponentCodex} {
		root := filepath.Join(generation, string(component))
		paths = append(paths, root, filepath.Join(root, "bin"), filepath.Join(root, "Scripts"))
	}
	paths = append(paths, managedExecutableDirectories(generation)...)
	if existing := strings.TrimSpace(os.Getenv("PATH")); existing != "" {
		paths = append(paths, strings.Split(existing, string(os.PathListSeparator))...)
	}
	values = append(values, "PATH="+joinUniquePaths(paths))
	switch mode {
	case "npm":
		values = append(values,
			"npm_config_prefix="+destination,
			"npm_config_ignore_scripts=true",
			"npm_config_audit=false",
			"npm_config_fund=false",
			"npm_config_update_notifier=false",
		)
	case "pnpm":
		values = append(values,
			"npm_config_ignore_scripts=true",
			"npm_config_audit=false",
			"npm_config_fund=false",
			"npm_config_update_notifier=false",
		)
	case "uv":
		values = append(values,
			"UV_TOOL_DIR="+filepath.Join(destination, ".uv-tools"),
			"UV_TOOL_BIN_DIR="+filepath.Join(destination, "bin"),
			"UV_CACHE_DIR="+filepath.Join(generation, ".uv-cache"),
		)
	}
	return values
}

func joinUniquePaths(paths []string) string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		key := path
		if runtime.GOOS == "windows" {
			key = strings.ToLower(path)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, path)
	}
	return strings.Join(result, string(os.PathListSeparator))
}

func validatePackageInstallDestination(generation, destination string) error {
	generation, err := filepath.Abs(filepath.Clean(generation))
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(filepath.Clean(destination))
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(generation, destination)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("package install destination must be inside its generation")
	}
	return nil
}
