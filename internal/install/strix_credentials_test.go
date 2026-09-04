package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/config"
)

type failingSecretBridge struct{}

func (failingSecretBridge) Write(context.Context, []byte) error {
	return errors.New("bridge unavailable")
}

func TestRotateDeepSeekFansOutToDSHTencentAndStrixWithoutTouchingCodex(t *testing.T) {
	root := t.TempDir()
	dshPath := filepath.Join(root, "dsh", ".credentials.yaml")
	tencentPath := filepath.Join(root, "tencent", ".env")
	strixPath := filepath.Join(root, "runtime", "credentials", "strix.env")
	codexPath := filepath.Join(root, "codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte("codex-auth-must-remain"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDSHProviderKey(map[string]string{"DSH_HOME": filepath.Dir(dshPath)}, "old-key"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tencentPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tencentPath, []byte("MEMORY_LLM_API_KEY='old-key'\nPROXY_UPSTREAM_API_KEY='old-key'\nCUSTOM=keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(strixPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strixPath, []byte("STRIX_PROVIDER=deepseek\nDEEPSEEK_API_KEY='old-key'\nCUSTOM=keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := RotateDeepSeek(context.Background(), CredentialFanout{
		DSHPath: dshPath, TencentEnvPath: tencentPath, StrixEnvPath: strixPath,
	}, "new-key")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := ReadDSHProviderKey(map[string]string{"DSH_HOME": filepath.Dir(dshPath)})
	if err != nil || stored != "new-key" {
		t.Fatalf("DSH key=%q err=%v", stored, err)
	}
	for path, want := range map[string]string{
		tencentPath: "MEMORY_LLM_API_KEY='new-key'\nPROXY_UPSTREAM_API_KEY='new-key'",
		strixPath:   "DEEPSEEK_API_KEY=new-key",
	} {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.Contains(string(data), want) {
			t.Fatalf("%s was not rotated: %s", path, data)
		}
	}
	strixValues, err := config.ReadEnvFile(strixPath)
	if err != nil {
		t.Fatal(err)
	}
	if strixValues["LLM_API_KEY"] != "new-key" || strixValues["STRIX_LLM"] != "deepseek/deepseek-chat" || strixValues["LLM_API_BASE"] == "" {
		t.Fatalf("Strix provider contract was not populated: %#v", strixValues)
	}
	codexData, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(codexData) != "codex-auth-must-remain" {
		t.Fatal("credential rotation touched Codex auth")
	}
}

func TestRotateDeepSeekRollsBackWhenWSLBridgeFails(t *testing.T) {
	root := t.TempDir()
	dshPath := filepath.Join(root, "dsh", ".credentials.yaml")
	tencentPath := filepath.Join(root, "tencent", ".env")
	strixPath := filepath.Join(root, "runtime", "credentials", "strix.env")
	if err := EnsureDSHProviderKey(map[string]string{"DSH_HOME": filepath.Dir(dshPath)}, "old-key"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tencentPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tencentPath, []byte("MEMORY_LLM_API_KEY='old-key'\nPROXY_UPSTREAM_API_KEY='old-key'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(strixPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strixPath, []byte("STRIX_PROVIDER=deepseek\nDEEPSEEK_API_KEY=old-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := RotateDeepSeek(context.Background(), CredentialFanout{
		DSHPath: dshPath, TencentEnvPath: tencentPath, StrixEnvPath: strixPath, WSLBridge: failingSecretBridge{},
	}, "new-key")
	if err == nil || !strings.Contains(err.Error(), "bridge") {
		t.Fatalf("expected bridge error, got %v", err)
	}
	stored, err := ReadDSHProviderKey(map[string]string{"DSH_HOME": filepath.Dir(dshPath)})
	if err != nil || stored != "old-key" {
		t.Fatalf("DSH rollback key=%q err=%v", stored, err)
	}
	for _, path := range []string{tencentPath, strixPath} {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(data), "new-key") {
			t.Fatalf("credential target was not rolled back: %s", path)
		}
	}
}
