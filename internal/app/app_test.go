package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baron-shared-brain/baron/internal/cli"
	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/continuity"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/credentials"
	"github.com/baron-shared-brain/baron/internal/doctor"
	"github.com/baron-shared-brain/baron/internal/install"
	"github.com/baron-shared-brain/baron/internal/project"
	"github.com/baron-shared-brain/baron/internal/release"
	"github.com/baron-shared-brain/baron/internal/storage"
)

func TestReleaseHTTPClientUsesArtifactTimeoutWithoutMutatingBase(t *testing.T) {
	base := &http.Client{Timeout: 3 * time.Second}

	got := releaseHTTPClient(base)
	if got == base {
		t.Fatal("release client must be a clone so shared clients are not mutated")
	}
	if got.Timeout < releaseDownloadTimeout {
		t.Fatalf("release timeout=%s, want at least %s", got.Timeout, releaseDownloadTimeout)
	}
	if base.Timeout != 3*time.Second {
		t.Fatalf("base timeout=%s, want 3s", base.Timeout)
	}
}

type credentialOrderingRunner struct {
	calls []string
}

func (r *credentialOrderingRunner) LookPath(name string) (string, error) {
	return "/fake/" + name, nil
}

func (r *credentialOrderingRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return "", nil
}

func TestResolveDSHCredentialPromptsAndPersistsOfficialStore(t *testing.T) {
	dshHome := t.TempDir()
	t.Setenv("DSH_HOME", dshHome)
	t.Setenv("DEEPSEEK_API_KEY", "")
	var output bytes.Buffer
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	application.PromptOutput = &output
	application.ReadSecret = func(io.Reader) ([]byte, error) { return []byte("  dsh-test-key\n"), nil }
	application.ValidateProviderCredential = func(context.Context, string, string) error { return nil }

	key, err := application.resolveDSHCredential()
	if err != nil {
		t.Fatal(err)
	}
	if key != "dsh-test-key" {
		t.Fatalf("key=%q", key)
	}
	stored, err := install.ReadDSHProviderKey(map[string]string{"DSH_HOME": dshHome})
	if err != nil {
		t.Fatal(err)
	}
	if stored != key {
		t.Fatal("prompted DSH key was not written to the official store")
	}
	if strings.Contains(output.String(), key) {
		t.Fatal("prompt output leaked the DSH key")
	}
}

