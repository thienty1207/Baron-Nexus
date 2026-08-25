package app

import (
	"context"
	"reflect"
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
