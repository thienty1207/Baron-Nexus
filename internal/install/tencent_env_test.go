package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveTencentRuntimeConfigUsesProviderEnvironmentWithoutLoggingValues(t *testing.T) {
	config := ResolveTencentRuntimeConfig(map[string]string{
		"DEEPSEEK_BASE_URL": "https://api.deepseek.com/v1",
		"DEEPSEEK_API_KEY":  "sk-secret-value",
		"DEEPSEEK_MODEL":    "deepseek-chat",
	})
	if config.MemoryLLMBaseURL != "https://api.deepseek.com/v1" || config.MemoryLLMAPIKey != "sk-secret-value" || config.MemoryLLMModel != "deepseek-chat" {
		t.Fatalf("provider environment was not resolved: %#v", config)
	}
	if config.ProxyUpstreamURL != config.MemoryLLMBaseURL || config.ProxyUpstreamAPIKey != config.MemoryLLMAPIKey || config.ProxyUpstreamModel != config.MemoryLLMModel {
		t.Fatalf("proxy defaults did not follow memory provider: %#v", config)
	}
}

func TestLoadTencentRuntimeConfigReadsManagedDeploymentEnv(t *testing.T) {
	root := t.TempDir()
	deployDir := filepath.Join(root, "deploy", "global-images")
	if err := os.MkdirAll(deployDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, ".env"), []byte("MEMORY_LLM_BASE_URL='https://managed.example/v1'\nMEMORY_LLM_API_KEY='managed-key'\nMEMORY_LLM_MODEL=managed-model\nMEMORY_LLM_PROTOCOL=openai\nPROXY_UPSTREAM_URL=https://proxy.example/v1\nPROXY_UPSTREAM_API_KEY=proxy-key\nPROXY_UPSTREAM_MODEL=proxy-model\nKNOWLEDGE_PUBLIC_BASE_URL=http://knowledge.example/v3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadTencentRuntimeConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if config.MemoryLLMBaseURL != "https://managed.example/v1" || config.MemoryLLMModel != "managed-model" || config.ProxyUpstreamURL != "https://proxy.example/v1" || config.KnowledgePublicURL != "http://knowledge.example/v3" {
		t.Fatalf("managed runtime fields were not loaded")
	}
	if config.MemoryLLMAPIKey != "managed-key" || config.ProxyUpstreamAPIKey != "proxy-key" {
		t.Fatal("managed runtime credentials were not loaded")
	}
}

