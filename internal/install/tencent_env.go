package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
)

// TencentRuntimeConfig contains only the values needed to start the official
// global-images stack. Secrets stay in the managed .env file with mode 0600;
// they are never copied into Baron global/project state or receipts.
type TencentRuntimeConfig struct {
	MemoryLLMBaseURL    string
	MemoryLLMAPIKey     string
	MemoryLLMModel      string
	MemoryLLMProtocol   string
	ProxyUpstreamURL    string
	ProxyUpstreamAPIKey string
	ProxyUpstreamModel  string
	KnowledgePublicURL  string
}

func (c TencentRuntimeConfig) MissingProviderValues() []string {
	missing := []string{}
	if isMissingEnvValue(c.MemoryLLMBaseURL) {
		missing = append(missing, "BARON_TENCENT_MEMORY_LLM_BASE_URL")
	}
	if isMissingEnvValue(c.MemoryLLMAPIKey) {
		missing = append(missing, "BARON_TENCENT_MEMORY_LLM_API_KEY")
	}
	if isMissingEnvValue(c.MemoryLLMModel) {
		missing = append(missing, "BARON_TENCENT_MEMORY_LLM_MODEL")
	}
	return missing
}

// ResolveTencentRuntimeConfig accepts explicit Baron names first, then common
// provider names. This lets a user run one init command after exporting their
// provider credentials without Baron inventing or printing a credential.
func ResolveTencentRuntimeConfig(values map[string]string) TencentRuntimeConfig {
	return normalizeTencentRuntimeConfig(resolveExplicitTencentRuntimeConfig(values))
}

func resolveExplicitTencentRuntimeConfig(values map[string]string) TencentRuntimeConfig {
	get := func(names ...string) string {
		for _, name := range names {
			if value := strings.TrimSpace(values[name]); value != "" {
				return value
			}
		}
		return ""
	}
	memoryURL := get("BARON_TENCENT_MEMORY_LLM_BASE_URL", "BARON_TENCENT_LLM_BASE_URL", "MEMORY_LLM_BASE_URL", "DEEPSEEK_BASE_URL", "OPENAI_BASE_URL")
	memoryKey := get("BARON_TENCENT_MEMORY_LLM_API_KEY", "BARON_TENCENT_LLM_API_KEY", "MEMORY_LLM_API_KEY", "DEEPSEEK_API_KEY", "OPENAI_API_KEY")
	memoryModel := get("BARON_TENCENT_MEMORY_LLM_MODEL", "BARON_TENCENT_LLM_MODEL", "MEMORY_LLM_MODEL", "DEEPSEEK_MODEL", "OPENAI_MODEL")
	proxyURL := get("BARON_TENCENT_PROXY_UPSTREAM_URL", "PROXY_UPSTREAM_URL")
	proxyKey := get("BARON_TENCENT_PROXY_UPSTREAM_API_KEY", "PROXY_UPSTREAM_API_KEY")
	proxyModel := get("BARON_TENCENT_PROXY_UPSTREAM_MODEL", "PROXY_UPSTREAM_MODEL")
	return TencentRuntimeConfig{
		MemoryLLMBaseURL:    memoryURL,
		MemoryLLMAPIKey:     memoryKey,
		MemoryLLMModel:      memoryModel,
		MemoryLLMProtocol:   get("BARON_TENCENT_MEMORY_LLM_PROTOCOL", "BARON_TENCENT_LLM_PROTOCOL", "MEMORY_LLM_PROTOCOL"),
		ProxyUpstreamURL:    proxyURL,
		ProxyUpstreamAPIKey: proxyKey,
		ProxyUpstreamModel:  proxyModel,
		KnowledgePublicURL:  get("BARON_TENCENT_KNOWLEDGE_PUBLIC_URL", "KNOWLEDGE_PUBLIC_BASE_URL"),
	}
}

func normalizeTencentRuntimeConfig(values TencentRuntimeConfig) TencentRuntimeConfig {
	if isMissingEnvValue(values.ProxyUpstreamURL) {
		values.ProxyUpstreamURL = values.MemoryLLMBaseURL
	}
	if isMissingEnvValue(values.ProxyUpstreamAPIKey) {
		values.ProxyUpstreamAPIKey = values.MemoryLLMAPIKey
	}
	if isMissingEnvValue(values.ProxyUpstreamModel) {
		values.ProxyUpstreamModel = values.MemoryLLMModel
	}
	return values
}

