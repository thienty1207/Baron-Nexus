package managedruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
)

const defaultProbeEntries = 8192

var reportedVersionPattern = regexp.MustCompile(`(?i)v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?(?:\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?`)

// NativeProbe verifies that a staged component contains an executable and that
// the executable can start. The probe runs with a minimal environment so an
// inherited provider key cannot reach a managed child process.
type NativeProbe struct {
	MaxEntries int
}

func (p NativeProbe) Verify(ctx context.Context, component ComponentPlan, root string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("component %s staging path is unavailable: %w", component.ID, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("component %s staging path is not a directory", component.ID)
	}
	executable, err := findExecutableByNames(root, componentExecutableNamesForPlan(component), p.maxEntries())
	if err != nil {
		return fmt.Errorf("component %s has no executable: %w", component.ID, err)
	}
	args := []string{"--version"}
	if component.ID == ComponentGo {
		args = []string{"version"}
	}
	command, commandErr := probeCommand(ctx, root, executable, args...)
	if commandErr != nil {
		return fmt.Errorf("prepare executable probe: %w", commandErr)
	}
	output, runErr := command.CombinedOutput()
	if runErr != nil {
		if component.ID == ComponentStrix {
			fallback, fallbackPrepareErr := probeCommand(ctx, root, executable, "--help")
			if fallbackPrepareErr != nil {
				return fmt.Errorf("prepare Strix help probe: %w", fallbackPrepareErr)
			}
			fallbackOutput, fallbackErr := fallback.CombinedOutput()
			if fallbackErr == nil && strings.TrimSpace(component.Version) == "" {
				return nil
			}
			if fallbackErr == nil && versionInOutput(fallbackOutput, component.Version) {
				return nil
			}
		}
		detail := strings.TrimSpace(config.Redact(string(output), nil))
		if len(detail) > 4096 {
			detail = detail[:4096] + "...[truncated]"
		}
		if detail != "" {
			return fmt.Errorf("executable probe failed: %w: %s", runErr, detail)
		}
		return fmt.Errorf("executable probe failed: %w", runErr)
	} else if len(output) == 0 && component.ID != ComponentStrix {
		return errors.New("executable returned no version output")
	}
	if expected := strings.TrimSpace(component.Version); expected != "" && !versionInOutput(output, expected) {
		return fmt.Errorf("component %s version mismatch: expected %s", component.ID, expected)
	}
	return nil
}

func probeCommand(ctx context.Context, root, executable string, args ...string) (*exec.Cmd, error) {
	if isJavaScriptEntryPoint(executable) {
		nodeRoot := filepath.Join(filepath.Dir(root), string(ComponentNode))
		node, err := FindExecutableNamed(nodeRoot, "node")
		if err != nil {
			return nil, fmt.Errorf("resolve managed Node for JavaScript entry point: %w", err)
		}
		command := exec.CommandContext(ctx, node, append([]string{executable}, args...)...)
		command.Dir = root
		command.Env = probeEnvironment(root, filepath.Dir(executable), filepath.Dir(node))
		return command, nil
	}
	environment := probeEnvironment(root, filepath.Dir(executable))
	if runtime.GOOS == "windows" && (strings.HasSuffix(strings.ToLower(executable), ".cmd") || strings.HasSuffix(strings.ToLower(executable), ".bat")) {
		command := exec.CommandContext(ctx, "cmd", append([]string{"/d", "/c", executable}, args...)...)
		command.Dir = root
		command.Env = environment
		return command, nil
	}
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = root
	command.Env = environment
	return command, nil
}

func isJavaScriptEntryPoint(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs":
		return true
	default:
		return false
	}
}

func versionInOutput(output []byte, expected string) bool {
	expectedVersion, err := parseSemanticVersion(expected)
	if err != nil {
		return false
	}
	text := string(output)
	for _, match := range reportedVersionPattern.FindAllStringIndex(text, -1) {
		if match[0] > 0 && isVersionContinuation(text[match[0]-1]) {
			continue
		}
		if match[1] < len(text) && isVersionContinuation(text[match[1]]) {
			continue
		}
		reported, parseErr := parseSemanticVersion(text[match[0]:match[1]])
		if parseErr == nil && reported == expectedVersion {
			return true
		}
	}
	return false
}