func TestLoadTencentRuntimeConfigTreatsAbsentDeploymentAsEmpty(t *testing.T) {
	config, err := LoadTencentRuntimeConfig(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if config != (TencentRuntimeConfig{}) {
		t.Fatalf("config=%#v, want empty config", config)
	}
}

func TestResolveTencentRuntimeConfigWithSourcesUsesExplicitManagedDSHAndDefaultsPrecedence(t *testing.T) {
	managed := TencentRuntimeConfig{
		MemoryLLMBaseURL:    "https://managed.example/v1",
		MemoryLLMAPIKey:     "managed-key",
		MemoryLLMModel:      "managed-model",
		ProxyUpstreamURL:    "https://managed-proxy.example/v1",
		ProxyUpstreamAPIKey: "managed-proxy-key",
		ProxyUpstreamModel:  "managed-proxy-model",
	}
	resolved := ResolveTencentRuntimeConfigWithSources(map[string]string{
		"BARON_TENCENT_MEMORY_LLM_MODEL": "explicit-model",
		"DEEPSEEK_API_KEY":               "explicit-key",
	}, managed, "dsh-key")
	if resolved.MemoryLLMBaseURL != "https://managed.example/v1" || resolved.MemoryLLMModel != "explicit-model" || resolved.MemoryLLMAPIKey != "explicit-key" {
		t.Fatal("explicit and managed memory values did not follow precedence")
	}
	if resolved.ProxyUpstreamURL != "https://managed-proxy.example/v1" || resolved.ProxyUpstreamAPIKey != "managed-proxy-key" || resolved.ProxyUpstreamModel != "managed-proxy-model" {
		t.Fatal("managed proxy values were unexpectedly overwritten")
	}
	if resolved.MemoryLLMProtocol != "openai" || resolved.KnowledgePublicURL != "http://host.docker.internal:8424/v3" {
		t.Fatal("defaults were not applied")
	}
}

func TestResolveTencentRuntimeConfigWithSourcesReusesDSHKeyAndAppliesDeepSeekDefaults(t *testing.T) {
	resolved := ResolveTencentRuntimeConfigWithSources(nil, TencentRuntimeConfig{}, "dsh-key")
	if resolved.MemoryLLMAPIKey != "dsh-key" || resolved.ProxyUpstreamAPIKey != "dsh-key" {
		t.Fatal("DSH provider key was not reused")
	}
	if resolved.MemoryLLMBaseURL != "https://api.deepseek.com/v1" || resolved.MemoryLLMModel != "deepseek-chat" || resolved.ProxyUpstreamURL != resolved.MemoryLLMBaseURL || resolved.ProxyUpstreamModel != resolved.MemoryLLMModel {
		t.Fatal("DeepSeek defaults were not applied")
	}
	if missing := resolved.MissingProviderValues(); len(missing) != 0 {
		t.Fatalf("missing=%v after DSH-key reuse", missing)
	}
}

func TestResolveTencentRuntimeConfigWithSourcesKeepsCustomProviderValues(t *testing.T) {
	resolved := ResolveTencentRuntimeConfigWithSources(map[string]string{
		"BARON_TENCENT_MEMORY_LLM_BASE_URL": "https://custom.example/v1",
		"BARON_TENCENT_MEMORY_LLM_MODEL":    "custom-model",
	}, TencentRuntimeConfig{}, "custom-key")
	if resolved.MemoryLLMBaseURL != "https://custom.example/v1" || resolved.MemoryLLMModel != "custom-model" || resolved.MemoryLLMAPIKey != "custom-key" {
		t.Fatal("custom provider values were not preserved")
	}
}

func TestEnsureTencentRuntimeEnvPreservesConfiguredValuesAndFillsManagedValues(t *testing.T) {
	root := t.TempDir()
	deployDir := filepath.Join(root, "deploy", "global-images")
	if err := os.MkdirAll(deployDir, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(deployDir, ".env")
	if err := os.WriteFile(envPath, []byte("PROXY_UPSTREAM_MODEL='user-model'\nMEMORY_LLM_MODEL=REPLACE_ME # managed placeholder\nCUSTOM_VALUE=keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := TencentRuntimeConfig{
		MemoryLLMBaseURL:    "https://memory.example/v1",
		MemoryLLMAPIKey:     "sk-secret-value",
		MemoryLLMModel:      "memory-model",
		MemoryLLMProtocol:   "openai",
		ProxyUpstreamURL:    "https://proxy.example/v1",
		ProxyUpstreamAPIKey: "sk-proxy-secret",
		ProxyUpstreamModel:  "proxy-model",
		KnowledgePublicURL:  "http://host.docker.internal:8424/v3",
	}
	if err := EnsureTencentRuntimeEnv(deployDir, values); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"MEMORY_LLM_BASE_URL='https://memory.example/v1'", "MEMORY_LLM_API_KEY='sk-secret-value'", "MEMORY_LLM_MODEL='memory-model'", "PROXY_UPSTREAM_MODEL='user-model'", "CUSTOM_VALUE=keep", "KNOWLEDGE_PUBLIC_BASE_URL='http://host.docker.internal:8424/v3'"} {
		if !strings.Contains(text, want) {
			t.Fatalf("managed env missing %q: %s", want, text)
		}
	}
	if info, err := os.Stat(envPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("managed env permissions changed: info=%v err=%v", info, err)
	}
}

func TestEnsureTencentRuntimeEnvReportsMissingNamesWithoutSecretValues(t *testing.T) {
	root := t.TempDir()
	deployDir := filepath.Join(root, "deploy", "global-images")
	if err := os.MkdirAll(deployDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, ".env"), []byte("MEMORY_LLM_BASE_URL=REPLACE_ME\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := TencentRuntimeConfig{MemoryLLMAPIKey: "sk-never-log-this"}
	err := EnsureTencentRuntimeEnv(deployDir, values)
	if err == nil || !strings.Contains(err.Error(), "MEMORY_LLM_BASE_URL") || !strings.Contains(err.Error(), "MEMORY_LLM_MODEL") || strings.Contains(err.Error(), "sk-never-log-this") {
		t.Fatalf("missing runtime configuration was not classified safely: %v", err)
	}
}

func TestReplaceTencentRuntimeAPIKeyPreservesUnrelatedValuesAndCreatesBackup(t *testing.T) {
	root := t.TempDir()
	envPath := filepath.Join(root, ".env")
	old := "MEMORY_LLM_API_KEY='old-key'\nPROXY_UPSTREAM_API_KEY='old-key'\nCUSTOM_VALUE=keep\n"
	if err := os.WriteFile(envPath, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceTencentRuntimeAPIKey(root, "new-key-value"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"MEMORY_LLM_API_KEY='new-key-value'", "PROXY_UPSTREAM_API_KEY='new-key-value'", "CUSTOM_VALUE=keep"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rotated env missing %q: %s", want, text)
		}
	}
	if info, err := os.Stat(envPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("rotated env is not private: info=%v err=%v", info, err)
	}
	entries, err := filepath.Glob(envPath + ".baron-backup-*")
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one recoverable env backup, entries=%v err=%v", entries, err)
	}
}
