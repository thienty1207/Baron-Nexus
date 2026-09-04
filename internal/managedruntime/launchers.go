package managedruntime

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/baron-shared-brain/baron/internal/config"
)

const LauncherMarker = "baron-nexus: managed runtime launcher v1"

type LauncherSpec struct {
	Name           string
	Target         string
	ClientIdentity string
	ManagedPath    []string
}

type Launcher struct {
	Name           string   `json:"name"`
	Path           string   `json:"path"`
	Target         string   `json:"target"`
	ClientIdentity string   `json:"client_identity,omitempty"`
	ManagedPath    []string `json:"managed_path,omitempty"`
	Collision      bool     `json:"collision,omitempty"`
}

type LauncherReport struct {
	Launchers  []Launcher
	Collisions []string
}

type launcherChange struct {
	path          string
	original      []byte
	originalMode  os.FileMode
	originalExist bool
}

// LauncherTransaction prepares all launcher paths before writing any of them.
// This lets the runtime activation boundary restore the previous launcher set
// if a later write or generation activation fails.
type LauncherTransaction struct {
	changes []launcherChange
	desired map[string][]byte
	applied int
}

func InstallLaunchers(paths Paths, specs []LauncherSpec) (LauncherReport, error) {
	transaction, report, err := PrepareLaunchers(paths, specs)
	if err != nil {
		return LauncherReport{}, err
	}
	if err := transaction.Apply(); err != nil {
		return LauncherReport{}, err
	}
	transaction.Commit()
	return report, nil
}

func PrepareLaunchers(paths Paths, specs []LauncherSpec) (*LauncherTransaction, LauncherReport, error) {
	launcherDirectory, err := paths.launcherDirectory()
	if err != nil {
		return nil, LauncherReport{}, err
	}
	if err := os.MkdirAll(launcherDirectory, 0o700); err != nil {
		return nil, LauncherReport{}, fmt.Errorf("create managed runtime launcher directory: %w", err)
	}
	transaction := &LauncherTransaction{
		changes: make([]launcherChange, 0, len(specs)),
		desired: make(map[string][]byte, len(specs)),
	}
	report := LauncherReport{
		Launchers:  make([]Launcher, 0, len(specs)),
		Collisions: make([]string, 0),
	}
	seenNames := make(map[string]struct{}, len(specs))
	seenPaths := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		name, err := validateLauncherName(spec.Name)
		if err != nil {
			return nil, LauncherReport{}, err
		}
		if _, exists := seenNames[name]; exists {
			return nil, LauncherReport{}, fmt.Errorf("managed runtime launcher %q is duplicated", name)
		}
		seenNames[name] = struct{}{}
		identity, err := normalizeLauncherClientIdentity(spec.ClientIdentity)
		if err != nil {
			return nil, LauncherReport{}, fmt.Errorf("launcher %s client identity: %w", name, err)
		}
		managedPath, err := normalizeLauncherManagedPath(spec.ManagedPath)
		if err != nil {
			return nil, LauncherReport{}, fmt.Errorf("launcher %s managed path: %w", name, err)
		}
		target, err := validateLauncherTarget(paths, spec.Target)
		if err != nil {
			return nil, LauncherReport{}, fmt.Errorf("launcher %s target: %w", name, err)
		}
		barePath := filepath.Join(launcherDirectory, launcherFilename(name))
		selectedPath, collision, err := selectLauncherPath(barePath, name)
		if err != nil {
			return nil, LauncherReport{}, err
		}
		if _, exists := seenPaths[selectedPath]; exists {
			return nil, LauncherReport{}, fmt.Errorf("managed runtime launcher path is duplicated: %s", selectedPath)
		}
		seenPaths[selectedPath] = struct{}{}
		if collision {
			report.Collisions = append(report.Collisions, barePath)
		}
		original, mode, exists, err := readLauncherState(selectedPath)
		if err != nil {
			return nil, LauncherReport{}, err
		}
		transaction.changes = append(transaction.changes, launcherChange{
			path: selectedPath, original: original, originalMode: mode, originalExist: exists,
		})
		transaction.desired[selectedPath] = renderLauncher(target, identity, managedPath)
		report.Launchers = append(report.Launchers, Launcher{
			Name: name, Path: selectedPath, Target: target, ClientIdentity: identity,
			ManagedPath: managedPath, Collision: collision,
		})
	}
	return transaction, report, nil
}

