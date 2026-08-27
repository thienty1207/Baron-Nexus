package uninstall

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/install"
)

type uninstallRunner struct {
	calls []string
}

func (r *uninstallRunner) LookPath(string) (string, error) { return "/fake/npm", nil }

func (r *uninstallRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return "", nil
}

func TestExecuteRemovesBaronResourcesAndPreservesSharedHomes(t *testing.T) {
	root := t.TempDir()
	globalDir := filepath.Join(root, "config")
	globalPath := filepath.Join(globalDir, "global.json")
	if err := os.MkdirAll(filepath.Join(globalDir, "receipts"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		globalPath,
		filepath.Join(globalDir, "dsh.json"),
		filepath.Join(globalDir, "dsh-adapter", "index.js"),
		filepath.Join(globalDir, "codex-adapter", "index.js"),
		filepath.Join(globalDir, "receipts", "dsh.json"),
		filepath.Join(globalDir, "bin", "dsh-auto"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("baron"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		globalPath + ".baron-backup-1",
	} {
		if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	customGlobal := filepath.Join(globalDir, "user-settings.json")
	if err := os.WriteFile(customGlobal, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	projectRoot := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".baron"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".baron", "project.toml"), []byte("baron"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".gitignore"), []byte("custom.tmp\n.baron/.env\n.baron/runtime/\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dshHome := filepath.Join(root, "dsh")
	if err := os.MkdirAll(dshHome, 0o700); err != nil {
		t.Fatal(err)
	}
	dshCredential := filepath.Join(dshHome, ".credentials.yaml")
	if err := os.WriteFile(dshCredential, []byte("version: 1\nrefs:\n  DEEPSEEK_API_KEY: secret\n  OTHER: keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join(dshHome, "profiles", "web", "cordis.patch.yml")
	if err := os.MkdirAll(filepath.Dir(patch), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(patch, []byte("user: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := install.EnsureDSHProfilePatch(patch); err != nil {
		t.Fatal(err)
	}
	patchData, err := os.ReadFile(patch)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(patch, append(patchData, []byte("\nuser-after: true\n")...), 0o600); err != nil {
		t.Fatal(err)
	}

	codexHome := filepath.Join(root, "codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(codexHome, "hooks.json")
	if err := os.WriteFile(hooks, []byte(`{"hooks":{"SessionStart":[{"command":"custom hook"},{"hooks":[{"type":"command","command":"baron hook codex SessionStart"}]}]}}`), 0o600); err != nil {
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
	report, err := Execute(context.Background(), Options{
		GlobalPath:           globalPath,
		DSHHome:              dshHome,
		CodexHome:            codexHome,
		DSHCredentialPath:    dshCredential,
		DSHProfilePatchPaths: []string{patch},
		CodexHooksPath:       hooks,
		ProjectRoots:         []string{projectRoot},
		ExecutablePath:       executable,
		Runner:               runner,
		RemoveExecutable:     os.Remove,
		PurgeShared:          false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Removed) == 0 || len(runner.calls) == 0 {
		t.Fatalf("cleanup report/calls are empty: %#v %#v", report, runner.calls)
	}
	for _, path := range []string{globalPath, filepath.Join(globalDir, "dsh.json"), filepath.Join(globalDir, "dsh-adapter"), filepath.Join(globalDir, "codex-adapter"), filepath.Join(projectRoot, ".baron"), executable} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("Baron resource remains %s: %v", path, err)
		}
	}
	gitignore, err := os.ReadFile(filepath.Join(projectRoot, ".gitignore"))
	if err != nil || string(gitignore) != "custom.tmp\n" {
		t.Fatalf("Baron .gitignore rules were not removed selectively: %q (%v)", gitignore, err)
	}
	if _, err := os.Stat(customGlobal); err != nil {
		t.Fatalf("unrelated global file removed: %v", err)
	}
	if matches, err := filepath.Glob(globalPath + ".baron-backup-*"); err != nil || len(matches) != 0 {
		t.Fatalf("Baron backup residue remains: %v %v", matches, err)
	}
	if _, err := os.Stat(dshHome); err != nil {
		t.Fatalf("shared DSH home removed by default: %v", err)
	}
	credential, err := os.ReadFile(dshCredential)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(credential), "DEEPSEEK_API_KEY") || !strings.Contains(string(credential), "OTHER") {
		t.Fatalf("credential cleanup was not selective: %s", credential)
	}
	patchData, err = os.ReadFile(patch)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(patchData), "baron-owned") || !strings.Contains(string(patchData), "user: true") || !strings.Contains(string(patchData), "user-after: true") {
		t.Fatalf("DSH patch cleanup was not selective: %s", patchData)
	}
	hookData, err := os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(hookData), "baron hook codex") || !strings.Contains(string(hookData), "custom hook") {
		t.Fatalf("Codex hook cleanup was not selective: %s", hookData)
	}
	if !strings.Contains(strings.Join(runner.calls, "\n"), "npm uninstall --global @deepseek-ai/dsh @openai/codex") {
		t.Fatalf("npm cleanup was not requested: %#v", runner.calls)
	}
}

func TestExecutePurgeSharedIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dshHome := filepath.Join(root, "dsh")
	codexHome := filepath.Join(root, "codex")
	for _, path := range []string{dshHome, codexHome} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "auth"), []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &uninstallRunner{}
	options := Options{
		GlobalPath:  filepath.Join(root, "config", "global.json"),
		DSHHome:     dshHome,
		CodexHome:   codexHome,
		Runner:      runner,
		PurgeShared: true,
	}
	if _, err := Execute(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dshHome, codexHome} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("purged shared home remains %s: %v", path, err)
		}
	}
}

func TestBuildPlanRejectsFilesystemRootProject(t *testing.T) {
	root := string(filepath.VolumeName(os.TempDir())) + string(os.PathSeparator)
	if _, err := BuildPlan(Options{GlobalPath: filepath.Join(t.TempDir(), "global.json"), ProjectRoots: []string{root}}); err == nil {
		t.Fatal("filesystem root was accepted for recursive cleanup")
	}
}

func TestBuildPlanRejectsSharedHomeOverlappingBaronConfig(t *testing.T) {
	globalDir := filepath.Join(t.TempDir(), "baron")
	globalPath := filepath.Join(globalDir, "global.json")
	if _, err := BuildPlan(Options{
		GlobalPath:  globalPath,
		DSHHome:     globalDir,
		PurgeShared: true,
	}); err == nil {
		t.Fatal("shared home overlapping Baron config was accepted")
	}
}

var _ install.CommandRunner = (*uninstallRunner)(nil)
