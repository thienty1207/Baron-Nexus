//go:build !windows

package uninstall

import (
	"fmt"
	"runtime"
)

func scheduleWindowsExecutableRemoval(path string, removeErr error) error {
	return fmt.Errorf("remove Baron executable on %s (initial error: %v): %w", runtime.GOOS, removeErr, removeErr)
}
