package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateBinaryRollsBackAfterValidationFailure(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "baron")
	candidate := filepath.Join(root, "candidate")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	backup, err := UpdateBinary(current, candidate, func() error { return errors.New("smoke failed") })
	if err == nil || backup == "" {
		t.Fatalf("failed validation was not reported: backup=%q err=%v", backup, err)
	}
	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old" {
		t.Fatalf("rollback did not restore prior binary: %q", data)
	}
}

func TestUpdateBinaryKeepsRollbackArtifactAfterSuccess(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "baron")
	candidate := filepath.Join(root, "candidate")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	backup, err := UpdateBinary(current, candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("candidate was not installed: %q", data)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("rollback artifact missing: %v", err)
	}
}
