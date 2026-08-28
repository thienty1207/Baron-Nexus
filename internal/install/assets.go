package install

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/baron-shared-brain/baron/internal/config"
)

//go:embed assets/dsh-adapter/*
var dshAdapterAssets embed.FS

//go:embed assets/codex-adapter/*
var codexAdapterAssets embed.FS

// InstallEmbeddedDSHAdapter materializes the versioned Baron DSH bundle under
// Baron-owned global state. The adapter is then installed into the DSH profile
// through the official dsh plugin command.
func InstallEmbeddedDSHAdapter(target string) error {
	_, err := InstallEmbeddedDSHAdapterWithChange(target)
	return err
}

func InstallEmbeddedDSHAdapterWithChange(target string) (bool, error) {
	return installEmbeddedAdapterWithChange(dshAdapterAssets, "assets/dsh-adapter", target, "DSH")
}

// InstallEmbeddedCodexAdapter materializes the Baron Codex lifecycle bridge
// under Baron-owned global state. Codex hooks continue to use the direct Go
// command for the path-safe default; this package is also available to
// runtimes that require an explicit Node hook bridge.
func InstallEmbeddedCodexAdapter(target string) error {
	_, err := InstallEmbeddedCodexAdapterWithChange(target)
	return err
}

func InstallEmbeddedCodexAdapterWithChange(target string) (bool, error) {
	return installEmbeddedAdapterWithChange(codexAdapterAssets, "assets/codex-adapter", target, "Codex")
}

func installEmbeddedAdapterWithChange(assets embed.FS, root, target, label string) (bool, error) {
	if target == "" {
		return false, fmt.Errorf("%s adapter target is required", label)
	}
	targetExisted := false
	if info, err := os.Lstat(target); err == nil {
		targetExisted = true
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return false, fmt.Errorf("%s adapter target is not a safe directory", label)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return false, err
	}
	changed := !targetExisted
	err := fs.WalkDir(assets, root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := assets.ReadFile(path)
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		perm := os.FileMode(0o600)
		if name == "index.js" {
			perm = 0o644
		}
		filePath := filepath.Join(target, name)
		if existing, readErr := os.ReadFile(filePath); readErr == nil {
			if info, statErr := os.Stat(filePath); statErr == nil && bytes.Equal(existing, data) {
				if runtime.GOOS == "windows" || info.Mode().Perm() == perm {
					return nil
				}
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		if err := config.AtomicWriteFile(filePath, data, perm); err != nil {
			return fmt.Errorf("write embedded %s adapter %s: %w", label, name, err)
		}
		changed = true
		return nil
	})
	return changed, err
}
