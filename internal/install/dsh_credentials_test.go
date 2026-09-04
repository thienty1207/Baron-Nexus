package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/baron-shared-brain/baron/internal/testsupport"
	"gopkg.in/yaml.v3"
)

func TestReadDSHProviderKeyPrefersLaunchingEnvironment(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".credentials.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nrefs:\n  DEEPSEEK_API_KEY: file-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := ReadDSHProviderKey(map[string]string{
		"DSH_HOME":         home,
		"DEEPSEEK_API_KEY": " env-key ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if key != "env-key" {
		t.Fatalf("key=%q, want env-key", key)
	}
}

func TestEnsureDSHProviderKeyPreservesOtherRefsAndUsesPrivateAtomicFile(t *testing.T) {
	if !testsupport.UnixModeBitsReliable() {
		t.Skip("Windows ACLs do not expose Unix permission bits")
	}
	home := t.TempDir()
	path := filepath.Join(home, ".credentials.yaml")
	original := "# user-owned comment\nversion: 1\nrefs:\n  OPENAI_API_KEY: keep-me\n  DEEPSEEK_API_KEY: old-key\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := EnsureDSHProviderKey(map[string]string{"DSH_HOME": home}, "new-key"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("credentials YAML is invalid: %v", err)
	}
	refs, ok := document["refs"].(map[string]any)
	if !ok {
		t.Fatalf("refs=%#v", document["refs"])
	}
	if refs["DEEPSEEK_API_KEY"] != "new-key" || refs["OPENAI_API_KEY"] != "keep-me" {
		t.Fatalf("refs=%#v", refs)
	}
	if string(data) == original {
		t.Fatal("credentials file was not updated")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode=%#o, want 0600", got)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary credential artifact remains: %v", err)
	}
}

func TestEnsureDSHProviderKeyCreatesOfficialVersionOneLayout(t *testing.T) {
	home := t.TempDir()
	if err := EnsureDSHProviderKey(map[string]string{"DSH_HOME": home}, "created-key"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".credentials.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document["version"] != 1 {
		t.Fatalf("version=%#v", document["version"])
	}
	refs, ok := document["refs"].(map[string]any)
	if !ok || refs["DEEPSEEK_API_KEY"] != "created-key" {
		t.Fatalf("refs=%#v", document["refs"])
	}
}

func TestEnsureDSHProviderKeyRejectsSymlink(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(target, []byte("version: 1\nrefs: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".credentials.yaml")
	if err := os.Symlink(target, path); err != nil {
		if testsupport.IsSymlinkPrivilegeError(err) {
			t.Skipf("symbolic link privilege is unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if err := EnsureDSHProviderKey(map[string]string{"DSH_HOME": home}, "refuse-key"); err == nil {
		t.Fatal("expected symlink refusal")
	}
}