func isVersionContinuation(char byte) bool {
	return (char >= '0' && char <= '9') || char == '.' || char == '-' || char == '+'
}

func (p NativeProbe) maxEntries() int {
	if p.MaxEntries > 0 {
		return p.MaxEntries
	}
	return defaultProbeEntries
}

func findComponentExecutable(root string, component ComponentID, maxEntries int) (string, error) {
	return findExecutableByNames(root, componentExecutableNames(component), maxEntries)
}

// FindExecutable returns one known executable for a staged component. It is
// exported for the app-level command runner so managed agent commands cannot
// silently resolve to a different system installation.
func FindExecutable(root string, component ComponentID) (string, error) {
	return findComponentExecutable(root, component, defaultProbeEntries)
}

// FindExecutableNamed resolves an alias such as uvx or npx inside one staged
// component without consulting the process PATH.
func FindExecutableNamed(root string, names ...string) (string, error) {
	return findExecutableByNames(root, names, defaultProbeEntries)
}

func findExecutableByNames(root string, names []string, maxEntries int) (string, error) {
	names = expandExecutableNames(names)
	byName := make(map[string]struct{}, len(names))
	for _, name := range names {
		byName[strings.ToLower(name)] = struct{}{}
	}
	var found string
	foundPriority := int(^uint(0) >> 1)
	consider := func(candidate string) {
		if !isExecutableCandidate(root, candidate) {
			return
		}
		priority := executableCandidatePriority(candidate)
		if found == "" || priority < foundPriority || (priority == foundPriority && candidate < found) {
			found, foundPriority = candidate, priority
		}
	}
	for _, candidate := range directExecutableCandidates(root, names) {
		consider(candidate)
	}
	if found != "" {
		return found, nil
	}
	entries := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > maxEntries {
			return errors.New("component contains too many files to probe")
		}
		if entry.IsDir() {
			if path != root && entry.Type()&os.ModeSymlink != 0 {
				return filepath.SkipDir
			}
			return nil
		}
		if !isExecutableCandidate(root, path) {
			return nil
		}
		if _, ok := byName[strings.ToLower(entry.Name())]; ok {
			consider(path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", errors.New("no known runtime executable was found")
	}
	return found, nil
}

func executableCandidatePriority(path string) int {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe":
		return 0
	case ".cmd":
		return 1
	case ".bat":
		return 2
	case ".com":
		return 3
	case "":
		return 4
	case ".ps1":
		return 5
	default:
		return 6
	}
}

func expandExecutableNames(names []string) []string {
	if runtime.GOOS != "windows" {
		return names
	}
	seen := make(map[string]struct{}, len(names)*4)
	result := make([]string, 0, len(names)*4)
	for _, name := range names {
		for _, candidate := range []string{name, name + ".cmd", name + ".exe", name + ".bat"} {
			if filepath.Ext(name) != "" && candidate != name {
				continue
			}
			key := strings.ToLower(candidate)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, candidate)
		}
	}
	return result
}

func isExecutableCandidate(root, path string) bool {
	if !pathWithin(root, path) {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil || !pathWithin(root, resolved) {
			return false
		}
	}
	return isExecutableFile(path)
}

func pathWithin(root, target string) bool {
	rootAbs, rootErr := filepath.Abs(filepath.Clean(root))
	targetAbs, targetErr := filepath.Abs(filepath.Clean(target))
	if rootErr != nil || targetErr != nil {
		return false
	}
	relative, relErr := filepath.Rel(rootAbs, targetAbs)
	if relErr != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return false
	}
	return true
}

