package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// AtomicWriteFile writes a file using a same-directory temporary file and
// rename. The old file remains intact if any pre-rename step fails.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	return AtomicWriteFileWithOptions(path, data, perm, nil)
}

// AtomicWriteFileWithOptions exposes a fault-injection point for power-loss and
// interrupted-write tests. Production callers should normally use
// AtomicWriteFile.
func AtomicWriteFileWithOptions(path string, data []byte, perm os.FileMode, beforeRename func(string) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tempName := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempName)
		}
	}()

	if err := temp.Chmod(perm); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set temporary file permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if beforeRename != nil {
		if err := beforeRename(tempName); err != nil {
			return err
		}
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("rename temporary file: %w", err)
	}
	removeTemp = false
	if err := syncDirectory(dir); err != nil {
		return err
	}
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		// Windows does not support syncing directory handles. The atomic rename
		// still provides the required behavior there.
		if errors.Is(err, os.ErrInvalid) || strings.Contains(strings.ToLower(err.Error()), "invalid argument") {
			return nil
		}
	}
	return nil
}

// WriteEnv serializes a project-local .env using stable ordering and mode 0600.
func WriteEnv(path string, values map[string]string) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		if !envKeyPattern.MatchString(key) {
			return fmt.Errorf("invalid environment key %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(formatEnvValue(values[key]))
		builder.WriteByte('\n')
	}
	return AtomicWriteFile(path, []byte(builder.String()), 0o600)
}

func ReadEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !envKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("invalid .env line")
		}
		values[key] = parseEnvValue(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func formatEnvValue(value string) string {
	if value == "" || strings.ContainsAny(value, " \t#\"'\\") {
		return strconv.Quote(value)
	}
	return value
}

func parseEnvValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		if value[0] == '"' {
			if decoded, err := strconv.Unquote(value); err == nil {
				return decoded
			}
		}
		return value[1 : len(value)-1]
	}
	return value
}

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9][A-Za-z0-9_-]{5,}`),
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)(?:api[_-]?key|token|secret|admin[-_]?key|user[-_]?key)\s*[:=]\s*[^\s,;]+`),
}

// Redact removes exact loaded secrets first, then common credential-shaped
// values. It is safe to use for bounded logs and memory payloads.
func Redact(input string, exactSecrets []string) string {
	result := input
	secrets := append([]string(nil), exactSecrets...)
	sort.SliceStable(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, secret := range secrets {
		if secret != "" {
			result = strings.ReplaceAll(result, secret, "[REDACTED]")
		}
	}
	for _, pattern := range redactionPatterns {
		result = pattern.ReplaceAllString(result, "[REDACTED]")
	}
	return result
}

func GlobalConfigDir(appName string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if appName == "" {
		appName = "baron"
	}
	return filepath.Join(base, appName), nil
}

// IsPrivateFile reports whether a credential-bearing file is not readable or
// writable by group/other users on Unix. Windows ACLs are handled by the
// platform installer; mode bits there are not a reliable ACL authority.
func IsPrivateFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if runtime.GOOS == "windows" {
		return true, nil
	}
	return info.Mode().Perm()&0o077 == 0, nil
}
