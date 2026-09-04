package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/testsupport"
)

func TestInstallEmbeddedCodexAdapterMaterializesPrivateBridge(t *testing.T) {
	target := filepath.Join(t.TempDir(), "codex-adapter")
	if err := InstallEmbeddedCodexAdapter(target); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.js", "index.ts", "package.json", "README.md"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Fatalf("Codex adapter file %s missing: %v", name, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(target, "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"baron hook codex", "SessionStart", "SessionEnd", "timeout", "task_id", "verification_kind", "spawnBaron", "ComSpec"} {
		if !strings.Contains(string(data), marker) {
			t.Fatalf("Codex adapter missing protocol marker %q", marker)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(target, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), `"@baron-nexus/codex-adapter"`) || !strings.Contains(string(manifest), `"version": "0.1.0"`) {
		t.Fatalf("Codex adapter manifest is not pinned: %s", manifest)
	}
	if !testsupport.UnixModeBitsReliable() {
		t.Skip("Windows ACLs do not expose Unix permission bits")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("Codex adapter target permissions=%o, want 700", info.Mode().Perm())
	}
}

func TestInstallEmbeddedCodexAdapterReportsOnlyFirstMaterializationAsChanged(t *testing.T) {
	target := filepath.Join(t.TempDir(), "codex-adapter")
	changed, err := InstallEmbeddedCodexAdapterWithChange(target)
	if err != nil || !changed {
		t.Fatalf("first adapter materialization changed=%v err=%v", changed, err)
	}
	changed, err = InstallEmbeddedCodexAdapterWithChange(target)
	if err != nil || changed {
		t.Fatalf("identical adapter materialization changed=%v err=%v", changed, err)
	}
}

func TestInstallEmbeddedCodexAdapterRejectsUnsafeTarget(t *testing.T) {
	link := filepath.Join(t.TempDir(), "codex-adapter")
	target := filepath.Join(t.TempDir(), "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		if testsupport.IsSymlinkPrivilegeError(err) {
			t.Skipf("symbolic link privilege is unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if err := InstallEmbeddedCodexAdapter(link); err == nil {
		t.Fatal("symlinked Codex adapter target was accepted")
	}
}
