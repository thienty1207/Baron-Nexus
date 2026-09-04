package uninstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/managedruntime"
)

// PurgeTarget is an explicitly receipt-backed filesystem target. Baron never
// infers ownership from a familiar filename or a package-manager location.
type PurgeTarget struct {
	Path       string
	Kind       string
	BaronOwned bool
}

type PurgeOptions struct {
	Root              string
	LauncherDirectory string
	Targets           []PurgeTarget
	DryRun            bool
}

type PurgeReport struct {
	Removed   []string
	Skipped   []string
	Preserved []string
	Failed    []string
}

// ManagedPurgeTargets derives the only recursive purge boundary from the
// persisted managed-runtime root and generation metadata. The resulting list
// contains no user-supplied arbitrary paths and every target is Baron-owned.
func ManagedPurgeTargets(state config.ManagedRuntimeState) ([]PurgeTarget, error) {
	paths, err := managedPurgePaths(state.Root, state.LauncherDirectory)
	if err != nil {
		return nil, err
	}
	targets := make([]PurgeTarget, 0, 16)
	add := func(path, kind string) error {
		if err := paths.ValidateOwned(path); err != nil {
			return err
		}
		path = filepath.Clean(path)
		for _, existing := range targets {
			if samePath(existing.Path, path) {
				return nil
			}
		}
		targets = append(targets, PurgeTarget{Path: path, Kind: kind, BaronOwned: true})
		return nil
	}
	for _, item := range []struct {
		path string
		kind string
	}{
		{paths.Root, "managed-root"},
		{paths.Generations, "generations"},
		{paths.Cache, "cache"},
		{paths.Credentials, "credentials"},
		{paths.Receipts, "receipts"},
		{paths.Bin, "launchers"},
		{paths.Current, "activation-current"},
		{paths.Previous, "activation-previous"},
		{paths.Operations, "operations"},
	} {
		if err := add(item.path, item.kind); err != nil {
			return nil, err
		}
	}
	for _, generation := range []struct {
		id   string
		kind string
	}{
		{state.CurrentGeneration, "current-generation"},
		{state.PreviousGeneration, "previous-generation"},
	} {
		if strings.TrimSpace(generation.id) == "" {
			continue
		}
		path, generationErr := paths.Generation(generation.id)
		if generationErr != nil {
			return nil, generationErr
		}
		if err := add(path, generation.kind); err != nil {
			return nil, err
		}
	}
	for _, receipt := range state.Receipts {
		receipt = filepath.Clean(strings.TrimSpace(receipt))
		if receipt == "." || receipt == "" {
			continue
		}
		if !filepath.IsAbs(receipt) {
			return nil, fmt.Errorf("managed runtime receipt path must be absolute: %s", receipt)
		}
		if err := add(receipt, "receipt"); err != nil {
			return nil, err
		}
	}
	for _, launcher := range state.Launchers {
		launcher = filepath.Clean(strings.TrimSpace(launcher))
		if launcher == "." || launcher == "" {
			continue
		}
		if err := paths.ValidateLauncherPath(launcher); err != nil {
			return nil, fmt.Errorf("validate managed runtime launcher path: %w", err)
		}
		duplicate := false
		for _, existing := range targets {
			if samePath(existing.Path, launcher) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			targets = append(targets, PurgeTarget{Path: launcher, Kind: "launcher", BaronOwned: true})
		}
	}
	return targets, nil
}

func managedPurgePaths(root, launcherDirectory string) (managedruntime.Paths, error) {
	paths, err := managedruntime.ResolvePaths(root)
	if err != nil {
		return managedruntime.Paths{}, err
	}
	if strings.TrimSpace(launcherDirectory) != "" {
		paths.LauncherDirectory = launcherDirectory
	}
	if _, err := paths.LauncherDirectoryPath(); err != nil {
		return managedruntime.Paths{}, err
	}
	return paths, nil
}

