package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestRunBootstrapExecutesOneTimeSetupInOrder(t *testing.T) {
	var got []string
	steps := BootstrapSteps{
		Preflight: func(context.Context) error { got = append(got, "preflight"); return nil },
		DSH:       func() error { got = append(got, "dsh"); return nil },
		Codex:     func() error { got = append(got, "codex"); return nil },
		Tencent:   func(context.Context) error { got = append(got, "tencent"); return nil },
		Setup:     func(context.Context) error { got = append(got, "setup"); return nil },
	}
	if err := runBootstrap(context.Background(), steps); err != nil {
		t.Fatal(err)
	}
	want := []string{"preflight", "dsh", "codex", "tencent", "setup"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("steps=%v want=%v", got, want)
	}
}

func TestBootstrapCompletionMessageOnlyRequestsMissingCodexAuth(t *testing.T) {
	t.Setenv("BARON_CODEX_AUTH_READY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	if message := bootstrapCompletionMessage("Baron release ready."); !strings.Contains(message, "ACTION REQUIRED") {
		t.Fatalf("missing Codex auth should be actionable: %q", message)
	}

	t.Setenv("BARON_CODEX_AUTH_READY", "1")
	if message := bootstrapCompletionMessage("Baron release ready."); strings.Contains(message, "ACTION REQUIRED") {
		t.Fatalf("authenticated Codex should not be asked to sign in again: %q", message)
	}
}
