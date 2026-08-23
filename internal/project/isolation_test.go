package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
)

func TestTamperedProjectBindingFailsBeforeRemoteUse(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project-B")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(context.Background(), root, SetupOptions{Binding: contracts.ProjectBinding{TeamID: "team-b", AgentID: "agent-b", UserID: "user"}}); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(root, ".baron", ".env")
	env, err := config.ReadEnvFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	env["BARON_TENCENT_AGENT_ID"] = "agent-a"
	if err := config.WriteEnv(envPath, env); err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBinding(resolved, contracts.ProjectBinding{ProjectID: resolved.ProjectID, TeamID: "team-b", AgentID: "agent-b", UserID: "user"}); err == nil {
		t.Fatal("tampered binding was accepted")
	}
}

func TestIsolationContextRequiresAgentForDataPlane(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project-A")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := Setup(context.Background(), root, SetupOptions{Binding: contracts.ProjectBinding{TeamID: "team-a", AgentID: "agent-a", UserID: "user"}})
	if err != nil {
		t.Fatal(err)
	}
	context := project.IsolationContext()
	if err := context.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTenProjectsWithIdenticalBasenamesKeepIndependentIdentities(t *testing.T) {
	base := t.TempDir()
	ids := map[string]bool{}
	for index := 0; index < 10; index++ {
		root := filepath.Join(base, "branch", string(rune('A'+index)), "Project-A")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		project, err := Setup(context.Background(), root, SetupOptions{Binding: contracts.ProjectBinding{TeamID: "team", AgentID: "agent-" + string(rune('a'+index)), UserID: "user"}})
		if err != nil {
			t.Fatal(err)
		}
		if ids[project.ProjectID] {
			t.Fatalf("duplicate project identity at index %d: %s", index, project.ProjectID)
		}
		ids[project.ProjectID] = true
		if project.Binding.AgentID != "agent-"+string(rune('a'+index)) {
			t.Fatalf("project %d binding crossed namespace: %#v", index, project.Binding)
		}
	}
}