func (t *LauncherTransaction) Apply() error {
	if t == nil {
		return errors.New("managed runtime launcher transaction is nil")
	}
	if t.applied != 0 {
		return errors.New("managed runtime launcher transaction was already applied")
	}
	for index, change := range t.changes {
		data, ok := t.desired[change.path]
		if !ok {
			return fmt.Errorf("managed runtime launcher transaction has no content for %s", change.path)
		}
		if err := config.AtomicWriteFile(change.path, data, launcherMode()); err != nil {
			rollbackErr := t.rollbackApplied(index)
			if rollbackErr != nil {
				return fmt.Errorf("write managed runtime launcher %s: %w; rollback failed: %v", change.path, err, rollbackErr)
			}
			return fmt.Errorf("write managed runtime launcher %s: %w", change.path, err)
		}
		t.applied = index + 1
	}
	return nil
}

func (t *LauncherTransaction) Rollback() error {
	if t == nil {
		return errors.New("managed runtime launcher transaction is nil")
	}
	return t.rollbackApplied(t.applied)
}

func (t *LauncherTransaction) Commit() {
	if t == nil {
		return
	}
	t.changes = nil
	t.desired = nil
	t.applied = 0
}

// RemoveManagedLaunchers removes only marked regular files in the managed
// launcher directory. Unmarked files, symlinks, and directories are preserved.
func RemoveManagedLaunchers(paths Paths) ([]string, error) {
	launcherDirectory, err := paths.launcherDirectory()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(launcherDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0)
	for _, entry := range entries {
		path := filepath.Join(launcherDirectory, entry.Name())
		info, lstatErr := os.Lstat(path)
		if lstatErr != nil {
			return removed, lstatErr
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return removed, readErr
		}
		if !bytes.Contains(data, []byte(LauncherMarker)) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		removed = append(removed, path)
	}
	return removed, nil
}

func (t *LauncherTransaction) rollbackApplied(count int) error {
	if count > len(t.changes) {
		count = len(t.changes)
	}
	var firstErr error
	for index := count - 1; index >= 0; index-- {
		change := t.changes[index]
		if change.originalExist {
			if err := ensureMarkedRegularLauncher(change.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if err := config.AtomicWriteFile(change.path, change.original, change.originalMode); err != nil && firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := ensureMarkedRegularLauncher(change.path); err != nil {
			if !errors.Is(err, os.ErrNotExist) && firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := os.Remove(change.path); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}
	t.applied = 0
	return firstErr
}

func selectLauncherPath(barePath, name string) (string, bool, error) {
	owned, exists, err := launcherOwnership(barePath)
	if err != nil {
		return "", false, err
	}
	if !exists || owned {
		return barePath, false, nil
	}
	alias := filepath.Join(filepath.Dir(barePath), launcherFilename("baron-"+name))
	aliasOwned, aliasExists, aliasErr := launcherOwnership(alias)
	if aliasErr != nil {
		return "", false, aliasErr
	}
	if aliasExists && !aliasOwned {
		return "", false, fmt.Errorf("managed runtime launcher collision for %s and baron-%s; preserving both existing files", name, name)
	}
	return alias, true, nil
}

func readLauncherState(path string) ([]byte, os.FileMode, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("refusing to replace non-regular managed runtime launcher %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false, err
	}
	if !bytes.Contains(data, []byte(LauncherMarker)) {
		return nil, 0, false, fmt.Errorf("refusing to replace unmarked managed runtime launcher %s", path)
	}
	return data, info.Mode().Perm(), true, nil
}

func launcherOwnership(path string) (bool, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, true, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, true, err
	}
	return bytes.Contains(data, []byte(LauncherMarker)), true, nil
}

func ensureMarkedRegularLauncher(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to modify non-regular managed runtime launcher %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Contains(data, []byte(LauncherMarker)) {
		return fmt.Errorf("refusing to modify unmarked managed runtime launcher %s", path)
	}
	return nil
}

func validateLauncherName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || len(value) > 128 {
		return "", errors.New("managed runtime launcher name is required and bounded")
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			continue
		}
		return "", fmt.Errorf("managed runtime launcher name is unsafe: %q", value)
	}
	return value, nil
}

