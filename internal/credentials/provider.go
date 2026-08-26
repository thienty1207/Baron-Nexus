package credentials

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// ErrInvalidProviderCredential means the provider explicitly rejected the
// supplied key, or the key failed local shape validation.
var ErrInvalidProviderCredential = errors.New("provider credential was rejected")

// ErrProviderUnavailable means validation could not establish whether the key
// is valid because the provider or network was unavailable.
var ErrProviderUnavailable = errors.New("provider credential validation is unavailable")

const providerValidationTimeout = 8 * time.Second

// ValidateOpenAICompatible performs a bounded, side-effect-free provider
// check against the OpenAI-compatible models endpoint. It deliberately does
// not read or include the response body: upstream error bodies can echo
// secrets, and the caller only needs the classification.
func ValidateOpenAICompatible(ctx context.Context, client *http.Client, baseURL, key string) error {
	key = strings.TrimSpace(key)
	if !validProviderKeyShape(key) {
		return ErrInvalidProviderCredential
	}
	endpoint, err := modelsEndpoint(baseURL)
	if err != nil {
		return ErrProviderUnavailable
	}
	if client == nil {
		client = &http.Client{}
	}
	validationCtx, cancel := context.WithTimeout(ctx, providerValidationTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(validationCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ErrProviderUnavailable
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := client.Do(request)
	if err != nil {
		return ErrProviderUnavailable
	}
	defer response.Body.Close()

	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return nil
	case response.StatusCode == http.StatusBadRequest,
		response.StatusCode == http.StatusUnauthorized,
		response.StatusCode == http.StatusForbidden:
		return ErrInvalidProviderCredential
	default:
		return ErrProviderUnavailable
	}
}

func validProviderKeyShape(key string) bool {
	if len(key) < 8 || strings.TrimSpace(key) != key {
		return false
	}
	return strings.IndexFunc(key, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) < 0
}

func modelsEndpoint(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("provider base URL is invalid")
	}
	if parsed.User != nil {
		return "", errors.New("provider base URL must not contain user information")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/models"
	return parsed.String(), nil
}
