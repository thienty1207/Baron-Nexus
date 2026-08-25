package install

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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
	return installEmbeddedAdapter(dshAdapterAssets, "assets/dsh-adapter", target, "DSH")
}

// InstallEmbeddedCodexAdapter materializes the Baron Codex lifecycle bridge
// under Baron-owned global state. Codex hooks continue to use the direct Go
// command for the path-safe default; this package is also available to
// runtimes that require an explicit Node hook bridge.
func InstallEmbeddedCodexAdapter(target string) error {
	return installEmbeddedAdapter(codexAdapterAssets, "assets/codex-adapter", target, "Codex")
}

func installEmbeddedAdapter(assets embed.FS, root, target, label string) error {
	if target == "" {
		return fmt.Errorf("%s adapter target is required", label)
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%s adapter target is not a safe directory", label)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	return fs.WalkDir(assets, root, func(path string, entry fs.DirEntry, walkErr error) error {
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
		if err := config.AtomicWriteFile(filepath.Join(target, name), data, perm); err != nil {
			return fmt.Errorf("write embedded %s adapter %s: %w", label, name, err)
		}
		return nil
	})
}
