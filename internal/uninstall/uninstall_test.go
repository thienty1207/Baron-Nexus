package uninstall

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/install"
	"github.com/baron-shared-brain/baron/internal/permissions"
)

type uninstallRunner struct {
	calls []string
}

func (r *uninstallRunner) LookPath(name string) (string, error) { return "/fake/" + name, nil }

func (r *uninstallRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	return "", nil
}

func TestExecuteRemovesBaronIntegrationAndPreservesSharedHomes(t *testing.T) {
	root := t.TempDir()
	globalDir := filepath.Join(root, "config")
	globalPath := filepath.Join(globalDir, "global.json")
	dshHome := filepath.Join(root, "dsh")
	codexHome := filepath.Join(root, "codex")
	projectRoot := filepath.Join(root, "project")
	for _, path := range []string{
		globalPath,
		filepath.Join(globalDir, "dsh.json"),
		filepath.Join(globalDir, "dsh-adapter", "index.js"),
		filepath.Join(globalDir, "codex-adapter", "index.js"),
		filepath.Join(dshHome, ".credentials.yaml"),
		filepath.Join(dshHome, "profiles", "web", "cordis.patch.yml"),
		filepath.Join(codexHome, "hooks.json"),
		filepath.Join(projectRoot, ".baron", "project.toml"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		contents := []byte("baron")
		switch filepath.Base(path) {
		case ".credentials.yaml":
			contents = []byte("version: 1\nrefs:\n  DEEPSEEK_API_KEY: secret\n  OTHER: keep\n")
		case "cordis.patch.yml":
			contents = []byte("# >>> BARON MANAGED BEGIN >>>\nbaron: true\n# <<< BARON MANAGED END <<<\nuser: true\n")
		case "hooks.json":
			contents = []byte(`{"hooks":{"SessionStart":[{"command":"baron hook codex SessionStart"},{"command":"user hook"}]}}`)
		}
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".gitignore"), []byte("keep.tmp\n.baron/runtime/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "bin", "baron")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("baron"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &uninstallRunner{}
	_, err := Execute(context.Background(), Options{
		GlobalPath:           globalPath,
		DSHHome:              dshHome,
		DSHCredentialPath:    filepath.Join(dshHome, ".credentials.yaml"),
		DSHProfilePatchPaths: []string{filepath.Join(dshHome, "profiles", "web", "cordis.patch.yml")},
		CodexHome:            codexHome,
		CodexHooksPath:       filepath.Join(codexHome, "hooks.json"),
		ProjectRoots:         []string{projectRoot},
		ExecutablePath:       executable,
		Runner:               runner,
		PurgeAll:             true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{globalPath, filepath.Join(globalDir, "dsh.json"), filepath.Join(globalDir, "dsh-adapter"), filepath.Join(globalDir, "codex-adapter"), filepath.Join(projectRoot, ".baron"), executable} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Fatalf("Baron resource remains %s: %v", path, statErr)
		}
	}
	for _, path := range []string{dshHome, codexHome} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("shared agent home was removed: %s: %v", path, statErr)
		}
	}
	credential, err := os.ReadFile(filepath.Join(dshHome, ".credentials.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(credential), "DEEPSEEK_API_KEY") || !strings.Contains(string(credential), "OTHER") {
		t.Fatalf("DSH credential was not selectively cleaned: %s", credential)
	}
	hookData, err := os.ReadFile(filepath.Join(codexHome, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(hookData), "baron hook codex") || !strings.Contains(string(hookData), "user hook") {
		t.Fatalf("Codex hooks were not selectively cleaned: %s", hookData)
	}
	joined := strings.Join(runner.calls, "\n")
	if strings.Contains(joined, "npm uninstall") || strings.Contains(joined, "docker system prune") || strings.Contains(joined, "apt-get purge") {
		t.Fatalf("uninstall touched unverified system runtimes: %#v", runner.calls)
	}
}

func TestExecutePurgesManagedRuntimeAndPreservesSystemObjects(t *testing.T) {
	root := t.TempDir()
	runtimeRoot := filepath.Join(root, "baron-runtime")
	generation := filepath.Join(runtimeRoot, "generations", "generation-current")
	if err := os.MkdirAll(filepath.Join(generation, "strix"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generation, "strix", "strix"), []byte("managed"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "docker-data")
	if err := os.MkdirAll(external, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "keep"), []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &uninstallRunner{}
	_, err := Execute(context.Background(), Options{
		GlobalPath: filepath.Join(root, "config", "global.json"),
		ManagedRuntime: &config.ManagedRuntimeState{
			Root: runtimeRoot, CurrentGeneration: "generation-current",
		},
		Runner:   runner,
		PurgeAll: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(runtimeRoot); !os.IsNotExist(err) {
		t.Fatalf("managed runtime remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(external, "keep")); err != nil {
		t.Fatalf("unrelated system data was removed: %v", err)
	}
	joined := strings.Join(runner.calls, "\n")
	for _, forbidden := range []string{"docker system prune", "docker volume rm", "docker image rm", "apt-get purge", "winget uninstall"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("managed uninstall ran forbidden global cleanup %q: %#v", forbidden, runner.calls)
		}
	}
}

func TestBuildPlanRejectsUnsafeManagedRuntimeRoot(t *testing.T) {
	root := t.TempDir()
	if _, err := BuildPlan(Options{
		GlobalPath: filepath.Join(root, "config", "global.json"),
		PurgeAll:   true,
		ManagedRuntime: &config.ManagedRuntimeState{
			Root: filepath.VolumeName(root) + string(os.PathSeparator),
		},
	}); err == nil {
		t.Fatal("filesystem root was accepted as managed runtime root")
	}
}

func TestBuildPlanPreservesUnverifiedExternalInputs(t *testing.T) {
	root := t.TempDir()
	plan, err := BuildPlan(Options{GlobalPath: filepath.Join(root, "config", "global.json"), PurgeAll: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "preserve unverified external runtimes and Docker objects") {
		t.Fatalf("plan did not state preservation boundary: %s", plan.String())
	}
}

func TestExecuteRemovesExternalBaronPermissionLaunchers(t *testing.T) {
	root := t.TempDir()
	permissionDirectory := filepath.Join(root, "path-bin")
	if _, err := permissions.Enable(permissionDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(context.Background(), Options{
		GlobalPath:           filepath.Join(root, "config", "global.json"),
		PermissionsDirectory: permissionDirectory,
		Runner:               &uninstallRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	paths := permissions.Paths(permissionDirectory)
	for _, path := range []string{paths.DSH, paths.Codex} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("permission launcher remains after uninstall: %s (%v)", path, err)
		}
	}
}

func TestTencentCleanupRequiresManifestOwnershipAndNoVolumeDeletion(t *testing.T) {
	root := t.TempDir()
	deployDir := filepath.Join(root, "deploy", "global-images")
	if err := os.MkdirAll(deployDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "docker-compose.yml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"schema_version":          1,
		"repository":              "https://example.invalid/tencent.git",
		"requested_ref":           strings.Repeat("a", 40),
		"resolved_commit":         strings.Repeat("b", 40),
		"container_image_digests": map[string][]string{"tdai-memory-core": {"repo@sha256:" + strings.Repeat("c", 64)}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "deployment-manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &uninstallRunner{}
	report := Report{}
	removeTencent(context.Background(), Options{TencentInstallPath: root, Runner: runner}, &report)
	joined := strings.Join(runner.calls, "\n")
	if strings.Contains(joined, "--volumes") || strings.Contains(joined, "docker rm -f tdai-memory-core") {
		t.Fatalf("Tencent cleanup removed unproven resources: %#v", runner.calls)
	}
	if len(report.Preserved) == 0 {
		t.Fatalf("unresolved Tencent ownership was not preserved: %#v", report)
	}
}

var _ install.CommandRunner = (*uninstallRunner)(nil)
