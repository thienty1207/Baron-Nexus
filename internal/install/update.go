package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// UpdateBinary installs a candidate beside the current executable and rolls
// back automatically if validation fails. It is intentionally a library
// operation; the shell installers still require an explicit replacement flag.
func UpdateBinary(current, candidate string, validate func() error) (string, error) {
	if current == "" || candidate == "" {
		return "", errors.New("current and candidate binary paths are required")
	}
	candidateInfo, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("read update candidate: %w", err)
	}
	if candidateInfo.IsDir() {
		return "", errors.New("update candidate is a directory")
	}
	currentInfo, currentErr := os.Stat(current)
	hadCurrent := currentErr == nil
	if currentErr != nil && !errors.Is(currentErr, os.ErrNotExist) {
		return "", currentErr
	}
	backup := current + fmt.Sprintf(".baron-update-backup-%d", time.Now().UTC().UnixNano())
	if hadCurrent {
		if err := copyBinary(current, backup, currentInfo.Mode().Perm()); err != nil {
			return "", fmt.Errorf("backup current binary: %w", err)
		}
	}
	temp, err := os.CreateTemp(filepath.Dir(current), filepath.Base(current)+".update-*")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	input, err := os.Open(candidate)
	if err != nil {
		_ = temp.Close()
		return "", err
	}
	copyErr := copyInto(temp, input)
	_ = input.Close()
	if copyErr != nil {
		_ = temp.Close()
		return "", copyErr
	}
	if err := temp.Chmod(0o755); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tempPath, current); err != nil {
		return "", fmt.Errorf("replace Baron binary: %w", err)
	}
	cleanup = false
	if validate != nil {
		if err := validate(); err != nil {
			if hadCurrent {
				if restoreErr := os.Rename(backup, current); restoreErr != nil {
					return backup, fmt.Errorf("update validation failed: %v; rollback failed: %w", err, restoreErr)
				}
			} else {
				_ = os.Remove(current)
			}
			return backup, fmt.Errorf("update validation failed: %w", err)
		}
	}
	return backup, nil
}

func RollbackBinary(current, backup string) error {
	if current == "" || backup == "" {
		return errors.New("current and backup binary paths are required")
	}
	if _, err := os.Stat(backup); err != nil {
		return err
	}
	return os.Rename(backup, current)
}

func copyBinary(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if err := copyInto(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func copyInto(destination io.Writer, source io.Reader) error {
	_, err := io.Copy(destination, source)
	return err
}