// LoadTencentRuntimeConfig reads the existing Baron-managed deployment
// environment without changing it. A deployment that has not been checked
// out yet is a valid empty source for the precedence resolver.
func LoadTencentRuntimeConfig(deployRoot string) (TencentRuntimeConfig, error) {
	deployRoot = strings.TrimSpace(deployRoot)
	if deployRoot == "" {
		return TencentRuntimeConfig{}, nil
	}
	envPath := filepath.Join(deployRoot, "deploy", "global-images", ".env")
	info, err := os.Lstat(envPath)
	if errors.Is(err, os.ErrNotExist) {
		return TencentRuntimeConfig{}, nil
	}
	if err != nil {
		return TencentRuntimeConfig{}, fmt.Errorf("inspect Tencent managed environment: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return TencentRuntimeConfig{}, errors.New("Tencent managed environment is not a safe regular file")
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		return TencentRuntimeConfig{}, fmt.Errorf("read Tencent managed environment: %w", err)
	}
	values := parseSimpleEnv(strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"))
	return normalizeTencentRuntimeConfig(TencentRuntimeConfig{
		MemoryLLMBaseURL:    values["MEMORY_LLM_BASE_URL"],
		MemoryLLMAPIKey:     values["MEMORY_LLM_API_KEY"],
		MemoryLLMModel:      values["MEMORY_LLM_MODEL"],
		MemoryLLMProtocol:   values["MEMORY_LLM_PROTOCOL"],
		ProxyUpstreamURL:    values["PROXY_UPSTREAM_URL"],
		ProxyUpstreamAPIKey: values["PROXY_UPSTREAM_API_KEY"],
		ProxyUpstreamModel:  values["PROXY_UPSTREAM_MODEL"],
		KnowledgePublicURL:  values["KNOWLEDGE_PUBLIC_BASE_URL"],
	}), nil
}

// EnsureTencentRuntimeEnv preserves the upstream template and unrelated user
// settings, fills only missing Baron-managed values, and fails before startup
// if the LLM binding required by Wiki/Knowledge and proxy is incomplete.
func EnsureTencentRuntimeEnv(deployDir string, values TencentRuntimeConfig) error {
	if err := requireDirectory(deployDir); err != nil {
		return err
	}
	envPath := deployDir + string(os.PathSeparator) + ".env"
	data, err := os.ReadFile(envPath)
	if err != nil {
		return fmt.Errorf("read Tencent deployment environment: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	current := parseSimpleEnv(lines)
	if values.MemoryLLMProtocol == "" {
		values.MemoryLLMProtocol = "openai"
	}
	if values.ProxyUpstreamURL == "" {
		values.ProxyUpstreamURL = values.MemoryLLMBaseURL
	}
	if values.ProxyUpstreamAPIKey == "" {
		values.ProxyUpstreamAPIKey = values.MemoryLLMAPIKey
	}
	if values.ProxyUpstreamModel == "" {
		values.ProxyUpstreamModel = values.MemoryLLMModel
	}
	if values.KnowledgePublicURL == "" {
		values.KnowledgePublicURL = "http://host.docker.internal:8424/v3"
	}
	managed := map[string]string{
		"MEMORY_LLM_BASE_URL":       values.MemoryLLMBaseURL,
		"MEMORY_LLM_API_KEY":        values.MemoryLLMAPIKey,
		"MEMORY_LLM_MODEL":          values.MemoryLLMModel,
		"MEMORY_LLM_PROTOCOL":       values.MemoryLLMProtocol,
		"PROXY_UPSTREAM_URL":        values.ProxyUpstreamURL,
		"PROXY_UPSTREAM_API_KEY":    values.ProxyUpstreamAPIKey,
		"PROXY_UPSTREAM_MODEL":      values.ProxyUpstreamModel,
		"KNOWLEDGE_PUBLIC_BASE_URL": values.KnowledgePublicURL,
	}

	changed := false
	for key, value := range managed {
		if !isMissingEnvValue(current[key]) || strings.TrimSpace(value) == "" {
			continue
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("Tencent environment value for %s contains a newline", key)
		}
		lines = setSimpleEnv(lines, key, value)
		current[key] = value
		changed = true
	}
	if changed {
		if _, err := os.Stat(envPath); err == nil {
			if err := backupBeforeEdit(envPath); err != nil {
				return err
			}
		}
		if err := config.AtomicWriteFile(envPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			return err
		}
	} else if err := os.Chmod(envPath, 0o600); err != nil {
		return err
	}

	missing := make([]string, 0, len(managed))
	for key := range managed {
		if isMissingEnvValue(current[key]) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return errors.New("Tencent LLM/runtime configuration is incomplete; set " + strings.Join(missing, ", ") + " or export the BARON_TENCENT_* variables, then rerun baron tencent-memory init")
	}
	return nil
}

func isMissingEnvValue(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || strings.EqualFold(value, "REPLACE_ME")
}

func parseSimpleEnv(lines []string) map[string]string {
	values := make(map[string]string)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if index := strings.Index(value, " #"); index >= 0 {
			value = strings.TrimSpace(value[:index])
		}
		value = strings.Trim(value, `"'`)
		values[key] = value
	}
	return values
}

func setSimpleEnv(lines []string, key, value string) []string {
	encoded := key + "=" + quoteEnvValue(value)
	for index, raw := range lines {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "export "))
		candidate, _, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(candidate) == key {
			lines[index] = encoded
			return lines
		}
	}
	return append(lines, encoded)
}

func quoteEnvValue(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
