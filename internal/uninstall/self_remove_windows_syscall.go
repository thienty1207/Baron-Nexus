//go:build windows

package uninstall

import "syscall"

func hiddenWindowsProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}