func directExecutableCandidates(root string, names []string) []string {
	directories := []string{
		root,
		filepath.Join(root, "bin"),
		filepath.Join(root, "Scripts"),
		filepath.Join(root, "usr", "bin"),
		filepath.Join(root, "node_modules", ".bin"),
		filepath.Join(root, "package", "bin"),
	}
	// Official runtime archives often keep one top-level directory (for
	// example Go extracts as go/bin/go). Check that bounded layout before the
	// recursive walk so large, valid archives do not hit the probe entry limit.
	for _, name := range names {
		if !isSafeExecutableName(name) {
			continue
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if base == "" || base == "." || base == ".." {
			continue
		}
		archiveRoot := filepath.Join(root, base)
		directories = append(directories,
			filepath.Join(archiveRoot, "bin"),
			filepath.Join(archiveRoot, "Scripts"),
			filepath.Join(archiveRoot, "usr", "bin"),
		)
	}
	result := make([]string, 0, len(directories)*len(names))
	for _, directory := range directories {
		for _, name := range names {
			if !isSafeExecutableName(name) {
				continue
			}
			result = append(result, filepath.Join(directory, name))
		}
	}
	return result
}

func isSafeExecutableName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsAny(name, `/\\`)
}

func componentExecutableNames(component ComponentID) []string {
	var names []string
	switch component {
	case ComponentUV:
		names = []string{"uv", "uv.exe", "uvx", "uvx.exe"}
	case ComponentPython:
		names = []string{"python", "python3", "python.exe", "python3.exe"}
	case ComponentStrix:
		names = []string{"strix", "strix.exe", "strix.cmd"}
	case ComponentBun:
		names = []string{"bun", "bun.exe"}
	case ComponentGo:
		names = []string{"go", "go.exe"}
	case ComponentNode:
		names = []string{"node", "node.exe"}
	case ComponentNPM:
		names = []string{"npm", "npm.cmd", "npm.exe", "npx", "npx.cmd", "npx.exe"}
	case ComponentPNPM:
		names = []string{"pnpm", "pnpm.cmd", "pnpm.exe"}
	case ComponentDSH:
		names = []string{"dsh", "dsh.cmd", "dsh.exe"}
	case ComponentCodex:
		names = []string{"codex", "codex.cmd", "codex.exe"}
	default:
		names = []string{string(component), string(component) + ".exe"}
	}
	sort.Strings(names)
	return names
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

func probeEnvironment(root string, extraPaths ...string) []string {
	allowed := []string{"HOME", "USERPROFILE", "SystemRoot", "WINDIR", "TEMP", "TMP", "LANG", "LC_ALL", "TZ"}
	result := make([]string, 0, len(allowed)+1)
	for _, key := range allowed {
		if value, ok := os.LookupEnv(key); ok {
			result = append(result, key+"="+value)
		}
	}
	pathEntries := []string{root, filepath.Join(root, "bin"), filepath.Join(root, "Scripts")}
	// Package managers commonly create entry-point shims that invoke a sibling
	// runtime (for example npm's dsh shim invoking node). Keep that lookup
	// inside the managed generation rather than relying on the host PATH.
	generation := filepath.Dir(root)
	for _, component := range []ComponentID{ComponentUV, ComponentPython, ComponentNode, ComponentNPM, ComponentPNPM, ComponentStrix, ComponentDSH, ComponentCodex} {
		componentRoot := filepath.Join(generation, string(component))
		pathEntries = append(pathEntries, componentRoot, filepath.Join(componentRoot, "bin"), filepath.Join(componentRoot, "Scripts"))
	}
	pathEntries = append(pathEntries, managedExecutableDirectories(generation)...)
	pathEntries = append(pathEntries, extraPaths...)
	pathValue := joinProbePaths(pathEntries)
	if existing, ok := os.LookupEnv("PATH"); ok && existing != "" {
		pathValue += string(os.PathListSeparator) + existing
	}
	result = append(result, "PATH="+pathValue)
	return result
}

func managedExecutableDirectories(generation string) []string {
	components := []ComponentID{ComponentUV, ComponentPython, ComponentNode, ComponentNPM, ComponentPNPM, ComponentStrix, ComponentDSH, ComponentCodex}
	directories := make([]string, 0, len(components))
	for _, component := range components {
		root := filepath.Join(generation, string(component))
		if executable, err := FindExecutableNamed(root, componentExecutableNames(component)...); err == nil {
			directories = append(directories, filepath.Dir(executable))
		}
	}
	return directories
}

func joinProbePaths(paths []string) string {
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
