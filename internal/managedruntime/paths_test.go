package managedruntime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolvePathsRejectsFilesystemRootAndHome(t *testing.T) {
	volumeRoot := filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	if _, err := ResolvePaths(volumeRoot); err == nil {
		t.Fatal("filesystem root was accepted as a managed runtime root")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolvePaths(home); err == nil {
		t.Fatal("user home was accepted as a managed runtime root")
	}
}

func TestResolvePathsAndValidateOwnedRejectEscapes(t *testing.T) {
	base := filepath.Join(t.TempDir(), "baron")
	paths, err := ResolvePaths(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.ValidateOwned(filepath.Join(paths.Root, "generations", "one", "bin")); err != nil {
		t.Fatalf("managed child was rejected: %v", err)
	}
	if err := paths.ValidateOwned(filepath.Join(paths.Root, "..", "outside")); err == nil {
		t.Fatal("path escape was accepted")
	}
	if err := paths.ValidateOwned(paths.Root); err != nil {
		t.Fatalf("runtime root should be a valid owned boundary: %v", err)
	}
}

func TestValidateOwnedRejectsSymlinkEscape(t *testing.T) {
	base := filepath.Join(t.TempDir(), "baron")
	paths, err := ResolvePaths(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(escape, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(paths.Root, "escape")
	if err := os.Symlink(escape, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symbolic link privilege is unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if err := paths.ValidateOwned(filepath.Join(link, "file")); err == nil {
		t.Fatal("symlink escape was accepted")
	}
}

func TestGenerationRejectsUnsafeID(t *testing.T) {
	paths, err := ResolvePaths(filepath.Join(t.TempDir(), "baron"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".", "..", "one/two", `one\\two`, filepath.VolumeName(os.TempDir()) + string(filepath.Separator) + "absolute"} {
		if _, err := paths.Generation(id); err == nil {
			t.Fatalf("unsafe generation ID %q was accepted", id)
		}
	}
}
