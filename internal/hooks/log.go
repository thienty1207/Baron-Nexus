package hooks

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/baron-shared-brain/baron/internal/config"
)

func AppendLog(path, message string, secrets []string, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 256 * 1024
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	message = config.Redact(message, secrets)
	if int64(len(message)) > maxBytes {
		if maxBytes <= int64(len("...[truncated]")) {
			message = message[:maxBytes]
		} else {
			message = message[:maxBytes-int64(len("...[truncated]"))] + "...[truncated]"
		}
	}
	if info, err := os.Stat(path); err == nil && info.Size()+int64(len(message)) > maxBytes {
		rotated := path + ".1"
		_ = os.Remove(rotated)
		if err := os.Rename(path, rotated); err != nil {
			return fmt.Errorf("rotate hook log: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.WriteString(message); err != nil {
		return err
	}
	return file.Sync()
}
