package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type commandFixture struct {
	available map[string]bool
	calls     []string
	outputs   map[string]string
}

type codexInstallFixture struct {
	*commandFixture
	setVersionAfterInstall bool
}

func (f *codexInstallFixture) Run(ctx context.Context, name string, args ...string) (string, error) {
	output, err := f.commandFixture.Run(ctx, name, args...)
	if err == nil && name == "npm" && len(args) >= 3 && args[0] == "install" && args[1] == "--global" && strings.HasPrefix(args[2], "@openai/codex@") {
		f.available["codex"] = true
		if f.setVersionAfterInstall {
			f.outputs["codex --version"] = "codex-cli 0.150.0"
		}
	}
	return output, err
}

type npmGlobalPermissionFixture struct {
	*commandFixture
	installName string
}

func (f *npmGlobalPermissionFixture) Run(ctx context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if name == "npm" && len(args) == 3 && args[0] == "view" {
		switch args[1] {
		case "@deepseek-ai/dsh":
			return "0.1.5", nil
		case "@openai/codex":
			return "0.150.0", nil
		}
	}
	if name == "npm" && len(args) >= 3 && args[0] == "install" && args[1] == "--global" {
		return "", errors.New("EACCES: permission denied, global npm prefix is root-owned")
	}
	if name == "sudo" && len(args) >= 4 && args[0] == "-n" && args[1] == "npm" && args[2] == "install" && args[3] == "--global" {
		f.available[f.installName] = true
		return "", nil
	}
	if name == "dsh" && len(args) == 1 && args[0] == "--version" {
		return "dsh 0.1.5", nil
	}
	if name == "codex" && len(args) == 1 && args[0] == "--version" {
		return "codex-cli 0.150.0", nil
	}
	return "ok", nil
}

func (f *npmGlobalPermissionFixture) LookPath(name string) (string, error) {
	return f.commandFixture.LookPath(name)
}

type failingCommandFixture struct {
	base       *commandFixture
	failMatch  string
	failRemain int
}

func (f *failingCommandFixture) LookPath(name string) (string, error) {
	return f.base.LookPath(name)
}

