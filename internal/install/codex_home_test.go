package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexHooksPathUsesCODEXHOME(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	t.Setenv("CODEX_HOME", codexHome)

	path, err := CodexHooksPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(codexHome, "hooks.json")
	if path != want {
		t.Fatalf("Codex hooks path=%q, want %q", path, want)
	}
}

func TestCodexHooksPathDefaultsToDotCodex(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	path, err := CodexHooksPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".codex", "hooks.json")
	if path != want {
		t.Fatalf("Codex hooks path=%q, want %q", path, want)
	}
}
