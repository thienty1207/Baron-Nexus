package install

import "strings"

// DefaultTencentRuntimeConfig supplies the normal DeepSeek OpenAI-compatible
// endpoint and the local Knowledge public URL. The API key remains the only
// required interactive provider value for this default path.
func DefaultTencentRuntimeConfig() TencentRuntimeConfig {
	return TencentRuntimeConfig{
		MemoryLLMBaseURL:   "https://api.deepseek.com/v1",
		MemoryLLMModel:     "deepseek-chat",
		MemoryLLMProtocol:  "openai",
		KnowledgePublicURL: "http://host.docker.internal:8424/v3",
	}
}

// ResolveTencentRuntimeConfigWithSources applies the credential bootstrap
// precedence: explicit process environment, existing managed .env values,
// reusable DSH provider key, and safe DeepSeek defaults. Prompting is kept in
// the app layer so this pure resolver never blocks or mutates state.
func ResolveTencentRuntimeConfigWithSources(values map[string]string, managed TencentRuntimeConfig, dshKey string) TencentRuntimeConfig {
	resolved := mergeTencentRuntimeConfig(resolveExplicitTencentRuntimeConfig(values), managed)
	if isMissingEnvValue(resolved.MemoryLLMAPIKey) {
		resolved.MemoryLLMAPIKey = strings.TrimSpace(dshKey)
	}
	if isMissingEnvValue(resolved.ProxyUpstreamAPIKey) {
		resolved.ProxyUpstreamAPIKey = strings.TrimSpace(dshKey)
	}
	resolved = mergeTencentRuntimeConfig(resolved, DefaultTencentRuntimeConfig())
	return normalizeTencentRuntimeConfig(resolved)
}

// MergeTencentRuntimeConfig fills missing fields in preferred with fallback;
// it never replaces a non-empty value. This is deliberately field-wise so a
// custom proxy can coexist with a provider URL inherited from another source.
func MergeTencentRuntimeConfig(preferred, fallback TencentRuntimeConfig) TencentRuntimeConfig {
	return mergeTencentRuntimeConfig(preferred, fallback)
}

func mergeTencentRuntimeConfig(preferred, fallback TencentRuntimeConfig) TencentRuntimeConfig {
	if isMissingEnvValue(preferred.MemoryLLMBaseURL) {
		preferred.MemoryLLMBaseURL = fallback.MemoryLLMBaseURL
	}
	if isMissingEnvValue(preferred.MemoryLLMAPIKey) {
		preferred.MemoryLLMAPIKey = fallback.MemoryLLMAPIKey
	}
	if isMissingEnvValue(preferred.MemoryLLMModel) {
		preferred.MemoryLLMModel = fallback.MemoryLLMModel
	}
	if isMissingEnvValue(preferred.MemoryLLMProtocol) {
		preferred.MemoryLLMProtocol = fallback.MemoryLLMProtocol
	}
	if isMissingEnvValue(preferred.ProxyUpstreamURL) {
		preferred.ProxyUpstreamURL = fallback.ProxyUpstreamURL
	}
	if isMissingEnvValue(preferred.ProxyUpstreamAPIKey) {
		preferred.ProxyUpstreamAPIKey = fallback.ProxyUpstreamAPIKey
	}
	if isMissingEnvValue(preferred.ProxyUpstreamModel) {
		preferred.ProxyUpstreamModel = fallback.ProxyUpstreamModel
	}
	if isMissingEnvValue(preferred.KnowledgePublicURL) {
		preferred.KnowledgePublicURL = fallback.KnowledgePublicURL
	}
	return preferred
}