func (f *failingCommandFixture) Run(ctx context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	if f.failRemain > 0 && strings.Contains(call, f.failMatch) {
		f.failRemain--
		f.base.calls = append(f.base.calls, call)
		return "", errors.New("simulated deployment start failure")
	}
	return f.base.Run(ctx, name, args...)
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

func TestInstallDSHDefaultsToLatestOfficialPackage(t *testing.T) {
	fixture := &commandFixture{available: map[string]bool{"npm": true, "dsh": true}, outputs: map[string]string{
		"dsh --version":                     "dsh 0.2.0\n",
		"npm view @deepseek-ai/dsh version": "0.2.0\n",
	}}
	if err := InstallDSH(context.Background(), fixture, ""); err != nil {
		t.Fatal(err)
	}
	if len(fixture.calls) != 2 || fixture.calls[0] != "dsh --version" || fixture.calls[1] != "npm view @deepseek-ai/dsh version" {
		t.Fatalf("unexpected installer call: %#v", fixture.calls)
	}
}

func TestInstallDSHWithVersionReportsLatestCommandVersion(t *testing.T) {
	fixture := &commandFixture{available: map[string]bool{"npm": true, "dsh": true}, outputs: map[string]string{
		"dsh --version":                     "dsh 0.2.0\n",
		"npm view @deepseek-ai/dsh version": "0.2.0\n",
	}}
	version, err := InstallDSHWithVersion(context.Background(), fixture, "")
	if err != nil {
		t.Fatal(err)
	}
	if version != "0.2.0" {
		t.Fatalf("reported DSH version=%q, want 0.2.0", version)
	}
}

func TestInstallDSHWithVersionRejectsWrongExplicitVersion(t *testing.T) {
	fixture := &commandFixture{available: map[string]bool{"npm": true, "dsh": true}, outputs: map[string]string{
		"dsh --version": "dsh 0.2.0\n",
	}}
	if _, err := InstallDSHWithVersion(context.Background(), fixture, "0.1.0"); err == nil || !strings.Contains(err.Error(), "0.1.0") {
		t.Fatalf("wrong explicit DSH version was accepted: %v", err)
	}
}

func TestInstallDSHReportsMissingNodeToolchain(t *testing.T) {
	fixture := &commandFixture{available: map[string]bool{}}
	if err := InstallDSH(context.Background(), fixture, "0.1.0"); err == nil || !strings.Contains(err.Error(), "Node/npm") {
		t.Fatalf("missing toolchain was not actionable: %v", err)
	}
}

func TestInstallDSHFallsBackToSudoForRootOwnedGlobalNpmPrefix(t *testing.T) {
	fixture := &npmGlobalPermissionFixture{
		commandFixture: &commandFixture{available: map[string]bool{"npm": true, "sudo": true}, outputs: map[string]string{}},
		installName:    "dsh",
	}
	version, err := InstallDSHWithVersion(context.Background(), fixture, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if version != "0.1.5" || len(fixture.calls) != 4 || fixture.calls[2] != "sudo -n npm install --global @deepseek-ai/dsh@0.1.5" {
		t.Fatalf("unexpected sudo npm fallback=%q calls=%#v", version, fixture.calls)
	}
}

type dshProbeFixture struct {
	immediateError bool
}

func (dshProbeFixture) LookPath(name string) (string, error) {
	if name == "dsh" {
		return "/fake/dsh", nil
	}
	return "", errCommandMissing
}

func (fixture dshProbeFixture) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name != "dsh" || len(args) != 2 || args[0] != "web" || args[1] != "--no-open" {
		return "", errors.New("unexpected DSH probe command")
	}
	if fixture.immediateError {
		return "", errors.New("startup failed")
	}
	<-ctx.Done()
	return "http://127.0.0.1:8080", ctx.Err()
}

func TestProbeDSHStartupAcceptsExpectedLongRunningHeadlessService(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A cancelled parent is an interruption, not a successful startup probe.
	if err := ProbeDSHStartup(ctx, dshProbeFixture{}); err == nil {
		t.Fatal("cancelled probe should fail")
	}
}

func TestProbeDSHStartupTreatsBoundedServiceLifetimeAsLiveness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := ProbeDSHStartup(ctx, dshProbeFixture{}); err != nil {
		t.Fatalf("probe error=%v", err)
	}
}

func TestProbeDSHStartupRejectsImmediateFailure(t *testing.T) {
	if err := ProbeDSHStartup(context.Background(), dshProbeFixture{immediateError: true}); err == nil {
		t.Fatal("immediate DSH failure was accepted")
	}
}

type proxyRepairFixture struct {
	calls []string
}

func (f *proxyRepairFixture) LookPath(name string) (string, error) {
	return "/fake/" + name, nil
}

func (f *proxyRepairFixture) Run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if call == "sudo -n docker inspect --format={{.State.Status}} tdai-proxy" {
		return "exited\n", nil
	}
	return "", nil
}