func validateLauncherTarget(paths Paths, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("managed runtime launcher target must be an absolute path")
	}
	target := filepath.Clean(value)
	if err := paths.ValidateOwned(target); err != nil {
		return "", err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("managed runtime launcher target is not a regular executable")
	}
	if !isExecutableFile(target) {
		return "", errors.New("managed runtime launcher target is not executable")
	}
	return target, nil
}

func launcherFilename(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".cmd"
	}
	return name
}

func launcherMode() os.FileMode {
	if runtime.GOOS == "windows" {
		return 0o600
	}
	return 0o700
}

func renderLauncher(target, clientIdentity string, managedPaths ...[]string) []byte {
	var pathEntries []string
	if len(managedPaths) > 0 {
		pathEntries = managedPaths[0]
	}
	if runtime.GOOS == "windows" {
		identityLine := ""
		if clientIdentity != "" {
			identityLine = fmt.Sprintf("@set \"BARON_CLIENT=%s\"\r\n", clientIdentity)
		}
		pathLine := ""
		if len(pathEntries) > 0 {
			pathValue := strings.Join(pathEntries, string(os.PathListSeparator))
			pathLine = fmt.Sprintf("@set \"PATH=%s;%%PATH%%\"\r\n", escapeBatchValue(pathValue))
		}
		return []byte(fmt.Sprintf("@echo off\r\nrem %s\r\n%s%s@call \"%s\" %%*\r\n", LauncherMarker, identityLine, pathLine, escapeBatchValue(target)))
	}
	quoted := "'" + strings.ReplaceAll(target, "'", "'\\''") + "'"
	identityLine := ""
	if clientIdentity != "" {
		identityLine = "export BARON_CLIENT='" + clientIdentity + "'\n"
	}
	pathLine := ""
	if len(pathEntries) > 0 {
		pathValue := "'" + strings.ReplaceAll(strings.Join(pathEntries, string(os.PathListSeparator)), "'", "'\\''") + "'"
		pathLine = "if [ -n \"${PATH:-}\" ]; then\nexport PATH=" + pathValue + ":\"$PATH\"\nelse\nexport PATH=" + pathValue + "\nfi\n"
	}
	return []byte("#!/bin/sh\n# " + LauncherMarker + "\nset -eu\n" + identityLine + pathLine + "exec " + quoted + " \"$@\"\n")
}

func escapeBatchValue(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "%", "%%"), "\"", "\"\"")
}

func normalizeLauncherClientIdentity(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if err := validateLauncherClientIdentity(value); err != nil {
		return "", err
	}
	return value, nil
}

func validateLauncherClientIdentity(value string) error {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "dsh", "codex":
		return nil
	default:
		return fmt.Errorf("unsupported managed client identity %q", value)
	}
}

func normalizeLauncherManagedPath(values []string) ([]string, error) {
	if len(values) > maxComponentCount {
		return nil, errors.New("managed runtime launcher PATH has too many entries")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > maxPathText || !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
			return nil, errors.New("managed runtime launcher PATH entries must be absolute and bounded")
		}
		if strings.ContainsRune(value, rune(os.PathListSeparator)) {
			return nil, errors.New("managed runtime launcher PATH entry contains a path-list separator")
		}
		value = filepath.Clean(value)
		key := value
		if runtime.GOOS == "windows" {
			key = strings.ToLower(value)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}
