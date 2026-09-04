package managedruntime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type packageProgressRecorder struct {
	mu    sync.Mutex
	steps []string
}

func (r *packageProgressRecorder) Step(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, label)
}

func (r *packageProgressRecorder) Download(string, int64, int64) {}

func (r *packageProgressRecorder) hasStep(fragment string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, step := range r.steps {
		if strings.Contains(step, fragment) {
			return true
		}
	}
	return false
}

func TestPackageEnvironmentIsolatesProviderCredentials(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-secret")
	t.Setenv("OPENAI_API_KEY", "openai-secret")
	t.Setenv("PATH", "host-path")

	generation := filepath.Join(t.TempDir(), "generation")
	destination := filepath.Join(generation, "dsh")
	environment := packageEnvironment(generation, destination, "npm")
	joined := strings.Join(environment, "\n")
	for _, secret := range []string{"DEEPSEEK_API_KEY", "OPENAI_API_KEY", "deepseek-secret", "openai-secret"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("package environment leaked %q: %q", secret, joined)
		}
	}
	if !strings.Contains(joined, "PATH=") || !strings.Contains(joined, generation) || !strings.Contains(joined, "host-path") {
		t.Fatalf("package environment is missing managed or host path entries: %q", joined)
	}
	for _, required := range []string{
		"npm_config_ignore_scripts=true", "npm_config_audit=false", "npm_config_fund=false", "npm_config_update_notifier=false",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("package environment missing %q: %q", required, joined)
		}
	}
}

func TestFindGenerationExecutableUsesCatalogEntryPoint(t *testing.T) {
	generation := filepath.Join(t.TempDir(), "generation-1")
	pythonRoot := filepath.Join(generation, string(ComponentPython), "bin")
	if err := os.MkdirAll(pythonRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(pythonRoot, "python3.14")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	plan := ComponentPlan{ID: ComponentPython, EntryPoint: "python3.14"}
	resolved, err := findGenerationExecutableForPlan(generation, plan, []ComponentID{ComponentPython})
	if err != nil {
		t.Fatalf("catalog entry point was not resolved: %v", err)
	}
	if resolved != executable {
		t.Fatalf("resolved executable=%q, want %q", resolved, executable)
	}
}

func TestPackageEnvironmentIncludesNestedManagedRuntimeExecutableDirectories(t *testing.T) {
	generation := filepath.Join(t.TempDir(), "generation-1")
	nodeDirectory := filepath.Join(generation, string(ComponentNode), "node-v24.20.0-win-x64")
	if err := os.MkdirAll(nodeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(nodeDirectory, "node.exe")
	if err := os.WriteFile(node, []byte("node"), 0o700); err != nil {
		t.Fatal(err)
	}
	environment := packageEnvironment(generation, filepath.Join(generation, "dsh"), "npm")
	var pathValue string
	for _, value := range environment {
		if strings.HasPrefix(value, "PATH=") {
			pathValue = strings.TrimPrefix(value, "PATH=")
			break
		}
	}
	if !strings.Contains(string(os.PathListSeparator)+pathValue+string(os.PathListSeparator), string(os.PathListSeparator)+nodeDirectory+string(os.PathListSeparator)) {
		t.Fatalf("nested managed Node directory is missing from package PATH: %q", pathValue)
	}
}

func TestNPMInstallArgsKeepPeerDependenciesHoisted(t *testing.T) {
	args := npmInstallArgs("C:\\bundle\\dsh", "C:\\bundle\\dsh.tgz")
	joined := strings.Join(args, "\x00")
	for _, required := range []string{
		"--ignore-scripts", "--package-lock=false", "--install-strategy=hoisted", "--omit=dev",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("npm install args missing %q: %#v", required, args)
		}
	}
	if strings.Contains(joined, "--legacy-peer-deps") || strings.Contains(joined, "--install-strategy=shallow") {
		t.Fatalf("npm install args use an unsafe dependency layout: %#v", args)
	}
}

func TestPNPMInstallArgsUseManagedPeerAwareInstall(t *testing.T) {
	args := pnpmInstallArgs("C:\\bundle\\dsh", "C:\\bundle\\dsh.tgz", "C:\\bundle\\generation-1")
	joined := strings.Join(args, "\x00")
	storeDirectory := filepath.Join("C:\\bundle\\generation-1", ".pnpm-store")
	for _, required := range []string{
		"add", "--dir", "C:\\bundle\\dsh", "--prod", "--ignore-scripts", "--no-lockfile",
		"--reporter=append-only", "--node-linker=hoisted", "--store-dir", storeDirectory,
		"C:\\bundle\\dsh.tgz",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("pnpm install args missing %q: %#v", required, args)
		}
	}
	if strings.Contains(joined, "--no-optional") || strings.Contains(joined, "--legacy-peer-deps") {
		t.Fatalf("pnpm install args disable runtime dependencies: %#v", args)
	}
}

func TestManagedCommandReportsHeartbeatWhileWaiting(t *testing.T) {
	recorder := &packageProgressRecorder{}
	var executable string
	var args []string
	if runtime.GOOS == "windows" {
		executable = "cmd.exe"
		args = []string{"/d", "/c", "ping.exe -n 3 127.0.0.1 > nul"}
	} else {
		executable = "/bin/sh"
		args = []string{"-c", "sleep 0.15"}
	}
	environment := []string{"PATH=" + os.Getenv("PATH")}
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		environment = append(environment, "SystemRoot="+systemRoot)
	}
	if err := runManagedExecutableWithProgressInterval(context.Background(), executable, args, environment, recorder, "Resolving test dependencies", 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !recorder.hasStep("Resolving test dependencies in progress") || !recorder.hasStep("still running") {
		t.Fatalf("managed command progress did not include start and heartbeat events")
	}
}

func TestUVToolInstallUsesTheVerifiedArtifactAsTheRequirement(t *testing.T) {
	args := uvToolInstallArgs("C:\\bundle\\strix_agent-1.6.1-py3-none-win_amd64.whl", "C:\\bundle\\python.exe")
	want := []string{"tool", "install", "--python", "C:\\bundle\\python.exe", "--force", "C:\\bundle\\strix_agent-1.6.1-py3-none-win_amd64.whl"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("uv tool install args=%#v, want %#v", args, want)
	}
}

func TestToolArtifactFilenamePreservesPackageExtension(t *testing.T) {
	for _, test := range []struct {
		url  string
		want string
	}{
		{url: "https://files.example/strix_agent-1.6.1-py3-none-win_amd64.whl", want: "strix_agent-1.6.1-py3-none-win_amd64.whl"},
		{url: "https://registry.example/npm/-/npm-12.0.2.tgz?download=1", want: "npm-12.0.2.tgz"},
	} {
		got, err := toolArtifactFilename(test.url)
		if err != nil || got != test.want {
			t.Fatalf("toolArtifactFilename(%q)=%q, err=%v; want %q", test.url, got, err, test.want)
		}
	}
}

func TestToolArtifactFilenameRejectsUnsafeOrExtensionlessURLs(t *testing.T) {
	for _, rawURL := range []string{
		"https://files.example/../strix.whl",
		"https://files.example/",
		"https://files.example/strix",
	} {
		if _, err := toolArtifactFilename(rawURL); err == nil {
			t.Fatalf("unsafe or extensionless tool artifact URL was accepted: %q", rawURL)
		}
	}
}
