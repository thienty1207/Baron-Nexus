package testsupport

import (
	"runtime"
	"strings"
)

// UnixModeBitsReliable reports whether FileMode.Perm reflects the platform's
// effective file ACL boundary. Windows uses ACLs instead of Unix mode bits.
func UnixModeBitsReliable() bool {
	return runtime.GOOS != "windows"
}

// IsSymlinkPrivilegeError identifies Windows environments where the test
// process cannot create symbolic links. Other symlink errors remain failures.
func IsSymlinkPrivilegeError(err error) bool {
	if runtime.GOOS != "windows" || err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "required privilege") ||
		strings.Contains(message, "privilege is not held") ||
		strings.Contains(message, "symbolic link privilege")
}
