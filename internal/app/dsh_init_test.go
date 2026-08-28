package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/install"
)

func TestDSHInitSkipsStartupProbeWhenManagedStateIsUnchanged(t *testing.T) {
	t.Setenv("DSH_HOME", t.TempDir())
	t.Setenv("DEEPSEEK_API_KEY", "sk-test-dsh-key")
	runner := &dshInitCommandFixture{}
	application := New()
	application.GlobalPath = t.TempDir() + "/global.json"
	application.CommandRunner = runner
	application.ValidateProviderCredential = func(context.Context, string, string) error { return nil }

	if err := application.DSHInit(); err != nil {
		t.Fatal(err)
	}
	if runner.probeCalls != 1 {
		t.Fatalf("first DSH init probe calls=%d, want 1; calls=%#v", runner.probeCalls, runner.calls)
	}
	firstCallCount := len(runner.calls)
	if err := application.DSHInit(); err != nil {
		t.Fatal(err)
	}
	if runner.probeCalls != 1 {
		t.Fatalf("unchanged DSH init reran startup probe: calls=%d; calls=%#v", runner.probeCalls, runner.calls)
	}
	if len(runner.calls) <= firstCallCount {
		t.Fatal("second DSH init did not perform live verification")
	}
}

type dshInitCommandFixture struct {
	calls      []string
	probeCalls int
}

func (f *dshInitCommandFixture) LookPath(name string) (string, error) {
	if name == "npm" || name == "dsh" || name == "pnpm" || name == "uvx" {
		return "/fake/" + name, nil
	}
	return "", errors.New("missing command")
}

func (f *dshInitCommandFixture) Run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	switch {
	case call == "dsh --version":
		return "dsh 0.2.0", nil
	case call == "npm view @deepseek-ai/dsh version":
		return "0.2.0", nil
	case strings.HasPrefix(call, "dsh --profile ") && strings.HasSuffix(call, " --dump-config"):
		return "superpowers-dsh\ndsh-reverse-skill\ndsh-mcp-client\nbaron-dsh-adapter\nbaron-ddg-search\nddg-search\n", nil
	case call == "dsh web --no-open":
		f.probeCalls++
		return "", nil
	default:
		return "", nil
	}
}

var _ install.CommandRunner = (*dshInitCommandFixture)(nil)