func TestResolveTencentRuntimeConfigReusesDSHKeyAndDefaultsWithoutPrompt(t *testing.T) {
	dshHome := t.TempDir()
	t.Setenv("DSH_HOME", dshHome)
	t.Setenv("DEEPSEEK_API_KEY", "")
	if err := install.EnsureDSHProviderKey(map[string]string{"DSH_HOME": dshHome}, "dsh-reused-key"); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	application := New()
	application.PromptOutput = &output
	application.ValidateProviderCredential = func(context.Context, string, string) error { return nil }
	application.ReadSecret = func(io.Reader) ([]byte, error) {
		t.Fatal("Tencent resolver prompted despite a reusable DSH key")
		return nil, nil
	}
	resolved, err := application.resolveTencentRuntimeConfig(filepath.Join(t.TempDir(), "tencent"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.MemoryLLMBaseURL != "https://api.deepseek.com/v1" || resolved.MemoryLLMModel != "deepseek-chat" || resolved.MemoryLLMAPIKey != "dsh-reused-key" {
		t.Fatal("Tencent did not reuse the DSH key and defaults")
	}
	if output.Len() != 0 {
		t.Fatalf("unexpected prompt output: %q", output.String())
	}
}

func TestResolveDSHCredentialReplacesRejectedStoredKeyOnlyAfterValidation(t *testing.T) {
	dshHome := t.TempDir()
	t.Setenv("DSH_HOME", dshHome)
	t.Setenv("DEEPSEEK_API_KEY", "")
	if err := install.EnsureDSHProviderKey(map[string]string{"DSH_HOME": dshHome}, "old-provider-key"); err != nil {
		t.Fatal(err)
	}
	attempts := []string{"bad-key", "valid-provider-key"}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	application.ReadSecret = func(io.Reader) ([]byte, error) {
		value := attempts[0]
		attempts = attempts[1:]
		return []byte(value), nil
	}
	application.ValidateProviderCredential = func(_ context.Context, _ string, key string) error {
		if key == "old-provider-key" || key == "bad-key" {
			return credentials.ErrInvalidProviderCredential
		}
		return nil
	}
	key, err := application.resolveDSHCredential()
	if err != nil {
		t.Fatal(err)
	}
	if key != "valid-provider-key" {
		t.Fatalf("resolved key=%q", key)
	}
	stored, err := install.ReadDSHProviderKey(map[string]string{"DSH_HOME": dshHome})
	if err != nil {
		t.Fatal(err)
	}
	if stored != "valid-provider-key" {
		t.Fatalf("stored key=%q, want validated replacement", stored)
	}
}

func TestSetCredentialUpdatesDSHAndManagedTencentKeysAfterValidation(t *testing.T) {
	dshHome := t.TempDir()
	t.Setenv("DSH_HOME", dshHome)
	t.Setenv("DEEPSEEK_API_KEY", "")
	deploymentRoot := filepath.Join(t.TempDir(), "tencent")
	deployDir := filepath.Join(deploymentRoot, "deploy", "global-images")
	if err := os.MkdirAll(deployDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, ".env"), []byte("MEMORY_LLM_BASE_URL='https://managed-provider.example/v1'\nMEMORY_LLM_API_KEY='old-provider-key'\nPROXY_UPSTREAM_API_KEY='old-provider-key'\nCUSTOM=keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(t.TempDir(), "global.json")
	if err := config.SaveGlobalState(globalPath, config.GlobalState{TencentInstallPath: deploymentRoot}); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = globalPath
	application.ReadSecret = func(io.Reader) ([]byte, error) { return []byte("new-provider-key"), nil }
	validatedBaseURL := ""
	application.ValidateProviderCredential = func(_ context.Context, baseURL, _ string) error {
		validatedBaseURL = baseURL
		return nil
	}
	if err := application.SetCredential("deepseek"); err != nil {
		t.Fatal(err)
	}
	if validatedBaseURL != "https://managed-provider.example/v1" {
		t.Fatalf("credential rotation used base URL %q, want managed provider URL", validatedBaseURL)
	}
	stored, err := install.ReadDSHProviderKey(map[string]string{"DSH_HOME": dshHome})
	if err != nil || stored != "new-provider-key" {
		t.Fatalf("DSH key=%q err=%v", stored, err)
	}
	envData, err := os.ReadFile(filepath.Join(deployDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(envData), "MEMORY_LLM_API_KEY='new-provider-key'") || !strings.Contains(string(envData), "PROXY_UPSTREAM_API_KEY='new-provider-key'") || !strings.Contains(string(envData), "CUSTOM=keep") {
		t.Fatalf("managed Tencent key rotation was incomplete: %s", envData)
	}
}

func TestReadinessReportsRejectedProviderCredentialWithoutKeyMaterial(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "rejected-provider-key")
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	application.ValidateProviderCredential = func(context.Context, string, string) error {
		return credentials.ErrInvalidProviderCredential
	}
	report, err := application.readinessReport()
	if err != nil {
		t.Fatal(err)
	}
	check := report.ByName("dsh-credentials")
	if check.Status != doctor.StatusIncomplete || check.Suggestion != "baron deepseek api_key" {
		t.Fatalf("rejected provider check=%#v", check)
	}
	if strings.Contains(report.Human(), "rejected-provider-key") {
		t.Fatal("readiness output exposed provider key")
	}
}

func TestReadinessReportsMissingTencentProviderCredential(t *testing.T) {
	root := t.TempDir()
	deployDir := filepath.Join(root, "deploy", "global-images")
	if err := os.MkdirAll(deployDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, ".env"), []byte("MEMORY_LLM_BASE_URL='https://managed-provider.example/v1'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(t.TempDir(), "global.json")
	if err := config.SaveGlobalState(globalPath, config.GlobalState{TencentInstallPath: root}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "")
	application := New()
	application.GlobalPath = globalPath
	report, err := application.readinessReport()
	if err != nil {
		t.Fatal(err)
	}
	check := report.ByName("tencent-provider-credential")
	if check.Status != doctor.StatusIncomplete || check.Suggestion != "baron tencent-memory init" {
		t.Fatalf("missing Tencent provider check=%#v", check)
	}
}

func TestTencentInitRequiresCredentialBeforeDockerOrNetwork(t *testing.T) {
	dshHome := t.TempDir()
	t.Setenv("DSH_HOME", dshHome)
	t.Setenv("DEEPSEEK_API_KEY", "")
	runner := &credentialOrderingRunner{}
	var output bytes.Buffer
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	application.CommandRunner = runner
	application.Input = strings.NewReader("not-a-terminal\n")
	application.PromptOutput = &output

	err := application.TencentInit(context.Background())
	if err == nil || !strings.Contains(err.Error(), "DEEPSEEK_API_KEY") {
		t.Fatalf("missing credential was not actionable: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("Docker/network work started before credential resolution: %#v", runner.calls)
	}
}

func TestTencentDockerBootstrapSkipsSudoWhenServicesAreHealthy(t *testing.T) {
	runner := &credentialOrderingRunner{}
	called := false
	if _, err := ensureTencentDocker(context.Background(), runner, func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("Tencent health probe was not run before Docker bootstrap")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("healthy Tencent stack unexpectedly triggered Docker/sudo work: %#v", runner.calls)
	}
}

func TestRepairRuntimeSkipsDockerAndDeploymentWhenServicesAreHealthy(t *testing.T) {
	runner := &credentialOrderingRunner{}
	deploymentCalled := false
	if err := ensureTencentRuntime(context.Background(), runner, func() error {
		return nil
	}, func() error {
		deploymentCalled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if deploymentCalled {
		t.Fatal("healthy Tencent services unexpectedly triggered deployment")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("healthy Tencent services unexpectedly triggered Docker/sudo work: %#v", runner.calls)
	}
}

func TestResolveTencentAdminKeyUsesManagedFileWithoutPrompt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "deploy", "global-images")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ".admin-key"), []byte("managed-admin-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.ReadSecret = func(io.Reader) ([]byte, error) {
		t.Fatal("managed admin key unexpectedly prompted")
		return nil, nil
	}
	key, err := application.resolveTencentAdminKey(root)
	if err != nil || key != "managed-admin-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}
}

func TestResolveTencentAdminKeyPromptsWithoutPersistingExternalKey(t *testing.T) {
	t.Setenv("BARON_TENCENT_ADMIN_KEY", "")
	root := filepath.Join(t.TempDir(), "missing-deployment")
	var output bytes.Buffer
	application := New()
	application.PromptOutput = &output
	application.ReadSecret = func(io.Reader) ([]byte, error) { return []byte("external-admin-key\n"), nil }
	key, err := application.resolveTencentAdminKey(root)
	if err != nil || key != "external-admin-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	if strings.Contains(output.String(), key) {
		t.Fatal("external admin key was echoed")
	}
	if _, err := os.Stat(filepath.Join(root, "deploy", "global-images", ".admin-key")); !os.IsNotExist(err) {
		t.Fatalf("external admin key was persisted: %v", err)
	}
}

func TestCodexAuthReadyAcceptsCodexHomeAuthFile(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("BARON_CODEX_AUTH_READY", "")
	t.Setenv("OPENAI_API_KEY", "")
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"auth_mode":"chatgpt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !codexAuthReady() {
		t.Fatal("Codex auth in CODEX_HOME should be recognized")
	}
}

func TestCodexInitPrintsOneTimeLoginNotice(t *testing.T) {
	t.Setenv("BARON_CODEX_AUTH_READY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	application := New()
	options := application.CLIOptions(io.Discard, io.Discard)
	notice := options.InitNoticeFunc["codex-cli"]()
	if !strings.Contains(strings.ToLower(notice), "codex") || !strings.Contains(strings.ToLower(notice), "once") {
		t.Fatalf("Codex one-time login notice=%q", notice)
	}
}

func TestCodexInitOmitsLoginNoticeWhenCodexAuthExists(t *testing.T) {
	t.Setenv("BARON_CODEX_AUTH_READY", "1")
	t.Setenv("OPENAI_API_KEY", "")
	application := New()
	options := application.CLIOptions(io.Discard, io.Discard)
	if notice := options.InitNoticeFunc["codex-cli"](); notice != "" {
		t.Fatalf("authenticated Codex should not need a login notice: %q", notice)
	}
}

func TestCodexTrustProjectRootFallsBackToCurrentDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if got := codexTrustProjectRoot(); got != root {
		t.Fatalf("Codex trust root=%q, want %q", got, root)
	}
}

func TestApplicationSetupAndHookRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project A")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	application.ProjectProvisioner = func(context.Context, string, string) (contracts.ProjectBinding, error) {
		return contracts.ProjectBinding{TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}, nil
	}
	if _, err := application.SetupProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".baron", "runtime", "state.db")); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	input := bytes.NewBufferString(`{"session_id":"ses-1","idempotency_key":"app-event-1","payload":{"command":"go test ./...","exit_code":1,"summary":"failed"}}`)
	if err := application.HandleHook(context.Background(), "codex", "tool_finished", root, input, &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["continue"] != true {
		t.Fatalf("unexpected hook response: %s", output.String())
	}
	state, err := config.ReadEnvFile(filepath.Join(root, ".baron", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if state["BARON_TENCENT_AGENT_ID"] != "agent-a" {
		t.Fatalf("binding was not written: %#v", state)
	}
}

func TestCLIOptionsUpdateUsesVerifiedReleaseWithoutProjectState(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("fixture candidate is a Linux amd64 executable")
	}
	root := t.TempDir()
	target := filepath.Join(root, "bin", "baron")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old Baron binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := []byte("#!/bin/sh\necho 'baron 0.1.3'\n")
	manifest := []byte(`{"project":"Baron Nexus","version":"0.1.3","artifacts":["baron-linux-amd64"]}`)
	sum := sha256.Sum256(candidate)
	sums := []byte(fmt.Sprintf("%s  baron-linux-amd64\n", hex.EncodeToString(sum[:])))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/owner/repo/releases/latest":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"tag_name": "v0.1.3",
				"assets": []map[string]string{
					{"name": "baron-linux-amd64", "browser_download_url": "http://" + request.Host + "/download/baron-linux-amd64"},
					{"name": "release-manifest.json", "browser_download_url": "http://" + request.Host + "/download/release-manifest.json"},
					{"name": "SHA256SUMS", "browser_download_url": "http://" + request.Host + "/download/SHA256SUMS"},
				},
			})
		case "/download/baron-linux-amd64":
			_, _ = writer.Write(candidate)
		case "/download/release-manifest.json":
			_, _ = writer.Write(manifest)
		case "/download/SHA256SUMS":
			_, _ = writer.Write(sums)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	application := New()
	application.GlobalPath = filepath.Join(root, "global.json")
	application.ExecutablePath = target
	application.ReleaseClient = &release.Client{
		HTTPClient:    server.Client(),
		APIBaseURL:    server.URL,
		Repository:    "owner/repo",
		GOOS:          "linux",
		GOARCH:        "amd64",
		AllowInsecure: true,
	}
	var output bytes.Buffer
	if code := cli.Run([]string{"update"}, application.CLIOptions(&output, &output)); code != cli.ExitSuccess {
		t.Fatalf("update failed with code %d: %s", code, output.String())
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(candidate) {
		t.Fatalf("updated binary=%q", data)
	}
	if _, err := os.Stat(filepath.Join(root, ".baron")); !os.IsNotExist(err) {
		t.Fatalf("binary update touched project state: %v", err)
	}
	if _, err := os.Stat(application.GlobalPath); !os.IsNotExist(err) {
		t.Fatalf("binary update touched global Baron state: %v", err)
	}
}

