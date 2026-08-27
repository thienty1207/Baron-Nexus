// Package permissions manages explicit, Baron-owned launchers for upstream
// tools. It intentionally does not replace the user's dsh/codex binaries or
// modify shell startup files.
package permissions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
)

const LauncherMarker = "baron-nexus: explicit auto-accept launcher v1"

type LauncherPaths struct {
	Directory string
	DSH       string
	Codex     string
}

type Status struct {
	Directory    string
	DSHPath      string
	CodexPath    string
	DSHEnabled   bool
	CodexEnabled bool
}

func Paths(directory string) LauncherPaths {
	directory = filepath.Clean(strings.TrimSpace(directory))
	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".cmd"
	}
	return LauncherPaths{
		Directory: directory,
		DSH:       filepath.Join(directory, "dsh-auto"+extension),
		Codex:     filepath.Join(directory, "codex-auto"+extension),
	}
}

// DefaultDirectory resolves the Baron-owned launcher directory beside global
// state. Passing a custom global path keeps app tests and portable installs
// isolated from the host user's real configuration.
func DefaultDirectory(globalPath string) (string, error) {
	if strings.TrimSpace(globalPath) == "" {
		var err error
		globalPath, err = config.DefaultGlobalStatePath()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(filepath.Dir(filepath.Clean(globalPath)), "bin"), nil
}

// ValidateDirectory permits a launcher directory without granting permission
// to recursively remove or replace the directory itself. The launcher files
// are the only paths this package ever writes or removes.
func ValidateDirectory(directory string) error {
	trimmed := strings.TrimSpace(directory)
	if trimmed == "" || trimmed == "." {
		return errors.New("permission launcher directory is required")
	}
	clean, err := filepath.Abs(trimmed)
	if err != nil {
		return fmt.Errorf("resolve permission launcher directory: %w", err)
	}
	root := filepath.VolumeName(clean) + string(filepath.Separator)
	if clean == root {
		return fmt.Errorf("refusing to use filesystem root as permission launcher directory: %s", directory)
	}
	if home, err := os.UserHomeDir(); err == nil && sameDirectory(clean, home) {
		return fmt.Errorf("refusing to use the home directory as permission launcher directory: %s", directory)
	}
	if info, err := os.Lstat(clean); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("permission launcher directory is not safe: %s", directory)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// DirectoryOnPath reports whether the exact directory is available through
// the current process PATH. Empty and relative PATH entries are ignored so a
// launcher is never silently placed in the current working directory.
func DirectoryOnPath(directory string) bool {
	clean, err := absoluteDirectory(directory)
	if err != nil {
		return false
	}
	for _, entry := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		entry = strings.TrimSpace(entry)
		if entry == "" || !filepath.IsAbs(entry) {
			continue
		}
		candidate, candidateErr := absoluteDirectory(entry)
		if candidateErr == nil && sameDirectory(clean, candidate) {
			return true
		}
	}
	return false
}

// DirectoryIsWritable probes the actual user permission instead of relying on
// mode bits, which are not a reliable ACL signal on Windows.
func DirectoryIsWritable(directory string) bool {
	if err := ValidateDirectory(directory); err != nil {
		return false
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return false
	}
	temporary, err := os.CreateTemp(directory, ".baron-permission-check-*")
	if err != nil {
		return false
	}
	name := temporary.Name()
	closeErr := temporary.Close()
	removeErr := os.Remove(name)
	return closeErr == nil && removeErr == nil
}

func Enable(directory string) (Status, error) {
	paths := Paths(directory)
	if err := ensureDirectory(paths.Directory); err != nil {
		return Status{}, err
	}
	written := make([]string, 0, 2)
	for _, launcher := range []struct {
		path string
		data []byte
	}{
		{path: paths.DSH, data: []byte(dshLauncher())},
		{path: paths.Codex, data: []byte(codexLauncher())},
	} {
		created, err := writeLauncher(launcher.path, launcher.data)
		if err != nil {
			for _, path := range written {
				if createdByBaron, createdErr := launcherWasCreatedByBaron(path); createdErr == nil && createdByBaron {
					_ = os.Remove(path)
				}
			}
			return Status{}, err
		}
		if created {
			written = append(written, launcher.path)
		}
	}
	return Inspect(directory), nil
}

func Disable(directory string) (Status, error) {
	paths := Paths(directory)
	for _, path := range []string{paths.DSH, paths.Codex} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Status{}, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return Status{}, fmt.Errorf("refusing to remove non-regular permission launcher %s", path)
		}
		owned, err := launcherWasCreatedByBaron(path)
		if err != nil {
			return Status{}, err
		}
		if !owned {
			return Status{}, fmt.Errorf("refusing to remove unmarked permission launcher %s", path)
		}
		if err := os.Remove(path); err != nil {
			return Status{}, err
		}
	}
	return Inspect(directory), nil
}

