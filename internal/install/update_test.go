package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/baron-shared-brain/baron/internal/storage"
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

func TestUpdateBinaryPreservesProjectAndKnowledgeMappings(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "state.db")
	projectID := "prj-update-12345678"
	store, err := storage.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.RegisterProject(ctx, storage.ProjectRecord{ProjectID: projectID, Root: root, Name: "update"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.UpsertKnowledgeRegistry(ctx, storage.KnowledgeRegistry{ProjectID: projectID, TeamID: "team", UserID: "user", AgentID: "agent", WikiID: "wiki-1", CodeGraphID: "graph-1", WikiStatus: "ready", CodeGraphStatus: "ready", CodeGraphCommit: "commit-1"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "baron")
	candidate := filepath.Join(root, "candidate")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	checkMappings := func() error {
		check, openErr := storage.Open(databasePath)
		if openErr != nil {
			return openErr
		}
		defer check.Close()
		mapping, mappingErr := check.GetKnowledgeRegistry(ctx, projectID)
		if mappingErr != nil {
			return mappingErr
		}
		if mapping.ProjectID != projectID || mapping.WikiID != "wiki-1" || mapping.CodeGraphID != "graph-1" || mapping.CodeGraphCommit != "commit-1" {
			return errors.New("project or Tencent knowledge mapping changed during update")
		}
		return nil
	}
	if _, err := UpdateBinary(current, candidate, checkMappings); err != nil {
		t.Fatal(err)
	}
	if err := checkMappings(); err != nil {
		t.Fatal(err)
	}
}
