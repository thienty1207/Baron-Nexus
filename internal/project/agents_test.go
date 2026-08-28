package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

func TestEnsureManagedAgentsCreatesContract(t *testing.T) {
	root := t.TempDir()

	if err := ensureManagedAgents(root); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, marker := range []string{
		"# Baron Nexus Agent Contract",
		"<!-- BARON:MANAGED:START version=1 -->",
		"<!-- BARON:MANAGED:END -->",
		"Tencent",
		".baron/project.toml",
	} {
		if !strings.Contains(content, marker) {
			t.Fatalf("generated AGENTS.md is missing %q: %s", marker, content)
		}
	}
}

func TestEnsureManagedAgentsIsIdempotent(t *testing.T) {
	root := t.TempDir()

	if err := ensureManagedAgents(root); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureManagedAgents(root); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("repeated setup changed AGENTS.md:\n%s\n---\n%s", first, second)
	}
}

func TestEnsureManagedAgentsPreservesExistingInstructions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	custom := "# Project rules\n\nRun the project-specific checks before a release.\n"
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ensureManagedAgents(root); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(first)
	if !strings.HasPrefix(content, custom) {
		t.Fatalf("custom AGENTS.md instructions were not preserved: %s", content)
	}
	if strings.Count(content, managedAgentsStart) != 1 || strings.Count(content, managedAgentsEnd) != 1 {
		t.Fatalf("managed AGENTS.md block count is wrong: %s", content)
	}

	if err := ensureManagedAgents(root); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("repeated setup changed a merged AGENTS.md")
	}
}

func TestEnsureManagedAgentsRejectsIncompleteManagedBlockWithoutMutation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	original := "# Project rules\n\n<!-- BARON:MANAGED:START version=1 -->\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ensureManagedAgents(root); err == nil {
		t.Fatal("expected incomplete managed block to be rejected")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != original {
		t.Fatalf("incomplete AGENTS.md was mutated: %s", data)
	}
}

func TestSetupCreatesManagedAgentsContract(t *testing.T) {
	root := t.TempDir()
	if _, err := Setup(context.Background(), root, SetupOptions{
		Binding: contracts.ProjectBinding{TeamID: "team-baron", AgentID: "agent-project"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatalf("project setup did not create AGENTS.md: %v", err)
	}
}
