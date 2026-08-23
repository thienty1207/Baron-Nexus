package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicWriteKeepsOriginalWhenRenameIsInterrupted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "settings.toml")
	if err := AtomicWriteFile(path, []byte("old = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("simulated power loss before rename")
	err := AtomicWriteFileWithOptions(path, []byte("old = false\n"), 0o600, func(string) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected injected error, got %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "old = true\n" {
		t.Fatalf("original file changed after interrupted write: %q", data)
	}
	if matches, _ := filepath.Glob(path + ".tmp-*"); len(matches) != 0 {
		t.Fatalf("temporary files left behind: %v", matches)
	}
}

func TestRedactRemovesExactAndCommonCredentialPatterns(t *testing.T) {
	input := "key=sk-live-example bearer=Bearer abc.def.ghi admin=.admin-key=local-admin-secret"
	redacted := Redact(input, []string{"local-admin-secret"})
	for _, secret := range []string{"sk-live-example", "Bearer abc.def.ghi", "local-admin-secret"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("secret %q survived redaction: %q", secret, redacted)
		}
	}
	if !strings.Contains(redacted, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %q", redacted)
	}
}

func TestEnvRoundTripAndRestrictiveMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".baron", ".env")
	want := map[string]string{
		"BARON_TENCENT_ENDPOINT": "http://127.0.0.1:8420",
		"BARON_TENCENT_USER_KEY": "secret-value",
	}
	if err := WriteEnv(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["BARON_TENCENT_USER_KEY"] != want["BARON_TENCENT_USER_KEY"] {
		t.Fatalf("env round trip lost secret value: %#v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf(".env is too permissive: %o", info.Mode().Perm())
	}
}
