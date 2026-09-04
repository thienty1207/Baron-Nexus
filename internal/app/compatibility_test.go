package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/storage"
)

func TestCompareCompatibilityManifestRejectsCanonicalStateLoss(t *testing.T) {
	before := CompatibilityManifest{
		GitHead:                "head-before",
		GitStatusHash:          "status-before",
		SQLiteSchema:           "schema-9",
		SQLiteRowIDs:           []string{"events/evt-1", "tasks/prj-1/task-1"},
		SQLiteCounts:           map[string]int{"events": 2, "tasks": 1},
		TencentIDs:             []string{"project:prj-1", "agent:agt-1"},
		TencentStates:          map[string]string{"wiki": "pending", "code_graph": "ready"},
		DSHHash:                "dsh-before",
		CodexHash:              "codex-before",
		HookCount:              8,
		CredentialFingerprints: []string{"dsh/.credentials.yaml:keys=1", "strix.env:keys=4"},
		ComponentVersions:      map[string]string{"dsh": "1.0.0", "strix": "0.2.0"},
	}
	after := before
	after.GitHead = "head-after"
	after.GitStatusHash = "status-after"
	after.SQLiteRowIDs = []string{"events/evt-1"}
	after.SQLiteCounts = map[string]int{"events": 1, "tasks": 0}
	after.TencentStates = map[string]string{"wiki": "pending"}
	after.HookCount = 7

	result := CompareCompatibilityManifest(before, after)
	if result.Passed {
		t.Fatal("state-loss compatibility comparison unexpectedly passed")
	}
	for _, want := range []string{"SQLite row IDs", "SQLite counts", "Tencent state", "hook count"} {
		if !containsCompatibilityReason(result.Reasons, want) {
			t.Fatalf("comparison reasons=%v, missing %q", result.Reasons, want)
		}
	}
}

func TestCompareCompatibilityManifestAllowsWorkingTreeChangeWhenStateIsPreserved(t *testing.T) {
	before := CompatibilityManifest{
		GitHead:                "head-before",
		GitStatusHash:          "status-before",
		SQLiteSchema:           "schema-9",
		SQLiteRowIDs:           []string{"events/evt-1"},
		SQLiteCounts:           map[string]int{"events": 1},
		TencentIDs:             []string{"project:prj-1"},
		TencentStates:          map[string]string{"wiki": "pending"},
		DSHHash:                "dsh-before",
		CodexHash:              "codex-before",
		HookCount:              8,
		CredentialFingerprints: []string{"dsh/.credentials.yaml:keys=1"},
		ComponentVersions:      map[string]string{"dsh": "1.0.0"},
	}
	after := before
	after.GitHead = "head-after"
	after.GitStatusHash = "status-after"
	after.ComponentVersions = map[string]string{"dsh": "1.0.1"}

	result := CompareCompatibilityManifest(before, after)
	if !result.Passed {
		t.Fatalf("preserved-state compatibility comparison failed: %v", result.Reasons)
	}
}

func TestCaptureCompatibilityManifestIsBoundedAndDoesNotSerializeCredentials(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".baron", "runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runCompatibilityGit(t, root, "init", "-q")
	runCompatibilityGit(t, root, "config", "user.email", "baron@example.invalid")
	runCompatibilityGit(t, root, "config", "user.name", "Baron Test")
	runCompatibilityGit(t, root, "add", "main.go")
	runCompatibilityGit(t, root, "commit", "-qm", "fixture")

	dbPath := filepath.Join(root, ".baron", "runtime", "state.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.RegisterProject(ctx, storage.ProjectRecord{ProjectID: "prj-compat-1", Root: root, Name: "compat"}); err != nil {
		t.Fatal(err)
	}
	if err := store.StartSession(ctx, storage.Session{SessionID: "ses-compat-1", ProjectID: "prj-compat-1", Client: contracts.ClientCodex, State: contracts.SessionActive, StartedAt: time.Unix(1, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertEvent(ctx, storage.Event{EventID: "evt-compat-1", ProjectID: "prj-compat-1", SessionID: "ses-compat-1", Client: contracts.ClientCodex, Type: contracts.EventUserPrompt, Payload: []byte(`{"text":"continue"}`), IdempotencyKey: "compat-1", OccurredAt: time.Unix(2, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertKnowledgeRegistry(ctx, storage.KnowledgeRegistry{ProjectID: "prj-compat-1", TeamID: "team-1", UserID: "user-1", AgentID: "agent-1", WikiID: "wiki-1", WikiIngestStatus: "pending", CodeGraphSyncStatus: "ready", UpdatedAt: time.Unix(3, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	dshConfig := filepath.Join(root, "dsh-config.yaml")
	dshHome := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(dshHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dshConfig, []byte("api_key: sk-test-secret\nprofile: stable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	codexHooks := filepath.Join(root, "codex-hooks.json")
	if err := os.WriteFile(codexHooks, []byte(`{"hooks":{"SessionStart":[{"command":"baron hook codex SessionStart"}],"UserPromptSubmit":[{"command":"baron hook codex UserPromptSubmit"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(root, "global.json")
	if err := config.SaveGlobalState(globalPath, config.GlobalState{
		DSHConfigPath: dshConfig, DSHHomePath: dshHome, CodexHooksPath: codexHooks,
		CodexHomePath:   filepath.Join(root, "codex-home"),
		ProjectBindings: map[string]contracts.ProjectBinding{"prj-compat-1": {ProjectID: "prj-compat-1", TeamID: "team-1", AgentID: "agent-1", UserID: "user-1"}},
		ProjectRoots:    map[string]string{"prj-compat-1": root},
	}); err != nil {
		t.Fatal(err)
	}

	manifest, err := CaptureCompatibilityManifest(ctx, CompatibilityFixture{Root: root, GlobalConfig: globalPath, SQLitePath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SQLiteSchema == "" || manifest.SQLiteCounts["projects"] != 1 || manifest.SQLiteCounts["events"] != 1 {
		t.Fatalf("captured SQLite evidence is incomplete: %#v", manifest)
	}
	if len(manifest.TencentIDs) == 0 || manifest.HookCount != 2 || manifest.GitHead == "" {
		t.Fatalf("captured compatibility evidence is incomplete: %#v", manifest)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "sk-test-secret") {
		t.Fatalf("compatibility manifest leaked a credential: %s", encoded)
	}
}

func TestLegacyCompatibilityGateFromEnvironment(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("BARON_COMPATIBILITY_ROOT"))
	if root == "" {
		t.Skip("legacy compatibility fixture is not configured")
	}
	result := RunLegacyUpgradeGate(context.Background(), CompatibilityFixture{
		Root:               root,
		GlobalConfig:       os.Getenv("BARON_COMPATIBILITY_GLOBAL"),
		SQLitePath:         os.Getenv("BARON_COMPATIBILITY_SQLITE"),
		BeforeManifestPath: os.Getenv("BARON_COMPATIBILITY_BEFORE"),
		AfterManifestPath:  os.Getenv("BARON_COMPATIBILITY_AFTER"),
	})
	if !result.Passed {
		t.Fatalf("legacy compatibility gate failed: %s", strings.Join(result.Reasons, "; "))
	}
}

func runCompatibilityGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func containsCompatibilityReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, want) {
			return true
		}
	}
	return false
}
