package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
)

func TestBackupExcludesSecretsAndRestoreRejectsChecksumCorruption(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project A")
	if err := os.MkdirAll(filepath.Join(root, ".baron"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".baron", "project.toml"), []byte("version = 1\nproject_id = \"prj-a-12345678\"\nname = \"A\"\ncreated_at = \"2026-08-23T00:00:00Z\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".baron", "checkpoint.json"), []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteEnv(filepath.Join(root, ".baron", ".env"), map[string]string{"BARON_TENCENT_USER_KEY": "sk-secret"}); err != nil {
		t.Fatal(err)
	}
	application := New()
	globalPath := filepath.Join(t.TempDir(), "global.json")
	application.GlobalPath = globalPath
	if err := config.SaveGlobalState(globalPath, config.GlobalState{Identity: contracts.Identity{UserKey: "sk-secret", TeamID: "team-a"}}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "baron-backup.tar.gz")
	if err := application.BackupProject(context.Background(), root, archive); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-secret") {
		t.Fatal("portable backup contains plaintext secret")
	}
	corrupt := filepath.Join(t.TempDir(), "corrupt.tar.gz")
	if err := os.WriteFile(corrupt, append(data[:len(data)/2], data[len(data)/2]^0xff), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restore")
	if err := application.RestoreArchive(context.Background(), corrupt, target); err == nil {
		t.Fatal("corrupt backup was accepted")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("corrupt restore mutated target: %v", err)
	}
}