func Inspect(directory string) Status {
	paths := Paths(directory)
	return Status{
		Directory:    paths.Directory,
		DSHPath:      paths.DSH,
		CodexPath:    paths.Codex,
		DSHEnabled:   launcherHasMarker(paths.DSH),
		CodexEnabled: launcherHasMarker(paths.Codex),
	}
}

func Instructions(directory string) string {
	directory = filepath.Clean(directory)
	if DirectoryOnPath(directory) {
		return "Launchers are already on PATH. Run `dsh-auto` and `codex-auto`; no PATH export is required."
	}
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("Run `dsh-auto` and `codex-auto` after adding this directory to PATH:\n  PowerShell: $env:Path = %q + ';' + $env:Path\n  cmd.exe:     set PATH=%s;%%PATH%%", directory, directory)
	}
	return fmt.Sprintf("Run `dsh-auto` and `codex-auto` after adding this directory to PATH:\n  export PATH=%q:$PATH", directory)
}

func ensureDirectory(directory string) error {
	if err := ValidateDirectory(directory); err != nil {
		return err
	}
	directory = filepath.Clean(directory)
	if info, err := os.Lstat(directory); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("permission launcher directory is not safe: %s", directory)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.MkdirAll(directory, 0o700)
}

func writeLauncher(path string, data []byte) (bool, error) {
	created := false
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, fmt.Errorf("refusing to replace non-regular permission launcher %s", path)
		}
		owned, err := launcherWasCreatedByBaron(path)
		if err != nil {
			return false, err
		}
		if !owned {
			return false, fmt.Errorf("refusing to replace unmarked permission launcher %s", path)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		created = true
	} else {
		return false, err
	}
	perm := os.FileMode(0o700)
	if runtime.GOOS == "windows" {
		perm = 0o600
	}
	if err := config.AtomicWriteFile(path, data, perm); err != nil {
		return false, fmt.Errorf("write permission launcher %s: %w", path, err)
	}
	return created, nil
}

func launcherWasCreatedByBaron(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return strings.Contains(string(data), LauncherMarker), nil
}

func launcherHasMarker(path string) bool {
	owned, err := launcherWasCreatedByBaron(path)
	return err == nil && owned
}

func absoluteDirectory(directory string) (string, error) {
	trimmed := strings.TrimSpace(directory)
	if trimmed == "" || !filepath.IsAbs(trimmed) {
		return "", errors.New("permission launcher directory must be absolute")
	}
	clean, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		clean = resolved
	}
	return filepath.Clean(clean), nil
}

func sameDirectory(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func dshLauncher() string {
	if runtime.GOOS == "windows" {
		return "@echo off\r\nrem " + LauncherMarker + "\r\nset \"DSH_PERMISSION_MODE=danger-full-access\"\r\ndsh %*\r\n"
	}
	return "#!/bin/sh\n# " + LauncherMarker + "\nset -eu\nexport DSH_PERMISSION_MODE=danger-full-access\nexec dsh \"$@\"\n"
}

func codexLauncher() string {
	if runtime.GOOS == "windows" {
		return "@echo off\r\nrem " + LauncherMarker + "\r\ncodex --sandbox danger-full-access --ask-for-approval never %*\r\n"
	}
	return "#!/bin/sh\n# " + LauncherMarker + "\nset -eu\nexec codex --sandbox danger-full-access --ask-for-approval never \"$@\"\n"
}
