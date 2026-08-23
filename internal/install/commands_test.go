package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type commandFixture struct {
	available map[string]bool
	calls     []string
	outputs   map[string]string
}

func (f *commandFixture) LookPath(name string) (string, error) {
	if f.available[name] {
		return "/fake/" + name, nil
	}
	return "", errCommandMissing
}

func (f *commandFixture) Run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if output, ok := f.outputs[call]; ok {
		return output, nil
	}
	return "ok", nil
}

func TestInstallDSHUsesPinnedOfficialPackage(t *testing.T) {
	fixture := &commandFixture{available: map[string]bool{"npm": true, "dsh": true}}
	if err := InstallDSH(context.Background(), fixture, "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if len(fixture.calls) != 2 || !strings.Contains(fixture.calls[0], "@deepseek-ai/dsh@0.1.0") || fixture.calls[1] != "dsh --version" {
		t.Fatalf("unexpected installer call: %#v", fixture.calls)
	}
}

func TestInstallDSHReportsMissingNodeToolchain(t *testing.T) {
	fixture := &commandFixture{available: map[string]bool{}}
	if err := InstallDSH(context.Background(), fixture, "0.1.0"); err == nil || !strings.Contains(err.Error(), "Node/npm") {
		t.Fatalf("missing toolchain was not actionable: %v", err)
	}
}

func TestEnsureTencentDeploymentPreservesUpstreamEnvAndStartsPinnedStack(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tencent-memory")
	deployDir := filepath.Join(root, "deploy", "global-images")
	if err := os.MkdirAll(deployDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"verify.sh", "start-all.sh"} {
		if err := os.WriteFile(filepath.Join(deployDir, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(deployDir, ".env.example"), []byte("MEMORY_LLM_MODEL=example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := &commandFixture{available: map[string]bool{"git": true, "docker": true}}
	if err := EnsureTencentDeployment(context.Background(), fixture, TencentDeploymentOptions{Root: root, Ref: "pinned-ref"}); err != nil {
		t.Fatal(err)
	}
	envData, err := os.ReadFile(filepath.Join(deployDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(envData) != "MEMORY_LLM_MODEL=example\n" {
		t.Fatalf("upstream env structure was not preserved: %q", envData)
	}
	if info, err := os.Stat(filepath.Join(deployDir, ".env")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("deployment env is not private: info=%v err=%v", info, err)
	}
	joined := strings.Join(fixture.calls, "\n")
	if !strings.Contains(joined, "git -C "+root+" fetch --depth 1 origin pinned-ref") || !strings.Contains(joined, filepath.Join(deployDir, "verify.sh")+" --skip-llm") || !strings.Contains(joined, filepath.Join(deployDir, "start-all.sh")) {
		t.Fatalf("unexpected deployment calls: %#v", fixture.calls)
	}
}

func TestTencentAdminKeyIsReadOnlyFromManagedDeployment(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tencent-memory")
	path := filepath.Join(root, "deploy", "global-images")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ".admin-key"), []byte("sk-admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := TencentAdminKey(root)
	if err != nil || key != "sk-admin" {
		t.Fatalf("unexpected admin key read: %q %v", key, err)
	}
}

func TestInstallDSHPluginsUsesProfilePluginMechanism(t *testing.T) {
	fixture := &commandFixture{
		available: map[string]bool{"pnpm": true, "uvx": true, "dsh": true},
		outputs:   map[string]string{"dsh --profile web --dump-config": "superpowers-dsh\ndsh-reverse-skill\n"},
	}
	if err := InstallDSHPlugins(context.Background(), fixture, PinnedDSHVersion); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(fixture.calls, "\n")
	for _, marker := range []string{"dsh plugin --profile web add superpowers-dsh@0.1.1", "dsh plugin --profile web add https://github.com/dhicoc/dsh-reverse-skill.git#" + PinnedReverseSkillCommit, "dsh plugin --profile web add @deepseek-ai/dsh-mcp-client@" + PinnedMCPClientVersion} {
		if !strings.Contains(joined, marker) {
			t.Fatalf("missing pinned DSH plugin operation %q in %#v", marker, fixture.calls)
		}
	}
}

var errCommandMissing = &commandError{}

type commandError struct{}

func (*commandError) Error() string { return "missing" }
