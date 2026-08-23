package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSetupRejectsSymlinkedBaronDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".baron")); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(context.Background(), root, SetupOptions{}); err == nil {
		t.Fatal("symlinked .baron directory was accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("setup wrote outside project root: %#v", entries)
	}
}

func TestSetupRejectsSymlinkedOwnedFiles(t *testing.T) {
	for _, name := range []string{"project.toml", ".env"} {
		root := filepath.Join(t.TempDir(), "Project")
		outside := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(filepath.Join(root, ".baron"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, ".baron", name)); err != nil {
			t.Fatal(err)
		}
		if _, err := Setup(context.Background(), root, SetupOptions{}); err == nil {
			t.Fatalf("symlinked %s was accepted", name)
		}
		data, err := os.ReadFile(outside)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "outside" {
			t.Fatalf("setup modified outside %s target", name)
		}
	}
}
