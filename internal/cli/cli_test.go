package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestCLIExposesFrozenCommandSurface(t *testing.T) {
	var out bytes.Buffer
	cmd := New(Options{Out: &out, Err: &out})
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"deepseek-harness", "codex-cli", "tencent-memory", "test", "setup",
		"status", "doctor", "repair", "backup", "restore", "install", "update",
	} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("help missing %q:\n%s", text, out.String())
		}
	}
}

func TestVersionFlagUsesBaronFormat(t *testing.T) {
	var out bytes.Buffer
	cmd := New(Options{Version: "0.1.0", Out: &out, Err: &out})
	cmd.SetArgs([]string{"--version"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "baron 0.1.0" {
		t.Fatalf("version output=%q", got)
	}
}

func TestInstallAndUpdateCommandsInvokeDedicatedHandlers(t *testing.T) {
	var out bytes.Buffer
	installCalled, updateCalled := false, false
	options := Options{
		Version: "0.1.0", Out: &out, Err: &out,
		Install: func() (string, error) { installCalled = true; return "install ok", nil },
		Update:  func() (string, error) { updateCalled = true; return "update ok", nil },
	}
	if code := Run([]string{"install"}, options); code != ExitSuccess || !installCalled {
		t.Fatalf("install code=%d called=%v output=%s", code, installCalled, out.String())
	}
	if code := Run([]string{"update"}, options); code != ExitSuccess || !updateCalled {
		t.Fatalf("update code=%d called=%v output=%s", code, updateCalled, out.String())
	}
}

func TestInstallAndUpdatePropagateHandlerExitErrors(t *testing.T) {
	var out bytes.Buffer
	want := &ExitError{Code: ExitIntegrityFailure, Err: errors.New("release checksum mismatch")}
	if code := Run([]string{"install"}, Options{
		Out:     &out,
		Err:     &out,
		Install: func() (string, error) { return "", want },
	}); code != ExitIntegrityFailure {
		t.Fatalf("install error code=%d output=%s", code, out.String())
	}
	out.Reset()
	if code := Run([]string{"update"}, Options{
		Out:    &out,
		Err:    &out,
		Update: func() (string, error) { return "", want },
	}); code != ExitIntegrityFailure {
		t.Fatalf("update error code=%d output=%s", code, out.String())
	}
}

func TestRunInvalidSetupArgumentsReturnsUsageExitCode(t *testing.T) {
	var out bytes.Buffer
	if got := Run([]string{"setup", "one", "two"}, Options{Out: &out, Err: &out}); got != ExitUsage {
		t.Fatalf("expected usage exit %d, got %d (%s)", ExitUsage, got, out.String())
	}
}

func TestExplicitSetupPathMustBeAbsolute(t *testing.T) {
	var out bytes.Buffer
	if code := Run([]string{"setup", "relative-project"}, Options{Out: &out, Err: &out}); code != ExitUsage {
		t.Fatalf("relative setup path returned %d: %s", code, out.String())
	}
}

func TestSetupPreservesUnicodeAndSpacesInExplicitPath(t *testing.T) {
	var got string
	var out bytes.Buffer
	want := "/tmp/Project A - Tiếng Việt"
	code := Run([]string{"setup", want}, Options{
		Out: &out,
		Err: &out,
		Setup: func(path string) error {
			got = path
			return nil
		},
	})
	if code != ExitSuccess {
		t.Fatalf("setup failed with %d: %s", code, out.String())
	}
	if got != want {
		t.Fatalf("path was changed before handler: got %q want %q", got, want)
	}
}

func TestJSONFlagIsAcceptedForReadinessCommands(t *testing.T) {
	var out bytes.Buffer
	code := Run([]string{"test", "--json"}, Options{
		Out:  &out,
		Err:  &out,
		Test: func(bool) error { return nil },
	})
	if code != ExitSuccess {
		t.Fatalf("JSON readiness command failed: %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "\"status\"") {
		t.Fatalf("JSON output missing status: %s", out.String())
	}
}

func TestReadinessDiagnosticsArePrintedBeforeNonZeroExit(t *testing.T) {
	var out bytes.Buffer
	code := Run([]string{"test"}, Options{
		Out: &out, Err: &out,
		TestOutput: func(bool) (string, error) {
			return "diagnostic\n", &ExitError{Code: ExitMissingDependency, Err: errors.New("missing dependency")}
		},
	})
	if code != ExitMissingDependency || !strings.Contains(out.String(), "diagnostic") {
		t.Fatalf("diagnostic output lost on failure: code=%d output=%s", code, out.String())
	}
}

func TestInitPrintsActionRequiredNoticeWithoutCredentials(t *testing.T) {
	var out bytes.Buffer
	code := Run([]string{"deepseek-harness", "init"}, Options{
		Out: &out,
		Err: &out,
		Init: map[string]func() error{
			"deepseek-harness": func() error { return nil },
		},
		InitNotice: map[string]string{
			"deepseek-harness": "configure the supported DSH credential flow",
		},
	})
	if code != ExitSuccess {
		t.Fatalf("init failed with %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "ACTION REQUIRED: configure the supported DSH credential flow") {
		t.Fatalf("action-required notice missing: %s", out.String())
	}
}
