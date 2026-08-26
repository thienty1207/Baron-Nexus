package credentials

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSecretUsesInjectedReaderWithoutEchoingValue(t *testing.T) {
	var output bytes.Buffer
	prompter := &Prompter{
		In:  strings.NewReader("unused"),
		Out: &output,
		ReadSecret: func(io.Reader) ([]byte, error) {
			return []byte("  sk-test-secret  \n"), nil
		},
	}
	got, err := prompter.Secret("DeepSeek API key")
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-test-secret" {
		t.Fatalf("secret=%q", got)
	}
	if strings.Contains(output.String(), "sk-test-secret") {
		t.Fatalf("prompt output leaked secret: %q", output.String())
	}
	if !strings.Contains(output.String(), "DeepSeek API key") {
		t.Fatalf("prompt label missing: %q", output.String())
	}
}

func TestVisibleSecretUsesOrdinaryLineInputAndWarnsAboutEcho(t *testing.T) {
	var output bytes.Buffer
	prompter := &Prompter{
		Out: &output,
		ReadLine: func(io.Reader) (string, error) {
			return "  visible-provider-key  ", nil
		},
	}
	got, err := prompter.VisibleSecret("DeepSeek API key")
	if err != nil {
		t.Fatal(err)
	}
	if got != "visible-provider-key" {
		t.Fatalf("secret=%q", got)
	}
	if !strings.Contains(strings.ToLower(output.String()), "visible") {
		t.Fatalf("visible-input warning missing: %q", output.String())
	}
}

func TestValueUsesInjectedLineReaderAndDefault(t *testing.T) {
	var output bytes.Buffer
	prompter := &Prompter{
		Out:      &output,
		ReadLine: func(io.Reader) (string, error) { return "  https://provider.example/v1  ", nil },
	}
	got, err := prompter.Value("Provider URL", "https://api.deepseek.com/v1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://provider.example/v1" {
		t.Fatalf("value=%q", got)
	}
	if !strings.Contains(output.String(), "Provider URL") || !strings.Contains(output.String(), "api.deepseek.com") {
		t.Fatalf("prompt/default missing: %q", output.String())
	}

	prompter.ReadLine = func(io.Reader) (string, error) { return "", nil }
	got, err = prompter.Value("Model", "deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	if got != "deepseek-chat" {
		t.Fatalf("default value=%q", got)
	}
}

func TestPromptRejectsNonInteractiveInput(t *testing.T) {
	prompter := &Prompter{In: strings.NewReader("sk-secret\n"), Out: io.Discard}
	_, err := prompter.Secret("API key")
	if !errors.Is(err, ErrNonInteractive) {
		t.Fatalf("error=%v, want ErrNonInteractive", err)
	}
}
