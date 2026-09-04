package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/testsupport"
)

func TestSetupIsIdempotentAndProtectsProjectSecret(t *testing.T) {
	if !testsupport.UnixModeBitsReliable() {
		t.Skip("Windows ACLs do not expose Unix permission bits")
	}
	root := filepath.Join(t.TempDir(), "Project A - Tiếng Việt")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	identity := contracts.Identity{
		UserID:      "usr-baron",
		UserKey:     "secret-user-key",
		TeamID:      "team-baron-projects",
		Endpoint:    "http://127.0.0.1:8420",
		HubEndpoint: "http://127.0.0.1:8125",
		ServiceID:   "default",
	}
	binding := contracts.ProjectBinding{TeamID: identity.TeamID, AgentID: "agt-project-a", UserID: identity.UserID}
	first, err := Setup(context.Background(), root, SetupOptions{Identity: identity, Binding: binding})
	if err != nil {
		t.Fatal(err)
	}
	tomlBefore, err := os.ReadFile(filepath.Join(root, ".baron", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Setup(context.Background(), root, SetupOptions{Identity: identity, Binding: binding})
	if err != nil {
		t.Fatal(err)
	}
	tomlAfter, err := os.ReadFile(filepath.Join(root, ".baron", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if first.ProjectID != second.ProjectID {
		t.Fatalf("setup changed project ID: %q -> %q", first.ProjectID, second.ProjectID)
	}
	if string(tomlBefore) != string(tomlAfter) {
		t.Fatalf("idempotent setup changed project.toml:\n%s\n---\n%s", tomlBefore, tomlAfter)
	}
	if second.Binding.AgentID != binding.AgentID {
		t.Fatalf("binding lost on rerun: %#v", second.Binding)
	}
	envData, err := os.ReadFile(filepath.Join(root, ".baron", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(envData), "BARON_TENCENT_USER_KEY=secret-user-key") {
		t.Fatalf("project env did not contain expected protected value: %s", envData)
	}
	if strings.Contains(string(tomlAfter), identity.UserKey) {
		t.Fatal("secret leaked into project.toml")
	}
	info, err := os.Stat(filepath.Join(root, ".baron", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf(".env mode is too broad: %o", info.Mode().Perm())
	}
}

func TestProjectIDSurvivesMoveAndGitignoreMergeIsIdempotent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project-A")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("# user rules\nnode_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := Setup(context.Background(), root, SetupOptions{Binding: contracts.ProjectBinding{TeamID: "team-a", AgentID: "agt-a"}})
	if err != nil {
		t.Fatal(err)
	}
	ignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{".baron/.env", ".baron/checkpoint.json", ".baron/runtime/"} {
		if strings.Count(string(ignore), rule) != 1 {
			t.Fatalf("gitignore rule %q count is wrong: %s", rule, ignore)
		}
	}
	moved := filepath.Join(t.TempDir(), "Project-A-moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve(moved)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ProjectID != first.ProjectID {
		t.Fatalf("moved project ID changed: got %q want %q", resolved.ProjectID, first.ProjectID)
	}
	if _, err := Setup(context.Background(), moved, SetupOptions{Binding: contracts.ProjectBinding{TeamID: "team-a", AgentID: "agt-a"}}); err != nil {
		t.Fatal(err)
	}
	ignoreAfter, err := os.ReadFile(filepath.Join(moved, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ignore) != string(ignoreAfter) {
		t.Fatalf("idempotent setup changed .gitignore:\n%s\n---\n%s", ignore, ignoreAfter)
	}
}

func TestSetupRejectsFilesystemRoot(t *testing.T) {
	if _, err := Setup(context.Background(), string(filepath.Separator), SetupOptions{}); err == nil {
		t.Fatal("expected filesystem root to be rejected")
	}
}