func TestRestartExitedTencentProxyRepairsPermissionFailureWithoutWeakeningConfig(t *testing.T) {
	fixture := &proxyRepairFixture{}
	if err := restartExitedTencentProxy(context.Background(), fixture); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(fixture.calls, "\n"), "sudo -n docker restart tdai-proxy") {
		t.Fatalf("stopped proxy was not restarted: %#v", fixture.calls)
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
	digest := func(name string) string { return `["` + name + `@sha256:` + strings.Repeat("c", 64) + `"]` }
	fixture := &commandFixture{available: map[string]bool{"git": true, "docker": true}, outputs: map[string]string{"git -C " + root + " rev-parse HEAD": strings.Repeat("a", 40), "docker inspect --format={{json .RepoDigests}} tdai-memory-core": digest("tdai/core"), "docker inspect --format={{json .RepoDigests}} tdai-memory-hub": digest("tdai/hub"), "docker inspect --format={{json .RepoDigests}} tdai-proxy": digest("tdai/proxy")}}
	if err := EnsureTencentDeployment(context.Background(), fixture, TencentDeploymentOptions{Root: root, Ref: strings.Repeat("a", 40), Runtime: testTencentRuntimeConfig()}); err != nil {
		t.Fatal(err)
	}
	envData, err := os.ReadFile(filepath.Join(deployDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(envData), "MEMORY_LLM_MODEL=example") || !strings.Contains(string(envData), "PROXY_UPSTREAM_MODEL='proxy-model'") {
		t.Fatalf("upstream env structure/runtime values were not preserved: %q", envData)
	}
	if info, err := os.Stat(filepath.Join(deployDir, ".env")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("deployment env is not private: info=%v err=%v", info, err)
	}
	manifest, err := ReadTencentDeploymentManifest(root)
	if err != nil || manifest.ResolvedCommit != strings.Repeat("a", 40) || len(manifest.ContainerImageDigests) != 3 || len(manifest.UnresolvedContainers) != 0 {
		t.Fatalf("immutable deployment manifest was not recorded: %#v err=%v", manifest, err)
	}
	joined := strings.Join(fixture.calls, "\n")
	if !strings.Contains(joined, "git -C "+root+" fetch --depth 1 origin "+strings.Repeat("a", 40)) || !strings.Contains(joined, filepath.Join(deployDir, "verify.sh")+" --skip-llm") || !strings.Contains(joined, filepath.Join(deployDir, "start-all.sh")) {
		t.Fatalf("unexpected deployment calls: %#v", fixture.calls)
	}
	if !strings.Contains(joined, "docker update --restart unless-stopped tdai-memory-core") || !strings.Contains(joined, "docker update --restart unless-stopped tdai-memory-hub") || !strings.Contains(joined, "docker update --restart unless-stopped tdai-proxy") {
		t.Fatalf("managed restart policies were not set: %#v", fixture.calls)
	}
}

func TestEnsureTencentDeploymentResolvesLatestDefaultHeadToImmutableCommit(t *testing.T) {
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
	latest := strings.Repeat("d", 40)
	digest := func(name string) string { return `["` + name + `@sha256:` + strings.Repeat("c", 64) + `"]` }
	fixture := &commandFixture{available: map[string]bool{"git": true, "docker": true}, outputs: map[string]string{
		"git ls-remote " + TencentMemoryRepository + " HEAD":             latest + "\tHEAD\n",
		"git -C " + root + " rev-parse HEAD":                             latest,
		"docker inspect --format={{json .RepoDigests}} tdai-memory-core": digest("tdai/core"),
		"docker inspect --format={{json .RepoDigests}} tdai-memory-hub":  digest("tdai/hub"),
		"docker inspect --format={{json .RepoDigests}} tdai-proxy":       digest("tdai/proxy"),
	}}
	if err := EnsureTencentDeployment(context.Background(), fixture, TencentDeploymentOptions{Root: root, Runtime: testTencentRuntimeConfig()}); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadTencentDeploymentManifest(root)
	if err != nil || manifest.RequestedRef != latest || manifest.ResolvedCommit != latest {
		t.Fatalf("latest Tencent manifest=%#v err=%v", manifest, err)
	}
	joined := strings.Join(fixture.calls, "\n")
	if !strings.Contains(joined, "git ls-remote "+TencentMemoryRepository+" HEAD") || !strings.Contains(joined, "origin "+latest) {
		t.Fatalf("latest Tencent resolution calls missing: %#v", fixture.calls)
	}
}

func TestEnsureTencentDeploymentUsesSudoOnlyForManagedScripts(t *testing.T) {
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
	digest := func(name string) string { return `["` + name + `@sha256:` + strings.Repeat("d", 64) + `"]` }
	fixture := &commandFixture{available: map[string]bool{"git": true, "docker": true, "sudo": true}, outputs: map[string]string{"git -C " + root + " rev-parse HEAD": strings.Repeat("b", 40), "sudo -n docker inspect --format={{json .RepoDigests}} tdai-memory-core": digest("tdai/core"), "sudo -n docker inspect --format={{json .RepoDigests}} tdai-memory-hub": digest("tdai/hub"), "sudo -n docker inspect --format={{json .RepoDigests}} tdai-proxy": digest("tdai/proxy")}}
	if err := EnsureTencentDeployment(context.Background(), fixture, TencentDeploymentOptions{Root: root, Ref: strings.Repeat("b", 40), UseSudo: true, Runtime: testTencentRuntimeConfig()}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(fixture.calls, "\n")
	if !strings.Contains(joined, "sudo -n bash "+filepath.Join(deployDir, "verify.sh")+" --skip-llm") || !strings.Contains(joined, "sudo -n bash "+filepath.Join(deployDir, "start-all.sh")) {
		t.Fatalf("managed scripts did not use sudo: %#v", fixture.calls)
	}
	if !strings.Contains(joined, "sudo -n chown -R ") {
		t.Fatalf("managed ownership was not restored: %#v", fixture.calls)
	}
}

func TestEnsureTencentDeploymentProtectsProxyConfigFromNonRootImage(t *testing.T) {
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
	digest := func(name string) string { return `["` + name + `@sha256:` + strings.Repeat("f", 64) + `"]` }
	fixture := &commandFixture{available: map[string]bool{"git": true, "docker": true, "sudo": true}, outputs: map[string]string{
		"git -C " + root + " rev-parse HEAD":                                     strings.Repeat("c", 40),
		"sudo -n docker inspect --format={{json .RepoDigests}} tdai-memory-core": digest("tdai/core"),
		"sudo -n docker inspect --format={{json .RepoDigests}} tdai-memory-hub":  digest("tdai/hub"),
		"sudo -n docker inspect --format={{json .RepoDigests}} tdai-proxy":       digest("tdai/proxy"),
	}}
	if err := EnsureTencentDeployment(context.Background(), fixture, TencentDeploymentOptions{
		Root:    root,
		Ref:     strings.Repeat("c", 40),
		UseSudo: true,
		Runtime: testTencentRuntimeConfig(),
	}); err != nil {
		t.Fatal(err)
	}

	configDir := filepath.Join(deployDir, ".proxy-config")
	configFile := filepath.Join(configDir, "config.yaml")
	joined := strings.Join(fixture.calls, "\n")
	for _, expected := range []string{
		"sudo -n mkdir -p " + configDir,
		"sudo -n chmod 0755 " + configDir,
		"sudo -n touch " + configFile,
		"sudo -n chmod 0400 " + configFile,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing proxy config permission operation %q in %#v", expected, fixture.calls)
		}
	}
	if strings.Count(joined, "sudo -n chown 10001:10001 "+configFile) < 2 {
		t.Fatalf("proxy config was not secured before startup and after ownership restore: %#v", fixture.calls)
	}
}

func TestEnsureTencentDeploymentStopsBeforeCheckoutWhenProviderConfigIsMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-yet-created")
	fixture := &commandFixture{available: map[string]bool{"git": true, "docker": true}}
	err := EnsureTencentDeployment(context.Background(), fixture, TencentDeploymentOptions{Root: root})
	if err == nil || !strings.Contains(err.Error(), "BARON_TENCENT_MEMORY_LLM_API_KEY") {
		t.Fatalf("missing provider preflight was not actionable: %v", err)
	}
	for _, call := range fixture.calls {
		if strings.Contains(call, "git clone") || strings.Contains(call, "git fetch") {
			t.Fatalf("provider preflight happened after checkout: %#v", fixture.calls)
		}
	}
}

