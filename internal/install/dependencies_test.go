package install

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeVersionAcceptsCommandAndReleaseForms(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "release tag", input: "v0.12.6", want: "0.12.6"},
		{name: "npm output", input: "0.12.6\n", want: "0.12.6"},
		{name: "command output", input: "uv 0.12.6 (abcdef)", want: "0.12.6"},
		{name: "codex output", input: "codex-cli 0.150.0", want: "0.150.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeVersion(test.input)
			if err != nil {
				t.Fatalf("NormalizeVersion(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("NormalizeVersion(%q)=%q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestNormalizeVersionRejectsMalformedInput(t *testing.T) {
	for _, input := range []string{"not-a-version", "1.2.3.4", "build1.2.3"} {
		if _, err := NormalizeVersion(input); err == nil {
			t.Fatalf("malformed version %q was accepted", input)
		}
	}
}

func TestEnsureNPMDependencyLatestSkipsEqualVersion(t *testing.T) {
	runner := &commandFixture{
		available: map[string]bool{"npm": true, "dsh": true},
		outputs: map[string]string{
			"dsh --version":                     "dsh 0.2.0\n",
			"npm view @deepseek-ai/dsh version": "0.2.0\n",
		},
	}
	report, err := EnsureNPMDependencyLatest(context.Background(), runner, NPMDependencySpec{
		Name: "DSH", Package: "@deepseek-ai/dsh", Command: "dsh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.State.NeedsUpdate || report.State.Changed || report.State.LocalVersion != "0.2.0" || report.State.LatestVersion != "0.2.0" {
		t.Fatalf("unexpected equal-version report: %#v", report.State)
	}
	for _, call := range runner.calls {
		if strings.Contains(call, "npm install") {
			t.Fatalf("equal dependency was reinstalled: %#v", runner.calls)
		}
	}
}

func TestEnsureNPMDependencyLatestInstallsResolvedVersionWhenStale(t *testing.T) {
	runner := &dependencyInstallFixture{commandFixture: &commandFixture{
		available: map[string]bool{"npm": true, "dsh": true},
		outputs: map[string]string{
			"dsh --version":                     "dsh 0.1.0\n",
			"npm view @deepseek-ai/dsh version": "0.2.0\n",
		},
	}}
	report, err := EnsureNPMDependencyLatest(context.Background(), runner, NPMDependencySpec{
		Name: "DSH", Package: "@deepseek-ai/dsh", Command: "dsh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.State.Changed || report.State.NeedsUpdate || report.State.LocalVersion != "0.2.0" {
		t.Fatalf("unexpected stale-version report: %#v", report.State)
	}
	if !containsCall(runner.calls, "npm install --global @deepseek-ai/dsh@0.2.0") {
		t.Fatalf("resolved version was not installed: %#v", runner.calls)
	}
}

func TestEnsureNPMDependencyLatestFailsWhenRegistryCannotBeQueried(t *testing.T) {
	runner := &failingNPMViewFixture{commandFixture: &commandFixture{
		available: map[string]bool{"npm": true, "dsh": true},
		outputs:   map[string]string{"dsh --version": "dsh 0.2.0\n"},
	}}
	_, err := EnsureNPMDependencyLatest(context.Background(), runner, NPMDependencySpec{
		Name: "DSH", Package: "@deepseek-ai/dsh", Command: "dsh",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "latest") {
		t.Fatalf("registry failure=%v, want actionable latest error", err)
	}
}

func TestEnsureNPMDependencyStateAvoidsDuplicateRegistryQuery(t *testing.T) {
	runner := &commandFixture{available: map[string]bool{"dsh": true}}
	report, err := EnsureNPMDependencyState(context.Background(), runner, NPMDependencySpec{
		Name: "DSH", Package: "@deepseek-ai/dsh", Command: "dsh",
	}, ComponentState{Name: "DSH", Installed: true, LocalVersion: "0.2.0", LatestVersion: "0.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if report.State.Changed || report.State.NeedsUpdate || len(runner.calls) != 0 {
		t.Fatalf("precomputed equal state was not reused: report=%#v calls=%#v", report, runner.calls)
	}
}

func TestEnsureNPMDependencyStateRejectsUnknownLatestVersion(t *testing.T) {
	runner := &commandFixture{available: map[string]bool{"dsh": true}}
	_, err := EnsureNPMDependencyState(context.Background(), runner, NPMDependencySpec{
		Name: "DSH", Package: "@deepseek-ai/dsh", Command: "dsh",
	}, ComponentState{Installed: true, LocalVersion: "0.2.0", LatestVersion: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "latest DSH version is unknown") {
		t.Fatalf("unknown latest version was accepted: %v", err)
	}
}

type dependencyInstallFixture struct {
	*commandFixture
}

func (f *dependencyInstallFixture) Run(ctx context.Context, name string, args ...string) (string, error) {
	output, err := f.commandFixture.Run(ctx, name, args...)
	if err == nil && name == "npm" && len(args) == 3 && args[0] == "install" && args[1] == "--global" {
		f.outputs["dsh --version"] = "dsh 0.2.0\n"
	}
	return output, err
}

type failingNPMViewFixture struct {
	*commandFixture
}

func (f *failingNPMViewFixture) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name == "npm" && len(args) == 3 && args[0] == "view" {
		f.calls = append(f.calls, name+" "+strings.Join(args, " "))
		return "", errors.New("registry unavailable")
	}
	return f.commandFixture.Run(ctx, name, args...)
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}
