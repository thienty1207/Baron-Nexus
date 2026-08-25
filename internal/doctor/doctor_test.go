package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/install"
)

type fakeProbe struct {
	commands map[string]string
	errors   map[string]error
}

func (p fakeProbe) LookPath(name string) (string, error) {
	if value, ok := p.commands[name]; ok {
		return value, nil
	}
	return "", errMissing
}

func (p fakeProbe) Run(_ context.Context, name string, args ...string) (string, error) {
	key := name + " " + strings.Join(args, " ")
	if err, ok := p.errors[key]; ok {
		return "", err
	}
	return p.commands[key], nil
}

func TestReadinessDistinguishesMissingDockerFromStoppedDaemon(t *testing.T) {
	missing := Check(context.Background(), Options{Probe: fakeProbe{commands: map[string]string{}, errors: map[string]error{}}})
	if result := missing.ByName("docker-cli"); result.Status != StatusMissing {
		t.Fatalf("Docker missing status=%s", result.Status)
	}
	probe := fakeProbe{commands: map[string]string{"docker": "/usr/bin/docker"}, errors: map[string]error{"docker info": errStopped}}
	stopped := Check(context.Background(), Options{Probe: probe})
	if result := stopped.ByName("docker-daemon"); result.Status != StatusUnavailable {
		t.Fatalf("Docker daemon status=%s", result.Status)
	}
	if result := stopped.ByName("docker-cli"); result.Status != StatusReady {
		t.Fatalf("Docker CLI status=%s", result.Status)
	}
}

func TestUnauthenticatedCodexNeverReadsOrPrintsAuthContents(t *testing.T) {
	probe := fakeProbe{commands: map[string]string{
		"docker": "/usr/bin/docker", "docker info": "ok", "node": "/usr/bin/node", "node --version": "v22.19.0", "npm": "/usr/bin/npm", "npx": "/usr/bin/npx", "pnpm": "/usr/bin/pnpm", "uv": "/usr/bin/uv", "uvx": "/usr/bin/uvx", "dsh": "/usr/bin/dsh", "dsh --version": "dsh 0.1.0", "codex": "/usr/bin/codex", "codex --version": "codex 1.0.0",
	}, errors: map[string]error{}}
	report := Check(context.Background(), Options{Probe: probe, CodexAuthenticated: false, DSHComponents: map[string]bool{
		"duckduckgo-search": true, "superpowers-dsh": true, "dsh-reverse-skill": true, "baron-dsh-adapter": true,
	}})
	if result := report.ByName("codex-auth"); result.Status != StatusIncomplete {
		t.Fatalf("Codex auth status=%s", result.Status)
	}
	if strings.Contains(report.Human(), "auth.json") || strings.Contains(report.Human(), "sk-") {
		t.Fatalf("readiness output exposed auth material: %s", report.Human())
	}
}

func TestMissingDSHProviderCredentialIsActionableWithoutSecretOutput(t *testing.T) {
	probe := fakeProbe{commands: map[string]string{
		"dsh": "/usr/bin/dsh", "dsh --version": "dsh 0.1.0",
	}, errors: map[string]error{}}
	report := Check(context.Background(), Options{Probe: probe, DSHProviderChecked: true, DSHProviderReady: false})
	result := report.ByName("dsh-credentials")
	if result.Status != StatusIncomplete || !strings.Contains(result.Suggestion, "DEEPSEEK_API_KEY") {
		t.Fatalf("missing DSH credential was not actionable: %#v", result)
	}
	if strings.Contains(report.Human(), "sk-") {
		t.Fatalf("credential diagnostic contained secret-shaped output: %s", report.Human())
	}
}

func TestAllLocalFixtureComponentsGreenHasStableSuccessMessage(t *testing.T) {
	probe := fakeProbe{commands: map[string]string{
		"docker": "/usr/bin/docker", "docker info": "ok", "node": "/usr/bin/node", "node --version": "v22.19.0", "npm": "/usr/bin/npm", "npx": "/usr/bin/npx", "pnpm": "/usr/bin/pnpm", "uv": "/usr/bin/uv", "uvx": "/usr/bin/uvx", "dsh": "/usr/bin/dsh", "dsh --version": "dsh 0.1.0", "codex": "/usr/bin/codex", "codex --version": "codex 1.0.0",
	}, errors: map[string]error{}}
	report := Check(context.Background(), Options{Probe: probe, CodexAuthenticated: true, DSHComponents: map[string]bool{
		"duckduckgo-search": true, "superpowers-dsh": true, "dsh-reverse-skill": true, "baron-dsh-adapter": true,
	}, DSHProviderChecked: true, DSHProviderReady: true, TencentReady: true})
	if !report.Ready || report.ExitCode != 0 {
		t.Fatalf("fixture not ready: %#v", report)
	}
	if !strings.HasSuffix(strings.TrimSpace(report.Human()), "All required components are ready.") {
		t.Fatalf("unexpected success text: %s", report.Human())
	}
}