func TestTencentDeploymentRejectsMovingRefBeforeCheckout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "moving-ref")
	fixture := &commandFixture{available: map[string]bool{"git": true, "docker": true}}
	err := EnsureTencentDeployment(context.Background(), fixture, TencentDeploymentOptions{Root: root, Ref: "main", Runtime: testTencentRuntimeConfig()})
	if err == nil || !strings.Contains(err.Error(), "immutable commit SHA") {
		t.Fatalf("moving Tencent ref was accepted: %v", err)
	}
	if len(fixture.calls) != 0 {
		t.Fatalf("moving ref was rejected after command execution: %#v", fixture.calls)
	}
}

func TestTencentDeploymentUpdateRollsBackPreviousPinnedCommit(t *testing.T) {
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
	oldRef := strings.Repeat("a", 40)
	newRef := strings.Repeat("b", 40)
	digest := func(name string) string { return `["` + name + `@sha256:` + strings.Repeat("e", 64) + `"]` }
	base := &commandFixture{available: map[string]bool{"git": true, "docker": true}, outputs: map[string]string{
		"git -C " + root + " rev-parse HEAD":                             oldRef,
		"docker inspect --format={{json .RepoDigests}} tdai-memory-core": digest("tdai/core"),
		"docker inspect --format={{json .RepoDigests}} tdai-memory-hub":  digest("tdai/hub"),
		"docker inspect --format={{json .RepoDigests}} tdai-proxy":       digest("tdai/proxy"),
	}}
	runtimeConfig := testTencentRuntimeConfig()
	if err := EnsureTencentDeployment(context.Background(), base, TencentDeploymentOptions{Root: root, Ref: oldRef, Runtime: runtimeConfig}); err != nil {
		t.Fatal(err)
	}
	previous, err := readTencentDeploymentManifest(root)
	if err != nil || previous.ResolvedCommit != oldRef {
		t.Fatalf("initial deployment manifest missing: %#v err=%v", previous, err)
	}
	failing := &failingCommandFixture{base: base, failMatch: "start-all.sh", failRemain: 1}
	_, err = UpdateTencentDeployment(context.Background(), failing, TencentDeploymentOptions{Root: root, Ref: newRef, Runtime: runtimeConfig})
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("failed update was not rolled back: %v", err)
	}
	restored, err := readTencentDeploymentManifest(root)
	if err != nil || restored.ResolvedCommit != oldRef {
		t.Fatalf("previous manifest was not restored: %#v err=%v", restored, err)
	}
	joined := strings.Join(base.calls, "\n")
	if !strings.Contains(joined, "origin "+newRef) || !strings.Contains(joined, "origin "+oldRef) {
		t.Fatalf("update/rollback did not visit both immutable commits: %s", joined)
	}
}

