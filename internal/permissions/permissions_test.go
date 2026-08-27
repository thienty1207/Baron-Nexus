package permissions

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnableWritesNamedAutoAcceptLaunchers(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "baron", "bin")
	status, err := Enable(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !status.DSHEnabled || !status.CodexEnabled {
		t.Fatalf("launchers are not enabled: %#v", status)
	}
	paths := Paths(directory)
	dsh, err := os.ReadFile(paths.DSH)
	if err != nil {
		t.Fatal(err)
	}
	codex, err := os.ReadFile(paths.Codex)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{string(dsh), string(codex)} {
		if !strings.Contains(content, LauncherMarker) {
			t.Fatalf("launcher marker missing: %s", content)
		}
	}
	if !strings.Contains(string(dsh), "DSH_PERMISSION_MODE=danger-full-access") {
		t.Fatalf("DSH launcher does not set explicit permission mode: %s", dsh)
	}
	if !strings.Contains(string(codex), "danger-full-access") || !strings.Contains(string(codex), "ask-for-approval") {
		t.Fatalf("Codex launcher does not set explicit approval flags: %s", codex)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(paths.DSH); err != nil {
			t.Fatal(err)
		} else if info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("DSH launcher is not executable: %o", info.Mode().Perm())
		}
	}
}

func TestEnableRefusesToReplaceUnmarkedLauncher(t *testing.T) {
	directory := t.TempDir()
	paths := Paths(directory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.DSH, []byte("user binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Enable(directory); err == nil {
		t.Fatal("unmarked launcher was replaced")
	}
	data, err := os.ReadFile(paths.DSH)
	if err != nil || string(data) != "user binary" {
		t.Fatalf("existing launcher changed: %q, %v", data, err)
	}
}

func TestDisableRemovesOnlyBaronLaunchers(t *testing.T) {
	directory := t.TempDir()
	if _, err := Enable(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "unrelated"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := Disable(directory)
	if err != nil {
		t.Fatal(err)
	}
	if status.DSHEnabled || status.CodexEnabled {
		t.Fatalf("launchers still enabled: %#v", status)
	}
	if _, err := os.Stat(filepath.Join(directory, "unrelated")); err != nil {
		t.Fatalf("unrelated file was removed: %v", err)
	}
}
