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

// InstallEmbeddedDSHAdapter materializes the versioned Baron DSH bundle under
// Baron-owned global state. The adapter is then installed into the DSH profile
// through the official dsh plugin command.
func InstallEmbeddedDSHAdapter(target string) error {
	if target == "" {
		return errors.New("DSH adapter target is required")
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("DSH adapter target is not a safe directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	return fs.WalkDir(dshAdapterAssets, "assets/dsh-adapter", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := dshAdapterAssets.ReadFile(path)
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		perm := os.FileMode(0o600)
		if name == "index.js" {
			perm = 0o644
		}
		if err := config.AtomicWriteFile(filepath.Join(target, name), data, perm); err != nil {
			return fmt.Errorf("write embedded DSH adapter %s: %w", name, err)
		}
		return nil
	})
}