func testTencentRuntimeConfig() TencentRuntimeConfig {
	return TencentRuntimeConfig{
		MemoryLLMBaseURL:    "https://memory.example/v1",
		MemoryLLMAPIKey:     "sk-memory-test",
		MemoryLLMModel:      "memory-model",
		ProxyUpstreamURL:    "https://proxy.example/v1",
		ProxyUpstreamAPIKey: "sk-proxy-test",
		ProxyUpstreamModel:  "proxy-model",
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

func TestInstallDSHPluginsSkipsAlreadyConfiguredProfiles(t *testing.T) {
	fixture := &commandFixture{
		available: map[string]bool{"pnpm": true, "uvx": true, "dsh": true},
		outputs: map[string]string{
			"dsh --profile web --dump-config":      "superpowers-dsh\ndsh-reverse-skill\n@deepseek-ai/dsh-mcp-client\n",
			"dsh --profile headless --dump-config": "superpowers-dsh\ndsh-reverse-skill\n@deepseek-ai/dsh-mcp-client\n",
		},
	}
	if err := InstallDSHPlugins(context.Background(), fixture, ""); err != nil {
		t.Fatal(err)
	}
	if len(fixture.calls) != 2 || fixture.calls[0] != "dsh --profile web --dump-config" || fixture.calls[1] != "dsh --profile headless --dump-config" {
		t.Fatalf("already configured profiles were mutated: %#v", fixture.calls)
	}
}

func TestDSHProfileMarkerRequiresAPluginToken(t *testing.T) {
	if DSHProfileHasMarker("user text: superpowers-dsh-disabled", "superpowers-dsh") {
		t.Fatal("disabled plugin text was treated as an installed marker")
	}
	if DSHProfileHasMarker("adapter path: /tmp/baron-dsh-adapter-old", "baron-dsh-adapter") {
		t.Fatal("unrelated adapter path was treated as the managed marker")
	}
	for _, test := range []struct {
		dump   string
		marker string
	}{
		{dump: "name: superpowers-dsh\n", marker: "superpowers-dsh"},
		{dump: `"@deepseek-ai/dsh-mcp-client": {}`, marker: "dsh-mcp-client"},
		{dump: "superpowers-dsh@1.2.3\n", marker: "superpowers-dsh"},
	} {
		if !DSHProfileHasMarker(test.dump, test.marker) {
			t.Fatalf("valid marker %q was not found in %q", test.marker, test.dump)
		}
	}
}

func TestInstallDSHPluginsAddsOnlyMissingProfileEntries(t *testing.T) {
	fixture := &profilePluginFixture{
		dumps: map[string]string{
			"web":      "superpowers-dsh\n",
			"headless": "superpowers-dsh\ndsh-reverse-skill\ndsh-mcp-client\n",
		},
	}
	report, err := InstallDSHPluginsWithReport(context.Background(), fixture, "")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Changed || len(fixture.adds) != 2 {
		t.Fatalf("missing profile entries were not selectively added: report=%#v adds=%#v", report, fixture.adds)
	}
	for _, call := range fixture.adds {
		if strings.Contains(call, "headless") {
			t.Fatalf("complete profile was mutated: %#v", fixture.adds)
		}
	}
}

func TestInstallDSHPluginsUpdatesVersionedManagedNPMPlugin(t *testing.T) {
	fixture := &versionedProfilePluginFixture{
		dumps: map[string]string{
			"web":      "superpowers-dsh 1.0.0\ndsh-reverse-skill\ndsh-mcp-client\n",
			"headless": "superpowers-dsh\ndsh-reverse-skill\ndsh-mcp-client\n",
		},
		latest: map[string]string{"superpowers-dsh": "1.1.0"},
	}
	report, err := InstallDSHPluginsWithReport(context.Background(), fixture, "")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Changed || len(fixture.adds) != 1 || !strings.Contains(fixture.adds[0], "superpowers-dsh@1.1.0") {
		t.Fatalf("versioned stale plugin was not updated exactly: report=%#v adds=%#v", report, fixture.adds)
	}
}

type profilePluginFixture struct {
	dumps map[string]string
	adds  []string
}

type versionedProfilePluginFixture struct {
	dumps  map[string]string
	latest map[string]string
	adds   []string
}

func (f *versionedProfilePluginFixture) LookPath(name string) (string, error) {
	if name == "pnpm" || name == "uvx" || name == "npm" {
		return "/fake/" + name, nil
	}
	return "", errCommandMissing
}

func (f *versionedProfilePluginFixture) Run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	if name == "npm" && len(args) == 3 && args[0] == "view" {
		if version, ok := f.latest[args[1]]; ok {
			return version, nil
		}
		return "", errors.New("unexpected npm package")
	}
	if name != "dsh" {
		return "", errors.New("unexpected command")
	}
	if len(args) == 3 && args[0] == "--profile" && args[2] == "--dump-config" {
		return f.dumps[args[1]], nil
	}
	if len(args) == 5 && args[0] == "plugin" && args[1] == "--profile" && args[3] == "add" {
		f.adds = append(f.adds, call)
		if strings.Contains(args[4], "superpowers-dsh@1.1.0") {
			f.dumps[args[2]] = "superpowers-dsh 1.1.0\ndsh-reverse-skill\ndsh-mcp-client\n"
		}
		return "", nil
	}
	return "", errors.New("unexpected DSH command")
}

func (f *profilePluginFixture) LookPath(name string) (string, error) {
	if name == "pnpm" || name == "uvx" {
		return "/fake/" + name, nil
	}
	return "", errCommandMissing
}

func (f *profilePluginFixture) Run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	if name != "dsh" {
		return "", errors.New("unexpected command")
	}
	if len(args) == 3 && args[0] == "--profile" && args[2] == "--dump-config" {
		return f.dumps[args[1]], nil
	}
	if len(args) == 5 && args[0] == "plugin" && args[1] == "--profile" && args[3] == "add" {
		f.adds = append(f.adds, call)
		profile := args[2]
		if strings.Contains(args[4], "superpowers-dsh") {
			f.dumps[profile] += "superpowers-dsh\n"
		}
		if strings.Contains(args[4], "reverse-skill") {
			f.dumps[profile] += "dsh-reverse-skill\n"
		}
		if strings.Contains(args[4], "mcp-client") {
			f.dumps[profile] += "dsh-mcp-client\n"
		}
		return "", nil
	}
	return "", errors.New("unexpected DSH command")
}

var errCommandMissing = &commandError{}

type commandError struct{}

func (*commandError) Error() string { return "missing" }
