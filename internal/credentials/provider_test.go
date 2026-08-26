package credentials

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateOpenAICompatibleAcceptsModelsEndpoint(t *testing.T) {
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		gotAuthorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := ValidateOpenAICompatible(context.Background(), server.Client(), server.URL+"/v1", "provider-key-123"); err != nil {
		t.Fatal(err)
	}
	if gotAuthorization != "Bearer provider-key-123" {
		t.Fatalf("authorization=%q", gotAuthorization)
	}
}

func TestValidateOpenAICompatibleRejectsWeakKeyBeforeNetwork(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	err := ValidateOpenAICompatible(context.Background(), server.Client(), server.URL, "123")
	if !errors.Is(err, ErrInvalidProviderCredential) {
		t.Fatalf("error=%v, want invalid credential", err)
	}
	if called {
		t.Fatal("weak credential reached the network")
	}
}

func TestValidateOpenAICompatibleClassifiesProviderResponsesWithoutLeakingKey(t *testing.T) {
	const secret = "provider-secret-value"
	for _, test := range []struct {
		name    string
		status  int
		wantErr error
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantErr: ErrInvalidProviderCredential},
		{name: "forbidden", status: http.StatusForbidden, wantErr: ErrInvalidProviderCredential},
		{name: "rate limited", status: http.StatusTooManyRequests, wantErr: ErrProviderUnavailable},
		{name: "server", status: http.StatusBadGateway, wantErr: ErrProviderUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(secret))
			}))
			defer server.Close()
			err := ValidateOpenAICompatible(context.Background(), server.Client(), server.URL, secret)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error=%v, want %v", err, test.wantErr)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("validation error leaked provider key: %v", err)
			}
		})
	}
}

func TestValidateOpenAICompatibleClassifiesTransportFailureAsUnavailable(t *testing.T) {
	const secret = "provider-secret-value"
	err := ValidateOpenAICompatible(context.Background(), http.DefaultClient, "http://127.0.0.1:1/v1", secret)
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("error=%v, want unavailable", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("transport error leaked provider key: %v", err)
	}
}
