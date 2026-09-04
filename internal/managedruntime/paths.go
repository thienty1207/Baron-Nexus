package managedruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ResolvePaths(base string) (Paths, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return Paths{}, errors.New("managed runtime root is required")
	}
	root, err := filepath.Abs(filepath.Clean(base))
	if err != nil {
		return Paths{}, fmt.Errorf("resolve managed runtime root: %w", err)
	}
	root = filepath.Clean(root)
	if isFilesystemRoot(root) {
		return Paths{}, errors.New("managed runtime root cannot be a filesystem root")
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil && samePath(root, home) {
		return Paths{}, errors.New("managed runtime root cannot be the user home directory")
	}
	if err := rejectSymlinkComponents(root); err != nil {
		return Paths{}, err
	}
	paths := Paths{
		Root:              root,
		Generations:       filepath.Join(root, "generations"),
		Cache:             filepath.Join(root, "cache"),
		Credentials:       filepath.Join(root, "credentials"),
		Receipts:          filepath.Join(root, "receipts"),
		Bin:               filepath.Join(root, "bin"),
		LauncherDirectory: filepath.Join(root, "bin"),
		Current:           filepath.Join(root, "current.json"),
		Previous:          filepath.Join(root, "previous.json"),
		Operations:        filepath.Join(root, "operations"),
	}
	for _, child := range []string{paths.Generations, paths.Cache, paths.Credentials, paths.Receipts, paths.Bin, paths.Current, paths.Previous, paths.Operations} {
		if err := paths.ValidateOwned(child); err != nil {
			return Paths{}, err
		}
	}
	return paths, nil
}

func (p Paths) launcherDirectory() (string, error) {
	directory := strings.TrimSpace(p.LauncherDirectory)
	if directory == "" {
		directory = strings.TrimSpace(p.Bin)
	}
	if directory == "" {
		return "", errors.New("managed runtime launcher directory is required")
	}
	directory, err := filepath.Abs(filepath.Clean(directory))
	if err != nil {
		return "", fmt.Errorf("resolve managed runtime launcher directory: %w", err)
	}
	if isFilesystemRoot(directory) {
		return "", errors.New("managed runtime launcher directory cannot be a filesystem root")
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil && samePath(directory, home) {
		return "", errors.New("managed runtime launcher directory cannot be the user home directory")
	}
	if err := rejectSymlinkComponents(directory); err != nil {
		return "", err
	}
	if info, statErr := os.Lstat(directory); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", errors.New("managed runtime launcher directory is not a directory")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect managed runtime launcher directory: %w", statErr)
	}
	return directory, nil
}

// LauncherDirectoryPath returns the validated directory for user-facing
// managed launchers. It may be separate from the private runtime root when
// Baron itself is installed in an already-discoverable bin directory.
func (p Paths) LauncherDirectoryPath() (string, error) {
	return p.launcherDirectory()
}

// ValidateLauncherPath accepts one file directly below the configured
// launcher directory and rejects directory traversal or symlinked entries.
func (p Paths) ValidateLauncherPath(path string) error {
	directory, err := p.launcherDirectory()
	if err != nil {
		return err
	}
	path, err = filepath.Abs(filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		return err
	}
	if !samePath(filepath.Dir(path), directory) {
		return errors.New("managed runtime launcher path is outside the launcher directory")
	}
	if strings.TrimSpace(filepath.Base(path)) == "" || filepath.Base(path) == "." || filepath.Base(path) == ".." {
		return errors.New("managed runtime launcher path is invalid")
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("managed runtime launcher path is a symlink")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	return nil
}

func (p Paths) Generation(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("managed runtime generation ID is required")
	}
	if id == "." || id == ".." || filepath.IsAbs(id) || strings.ContainsAny(id, `/\\`) || filepath.Clean(id) != id {
		return "", errors.New("managed runtime generation ID is not a safe path component")
	}
	path := filepath.Join(p.Generations, id)
	if err := p.ValidateOwned(path); err != nil {
		return "", err
	}
	return path, nil
}

func (p Paths) ValidateOwned(path string) error {
	if strings.TrimSpace(p.Root) == "" || strings.TrimSpace(path) == "" {
		return errors.New("managed runtime ownership path is required")
	}
	root, err := filepath.Abs(filepath.Clean(p.Root))
	if err != nil {
		return fmt.Errorf("resolve managed runtime ownership root: %w", err)
	}
	target, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("resolve managed runtime ownership path: %w", err)
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("managed runtime path escapes Baron root: %s", path)
	}
	if err := rejectSymlinkComponents(target); err != nil {
		return err
	}
	return nil
}

func rejectSymlinkComponents(path string) error {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	current := volume + string(filepath.Separator)
	remainder := strings.TrimPrefix(path, current)
	if volume == "" {
		current = string(filepath.Separator)
		remainder = strings.TrimPrefix(path, current)
	}
	for _, part := range splitPathParts(remainder) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect managed runtime path %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("managed runtime path contains a symlink: %s", current)
		}
	}
	return nil
}

func splitPathParts(path string) []string {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	return parts
}

func isFilesystemRoot(path string) bool {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	if volume != "" {
		return clean == volume+string(filepath.Separator)
	}
	return clean == string(filepath.Separator)
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(filepath.Clean(left))
	rightAbs, rightErr := filepath.Abs(filepath.Clean(right))
	if leftErr != nil || rightErr != nil {
		return false
	}
	if os.PathSeparator == '\\' {
		return strings.EqualFold(leftAbs, rightAbs)
	}
	return leftAbs == rightAbs
}