func TestCodexHookWrapsRecoveryContextInHookSpecificOutput(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project B")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	application.ProjectProvisioner = func(context.Context, string, string) (contracts.ProjectBinding, error) {
		return contracts.ProjectBinding{TeamID: "team-b", AgentID: "agent-b", UserID: "user-b"}, nil
	}
	project, err := application.SetupProject(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(root, ".baron", "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	engine := continuity.NewEngine(store, project.ProjectID, project.Metadata.Name, filepath.Join(root, ".baron", "checkpoint.json"))
	if err := engine.Save(context.Background(), continuity.WorkState{ProjectID: project.ProjectID, ProjectName: project.Metadata.Name, LastClient: contracts.ClientDSH, SessionID: "dsh-old", SessionState: contracts.SessionActive, Task: continuity.TaskState{Goal: "finish", CurrentStep: "test", NextAction: "rerun test"}}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	store.Close()
	var output bytes.Buffer
	if err := application.HandleHook(context.Background(), "codex", "SessionStart", root, bytes.NewBufferString(`{}`), &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if _, ok := response["hookSpecificOutput"]; !ok {
		t.Fatalf("Codex-specific hook output missing: %s", output.String())
	}
}

func TestHookWiresConfiguredTencentBackendWithoutBlockingLocalState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project Remote")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var captureBodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v3/conversation/add" {
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode capture: %v", err)
			}
			mu.Lock()
			captureBodies = append(captureBodies, body)
			mu.Unlock()
			_, _ = writer.Write([]byte(`{"request_id":"remote-1"}`))
			return
		}
		if request.URL.Path == "/v3/atomic/search" {
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode search: %v", err)
			}
			if value, ok := body["project_id"].(string); !ok || !strings.HasPrefix(value, "prj-") {
				t.Fatalf("search omitted project isolation: %#v", body)
			}
			_, _ = writer.Write([]byte(`{"items":[{"project_id":"","source_client":"dsh","kind":"sentinel","content":"remote sentinel"}]}`))
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()

	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	if err := config.SaveGlobalState(application.GlobalPath, config.GlobalState{Identity: contracts.Identity{
		Endpoint: server.URL, UserID: "user-a", UserKey: "sk-test-key", TeamID: "team-a", ServiceID: "baron",
	}}); err != nil {
		t.Fatal(err)
	}
	application.ProjectProvisioner = func(context.Context, string, string) (contracts.ProjectBinding, error) {
		return contracts.ProjectBinding{TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}, nil
	}
	if _, err := application.SetupProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := application.HandleHook(context.Background(), "dsh", "user_prompt", root, bytes.NewBufferString(`{"prompt":"continue"}`), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "remote sentinel") {
		t.Fatalf("remote context was not returned: %s", output.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(captureBodies) != 1 {
		t.Fatalf("expected one remote capture, got %d", len(captureBodies))
	}
	if value, ok := captureBodies[0]["project_id"].(string); !ok || !strings.HasPrefix(value, "prj-") {
		t.Fatalf("unexpected capture project_id: %#v", captureBodies[0])
	}
	if captureBodies[0]["team_id"] != "team-a" || captureBodies[0]["agent_id"] != "agent-a" || captureBodies[0]["user_id"] != "user-a" {
		t.Fatalf("capture isolation was not strict: %#v", captureBodies[0])
	}
}

func TestTamperedProjectEnvIsRejectedBeforeRemoteHookUse(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project Tampered")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var requests int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		_, _ = writer.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	if err := config.SaveGlobalState(application.GlobalPath, config.GlobalState{Identity: contracts.Identity{Endpoint: server.URL, UserID: "user", UserKey: "sk-key", TeamID: "team"}}); err != nil {
		t.Fatal(err)
	}
	application.ProjectProvisioner = func(context.Context, string, string) (contracts.ProjectBinding, error) {
		return contracts.ProjectBinding{TeamID: "team", AgentID: "agent-a", UserID: "user"}, nil
	}
	if _, err := application.SetupProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	requests = 0
	mu.Unlock()
	envPath := filepath.Join(root, ".baron", ".env")
	env, err := config.ReadEnvFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	env["BARON_TENCENT_AGENT_ID"] = "agent-b"
	if err := config.WriteEnv(envPath, env); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := application.HandleHook(context.Background(), "codex", "SessionStart", root, bytes.NewBufferString(`{}`), &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["continue"] != true || !strings.Contains(output.String(), "integrity") {
		t.Fatalf("tampered hook was not blocked: %#v", response)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Fatalf("remote service was contacted after integrity failure: %d", requests)
	}
}

func TestMalformedCodexHookFailsOpenWithOfficialContinueResponse(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Project Malformed")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	application.ProjectProvisioner = func(context.Context, string, string) (contracts.ProjectBinding, error) {
		return contracts.ProjectBinding{TeamID: "team", AgentID: "agent", UserID: "user"}, nil
	}
	if _, err := application.SetupProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := application.HandleHook(context.Background(), "codex", "UserPromptSubmit", root, bytes.NewBufferString("{"), &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["continue"] != true {
		t.Fatalf("malformed Codex hook was not fail-open: %s", output.String())
	}
}

func TestKnowledgeSurfaceDiagnosticsSeparateWikiCodeGraphAndTools(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		// Tencent's status route intentionally has a narrow schema and only
		// accepts the resource ID. Other knowledge routes carry full isolation.
		if request.URL.Path == "/v3/code-graph/status" {
			if len(body) != 1 || body["code_graph_id"] != "graph-1" {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
		} else {
			for key, want := range map[string]string{"project_id": "", "team_id": "team-a", "agent_id": "agent-a", "user_id": "user-a"} {
				if key == "project_id" {
					if value, ok := body[key].(string); !ok || !strings.HasPrefix(value, "prj-") {
						writer.WriteHeader(http.StatusBadRequest)
						return
					}
					continue
				}
				if body[key] != want {
					writer.WriteHeader(http.StatusBadRequest)
					return
				}
			}
		}
		if request.URL.Path == "/v3/tools/list" && body["knowledge_id"] != "wiki-1" {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		paths = append(paths, request.URL.Path)
		data := map[string]any{"status": "ready", "id": "wiki-1", "wiki_id": "wiki-1"}
		if request.URL.Path == "/v3/code-graph/status" {
			data = map[string]any{"status": "ready", "code_graph_id": "graph-1"}
		}
		if request.URL.Path == "/v3/tools/list" {
			data = map[string]any{"items": []any{map[string]any{"name": "code_graph_search"}}}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	root := filepath.Join(t.TempDir(), "Project Surfaces")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	application.ProjectProvisioner = func(context.Context, string, string) (contracts.ProjectBinding, error) {
		return contracts.ProjectBinding{TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}, nil
	}
	projectResult, err := application.SetupProject(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	global, err := config.LoadGlobalState(application.GlobalPath)
	if err != nil {
		t.Fatal(err)
	}
	global.Identity.TeamID = "team-a"
	global.Identity.UserID = "user-a"
	global.Identity.KnowledgeEndpoint = server.URL + "/v3"
	if err := config.SaveGlobalState(application.GlobalPath, global); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(projectResult.Root, ".baron", "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertKnowledgeRegistry(context.Background(), storage.KnowledgeRegistry{ProjectID: projectResult.ProjectID, TeamID: "team-a", UserID: "user-a", AgentID: "agent-a", WikiID: "wiki-1", CodeGraphID: "graph-1"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	application.HTTPClient = server.Client()
	checks := application.knowledgeSurfaceChecks(context.Background(), global)
	if len(checks) != 3 {
		t.Fatalf("surface checks=%#v", checks)
	}
	for _, check := range checks {
		if check.Status != doctor.StatusReady {
			t.Fatalf("surface check was not ready: %#v", checks)
		}
	}
	joined := strings.Join(paths, ",")
	for _, want := range []string{"/v3/wiki/get", "/v3/code-graph/status", "/v3/tools/list"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("surface route %q missing: %s", want, joined)
		}
	}
}

func TestUnsupportedKnowledgeSurfaceIsMissingWithoutMutatingRegistry(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	root := filepath.Join(t.TempDir(), "Project Unsupported")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	application.ProjectProvisioner = func(context.Context, string, string) (contracts.ProjectBinding, error) {
		return contracts.ProjectBinding{TeamID: "team-a", AgentID: "agent-a", UserID: "user-a"}, nil
	}
	projectResult, err := application.SetupProject(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	global, err := config.LoadGlobalState(application.GlobalPath)
	if err != nil {
		t.Fatal(err)
	}
	global.Identity.TeamID = "team-a"
	global.Identity.UserID = "user-a"
	global.Identity.KnowledgeEndpoint = server.URL + "/v3"
	if err := config.SaveGlobalState(application.GlobalPath, global); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(projectResult.Root, ".baron", "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	want := storage.KnowledgeRegistry{ProjectID: projectResult.ProjectID, TeamID: "team-a", UserID: "user-a", AgentID: "agent-a", WikiID: "wiki-stable", CodeGraphID: "graph-stable"}
	if err := store.UpsertKnowledgeRegistry(context.Background(), want); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	application.HTTPClient = server.Client()
	checks := application.knowledgeSurfaceChecks(context.Background(), global)
	for _, check := range checks {
		if check.Name == "tencent-wiki" || check.Name == "tencent-codegraph" || check.Name == "tencent-tools" {
			if check.Status != doctor.StatusMissing {
				t.Fatalf("unsupported surface was not classified as missing: %#v", check)
			}
		}
	}
	restoredStore, err := storage.Open(filepath.Join(projectResult.Root, ".baron", "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restoredStore.Close()
	got, err := restoredStore.GetKnowledgeRegistry(context.Background(), projectResult.ProjectID)
	if err != nil || got.WikiID != want.WikiID || got.CodeGraphID != want.CodeGraphID {
		t.Fatalf("unsupported surface check mutated local registry: %#v err=%v", got, err)
	}
}

func TestSetupProjectCreatesAndReusesOneAgentWikiAndCodeGraph(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		projectID, _ := body["project_id"].(string)
		if !strings.HasPrefix(projectID, "prj-") {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		counts[request.URL.Path]++
		agentExists := counts["/v3/meta/agent/create"] > 0
		wikiExists := counts["/v3/wiki/create"] > 0
		graphExists := counts["/v3/code-graph/create"] > 0
		mu.Unlock()
		data := map[string]any{}
		switch request.URL.Path {
		case "/v3/meta/agent/list":
			if agentExists {
				data = map[string]any{"items": []any{map[string]any{"id": "agent-" + projectID, "agent_id": "agent-" + projectID, "description": "Baron project_id=" + projectID}}}
			} else {
				data = map[string]any{"items": []any{}}
			}
		case "/v3/meta/agent/create":
			data = map[string]any{"id": "agent-" + projectID, "agent_id": "agent-" + projectID, "description": "Baron project_id=" + projectID}
		case "/v3/atomic/search":
			data = map[string]any{"items": []any{}}
		case "/v3/wiki/list":
			if wikiExists {
				data = map[string]any{"items": []any{map[string]any{"id": "wiki-" + projectID, "wiki_id": "wiki-" + projectID, "name": "Baron Nexus project wiki " + projectID, "status": "ready"}}}
			} else {
				data = map[string]any{"items": []any{}}
			}
		case "/v3/wiki/create", "/v3/wiki/get":
			data = map[string]any{"id": "wiki-" + projectID, "wiki_id": "wiki-" + projectID, "name": "Baron Nexus project wiki " + projectID, "status": "ready"}
		case "/v3/code-graph/list":
			if graphExists {
				data = map[string]any{"items": []any{map[string]any{"id": "graph-" + projectID, "code_graph_id": "graph-" + projectID, "name": "Baron Nexus project code graph " + projectID, "status": "ready"}}}
			} else {
				data = map[string]any{"items": []any{}}
			}
		case "/v3/code-graph/create", "/v3/code-graph/get":
			data = map[string]any{"id": "graph-" + projectID, "code_graph_id": "graph-" + projectID, "name": "Baron Nexus project code graph " + projectID, "status": "ready"}
		case "/v3/code-graph/status":
			data = map[string]any{"status": "ready", "commit_hash": "abc123"}
		case "/v3/knowledge/create":
			data = map[string]any{"knowledge_id": "metadata-" + projectID}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "ok", "data": data})
	}))
	defer server.Close()
	root := filepath.Join(t.TempDir(), "Project Setup")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", root, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", root, "remote", "add", "origin", "https://example.com/setup.git").Run(); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	application.HTTPClient = server.Client()
	if err := config.SaveGlobalState(application.GlobalPath, config.GlobalState{Identity: contracts.Identity{Endpoint: server.URL, KnowledgeEndpoint: server.URL + "/v3", TeamID: "team-a", UserID: "user-a", UserKey: "sk-user", ServiceID: "baron"}}); err != nil {
		t.Fatal(err)
	}
	var projectResult project.Project
	for rerun := 0; rerun < 5; rerun++ {
		var setupErr error
		projectResult, setupErr = application.SetupProject(context.Background(), root)
		if setupErr != nil {
			t.Fatalf("setup rerun %d failed: %v", rerun, setupErr)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/v3/meta/agent/create", "/v3/wiki/create", "/v3/code-graph/create"} {
		if counts[path] != 1 {
			t.Fatalf("setup duplicated %s: counts=%#v", path, counts)
		}
	}
	store, err := storage.Open(filepath.Join(projectResult.Root, ".baron", "runtime", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry, err := store.GetKnowledgeRegistry(context.Background(), projectResult.ProjectID)
	if err != nil || registry.WikiID != "wiki-"+projectResult.ProjectID || registry.CodeGraphID != "graph-"+projectResult.ProjectID {
		t.Fatalf("setup did not persist one stable knowledge mapping: %#v err=%v", registry, err)
	}
	global, err := config.LoadGlobalState(application.GlobalPath)
	if err != nil || global.ProjectBindings[projectResult.ProjectID].AgentID != "agent-"+projectResult.ProjectID {
		t.Fatalf("setup did not persist one stable agent mapping: %#v err=%v", global.ProjectBindings, err)
	}
}