func validateManagedPurgeTarget(options PurgeOptions, target PurgeTarget) error {
	if !target.BaronOwned {
		return nil
	}
	path := strings.TrimSpace(target.Path)
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("managed purge target must be an absolute path")
	}
	if strings.TrimSpace(target.Kind) == "" {
		return errors.New("managed purge target kind is required")
	}
	if err := rejectDangerousPath(path); err != nil {
		return err
	}
	root := strings.TrimSpace(options.Root)
	if root == "" {
		return errors.New("managed purge root is required")
	}
	paths, err := managedPurgePaths(root, options.LauncherDirectory)
	if err != nil {
		return err
	}
	if target.Kind == "launcher" {
		launcherDirectory, launcherErr := paths.LauncherDirectoryPath()
		if launcherErr != nil {
			return launcherErr
		}
		if err := paths.ValidateLauncherPath(path); err != nil {
			return err
		}
		return validateManagedPathComponents(launcherDirectory, path)
	}
	if err := paths.ValidateOwned(path); err != nil {
		return err
	}
	return validateManagedPathComponents(paths.Root, path)
}

func validateManagedPathComponents(root, target string) error {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return fmt.Errorf("resolve managed purge path components: %w", err)
	}
	if relative == "." {
		return nil
	}
	current := filepath.Clean(root)
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return fmt.Errorf("inspect managed purge path component %s: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("managed purge path contains a symlink: %s", current)
		}
		if current != filepath.Clean(target) && !info.IsDir() {
			return fmt.Errorf("managed purge path contains a non-directory component: %s", current)
		}
	}
	return nil
}

// PurgeManagedRuntime removes only prevalidated Baron-owned targets. All
// targets are validated before the first mutation, so an unsafe receipt or
// path cannot cause a partial purge of otherwise safe targets.
func PurgeManagedRuntime(ctx context.Context, options PurgeOptions) PurgeReport {
	report := PurgeReport{}
	owned := make([]PurgeTarget, 0, len(options.Targets))
	seen := map[string]struct{}{}
	for _, target := range options.Targets {
		if !target.BaronOwned {
			report.Preserved = append(report.Preserved, filepath.Clean(strings.TrimSpace(target.Path))+" (not Baron-owned)")
			continue
		}
		if err := validateManagedPurgeTarget(options, target); err != nil {
			report.Failed = append(report.Failed, fmt.Sprintf("%s (%s)", target.Path, err))
			continue
		}
		path := filepath.Clean(target.Path)
		key := path
		if strings.EqualFold(string(filepath.Separator), "\\") {
			key = strings.ToLower(path)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		owned = append(owned, PurgeTarget{Path: path, Kind: target.Kind, BaronOwned: true})
	}
	if len(report.Failed) > 0 {
		return report
	}
	sort.SliceStable(owned, func(i, j int) bool {
		return managedPathDepth(owned[i].Path) > managedPathDepth(owned[j].Path)
	})
	for _, target := range owned {
		if err := ctx.Err(); err != nil {
			report.Failed = append(report.Failed, fmt.Sprintf("%s (operation interrupted: %v)", target.Path, err))
			continue
		}
		if options.DryRun {
			report.Skipped = append(report.Skipped, target.Path+" (dry-run)")
			continue
		}
		info, err := os.Lstat(target.Path)
		if errors.Is(err, os.ErrNotExist) {
			report.Skipped = append(report.Skipped, target.Path+" (already absent)")
			continue
		}
		if err != nil {
			report.Failed = append(report.Failed, fmt.Sprintf("%s (inspect: %v)", target.Path, err))
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			report.Failed = append(report.Failed, target.Path+" (symlink refused)")
			continue
		}
		if target.Kind == "launcher" {
			if info.IsDir() || !info.Mode().IsRegular() {
				report.Preserved = append(report.Preserved, target.Path+" (not a managed launcher file)")
				continue
			}
			data, readErr := os.ReadFile(target.Path)
			if readErr != nil {
				report.Failed = append(report.Failed, fmt.Sprintf("%s (inspect launcher ownership: %v)", target.Path, readErr))
				continue
			}
			if !strings.Contains(string(data), managedruntime.LauncherMarker) {
				report.Preserved = append(report.Preserved, target.Path+" (launcher ownership marker unavailable)")
				continue
			}
		}
		if info.IsDir() {
			err = os.RemoveAll(target.Path)
		} else {
			err = os.Remove(target.Path)
		}
		if err != nil {
			report.Failed = append(report.Failed, fmt.Sprintf("%s (remove: %v)", target.Path, err))
			continue
		}
		report.Removed = append(report.Removed, target.Path)
	}
	return report
}

func managedPathDepth(path string) int {
	return len(strings.FieldsFunc(filepath.Clean(path), func(r rune) bool { return r == '/' || r == '\\' }))
}