func TestWeakCredentialPermissionIsReportedWithoutEchoingContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "global.json")
	if err := os.WriteFile(path, []byte(`{"UserKey":"sk-secret"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	report := Check(context.Background(), Options{Probe: fakeProbe{commands: map[string]string{}, errors: map[string]error{}}, CredentialPaths: []string{path}})
	found := false
	for _, check := range report.Checks {
		if strings.HasPrefix(check.Name, "permissions:") {
			found = true
			if check.Status != StatusWarning || strings.Contains(check.Message, "sk-secret") {
				t.Fatalf("unsafe permission diagnostic=%#v", check)
			}
		}
	}
	if !found {
		t.Fatal("weak credential permission was not reported")
	}
}

func TestUnsupportedNodeVersionIsActionable(t *testing.T) {
	probe := fakeProbe{commands: map[string]string{"node": "/usr/bin/node", "node --version": "v20.11.1"}, errors: map[string]error{}}
	report := Check(context.Background(), Options{Probe: probe})
	result := report.ByName("node")
	if result.Status != StatusIncomplete || !strings.Contains(result.Suggestion, "22.19") {
		t.Fatalf("unsupported Node version was not diagnosed: %#v", result)
	}
}

func TestLinuxBootstrapDiagnosticsSeparatePackageManagerAndSudo(t *testing.T) {
	probe := fakeProbe{commands: map[string]string{"docker": "/usr/bin/docker", "apt-get": "/usr/bin/apt-get", "sudo": "/usr/bin/sudo"}, errors: map[string]error{"docker info": errStopped, "sudo -n docker info": errStopped, "sudo -n true": errMissing}}
	report := Check(context.Background(), Options{Probe: probe, LinuxBootstrap: true})
	if result := report.ByName("docker-package-manager"); result.Status != StatusReady {
		t.Fatalf("apt-get diagnostic=%#v", result)
	}
	if result := report.ByName("sudo"); result.Status != StatusIncomplete || !strings.Contains(result.Suggestion, "sudo -v") {
		t.Fatalf("sudo diagnostic=%#v", result)
	}
}

func TestConfiguredCodexHooksRequireExplicitInteractiveApproval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"baron hook codex SessionStart"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The fixture intentionally contains only one event: doctor must report an
	// incomplete configuration instead of claiming that trust was granted.
	probe := fakeProbe{commands: map[string]string{"codex": "/usr/bin/codex", "codex --version": "codex 0.149.0"}, errors: map[string]error{}}
	report := Check(context.Background(), Options{Probe: probe, CodexAuthenticated: true, CodexHooksPath: path})
	result := report.ByName("codex-hooks")
	if result.Status != StatusIncomplete || !strings.Contains(result.Suggestion, "baron codex-cli init") {
		t.Fatalf("incomplete Codex hooks were not diagnosed: %#v", result)
	}
}

func TestConfiguredCodexHooksAreReadyWhenProjectTrustIsPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if err := install.MergeCodexHooks(path, "baron"); err != nil {
		t.Fatal(err)
	}
	probe := fakeProbe{commands: map[string]string{"codex": "/usr/bin/codex", "codex --version": "codex 0.149.0"}, errors: map[string]error{}}
	report := Check(context.Background(), Options{Probe: probe, CodexAuthenticated: true, CodexHooksPath: path, CodexProjectTrusted: true})
	result := report.ByName("codex-hooks")
	if result.Status != StatusReady {
		t.Fatalf("trusted Codex hooks were not accepted: %#v", result)
	}
}

var errMissing = &testError{"missing"}
var errStopped = &testError{"stopped"}

type testError struct{ text string }

func (e *testError) Error() string { return e.text }
