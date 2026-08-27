//go:build windows

package uninstall

import (
	"fmt"
	"os/exec"
	"strings"
)

func scheduleWindowsExecutableRemoval(path string, removeErr error) error {
	command := fmt.Sprintf("ping 127.0.0.1 -n 2 >nul & del /f /q \"%s\"", strings.ReplaceAll(path, "\"", ""))
	process := exec.Command("cmd.exe", "/d", "/c", command)
	process.SysProcAttr = hiddenWindowsProcessAttributes()
	if err := process.Start(); err != nil {
		return fmt.Errorf("remove Baron executable (initial error: %v): %w", removeErr, err)
	}
	return nil
}
