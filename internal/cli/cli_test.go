package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/version"
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
		"status", "timeline", "doctor", "repair", "backup", "restore", "install", "update",
		"credentials", "deepseek", "permissions", "uninstall",
	} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("help missing %q:\n%s", text, out.String())
		}
	}
	out.Reset()
	nested := New(Options{Out: &out, Err: &out})
	nested.SetArgs([]string{"deepseek", "--help"})
	if err := nested.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "api_key") {
		t.Fatalf("DeepSeek help missing api_key:\n%s", out.String())
	}
}

func TestInitUsesConfiguredLoadingRunner(t *testing.T) {
	var out bytes.Buffer
	called := false
	label := ""
	code := Run([]string{"deepseek-harness", "init"}, Options{
		Out: &out,
		Err: &out,
		Init: map[string]func() error{
			"deepseek-harness": func() error { called = true; return nil },
		},
		RunWithLoading: func(gotLabel string, action func() error) error {
			label = gotLabel
			return action()
		},
	})
	if code != ExitSuccess || !called || label != "Initializing deepseek-harness" {
		t.Fatalf("init code=%d called=%v label=%q output=%s", code, called, label, out.String())
	}
}

func TestUninstallConfirmationAcceptsYOrEmptyAndRejectsOtherAnswers(t *testing.T) {
	for _, input := range []string{"", "\n", "y\n", "Y\n"} {
		var out bytes.Buffer
		called := false
		code := Run([]string{"uninstall"}, Options{
			In:            strings.NewReader(input),
			Out:           &out,
			Err:           &out,
			UninstallPlan: func(bool) (string, error) { return "Would remove Baron resources.", nil },
			Uninstall:     func(bool) (string, error) { called = true; return "removed", nil },
		})
		if code != ExitSuccess || !called || !strings.Contains(out.String(), "removed") {
			t.Fatalf("accepted uninstall input=%q code=%d called=%v output=%s", input, code, called, out.String())
		}
		if !strings.Contains(out.String(), "Continue uninstall? [Y/n]:") {
			t.Fatalf("uninstall prompt missing for input=%q: %s", input, out.String())
		}
	}

	for _, input := range []string{"n\n", "N\n"} {
		var out bytes.Buffer
		called := false
		code := Run([]string{"uninstall"}, Options{
			In:            strings.NewReader(input),
			Out:           &out,
			Err:           &out,
			UninstallPlan: func(bool) (string, error) { return "Would remove Baron resources.", nil },
			Uninstall:     func(bool) (string, error) { called = true; return "removed", nil },
		})
		if code != ExitSuccess || called || !strings.Contains(out.String(), "cancelled") {
			t.Fatalf("cancelled uninstall input=%q code=%d called=%v output=%s", input, code, called, out.String())
		}
	}

	for _, input := range []string{"UNINSTALL BARON\n", "yes\n", "cancel\n"} {
		var out bytes.Buffer
		called := false
		code := Run([]string{"uninstall"}, Options{
			In:            strings.NewReader(input),
			Out:           &out,
			Err:           &out,
			UninstallPlan: func(bool) (string, error) { return "Would remove Baron resources.", nil },
			Uninstall:     func(bool) (string, error) { called = true; return "removed", nil },
		})
		if code != ExitUsage || called {
			t.Fatalf("invalid uninstall input=%q code=%d called=%v output=%s", input, code, called, out.String())
		}
	}
}

func TestUninstallDefaultsToFullPurge(t *testing.T) {
	called := false
	purgeAll := false
	code := Run([]string{"uninstall", "--yes"}, Options{
		Uninstall: func(value bool) (string, error) {
			called = true
			purgeAll = value
			return "removed", nil
		},
	})
	if code != ExitSuccess || !called || !purgeAll {
		t.Fatalf("uninstall default purge=%v code=%d called=%v", purgeAll, code, called)
	}
}

func TestPermissionsCommandsInvokeDedicatedHandlers(t *testing.T) {
	var out bytes.Buffer
	called := ""
	options := Options{
		Out:                &out,
		Err:                &out,
		PermissionsEnable:  func() (string, error) { called = "enable"; return "enabled", nil },
		PermissionsDisable: func() (string, error) { called = "disable"; return "disabled", nil },
		PermissionsStatus:  func() (string, error) { called = "status"; return "status", nil },
	}
	for _, command := range []string{"enable", "disable", "status"} {
		called = ""
		if code := Run([]string{"permissions", command}, options); code != ExitSuccess || called != command {
			t.Fatalf("permissions %s code=%d called=%q output=%s", command, code, called, out.String())
		}
	}
}

func TestVersionFlagUsesBaronFormat(t *testing.T) {
	var out bytes.Buffer
	cmd := New(Options{Version: "0.1.8", Out: &out, Err: &out})
	cmd.SetArgs([]string{"--version"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "baron 0.1.8" {
		t.Fatalf("version output=%q", got)
	}
}

func TestDefaultVersionIsCurrentBaronRelease(t *testing.T) {
	if version.Value != "0.1.16" {
		t.Fatalf("default version=%q, want 0.1.16", version.Value)
	}
}

func TestInstallAndUpdateCommandsInvokeDedicatedHandlers(t *testing.T) {
	var out bytes.Buffer
	installCalled, updateCalled := false, false
	options := Options{
		Version: "0.1.8", Out: &out, Err: &out,
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

func TestCredentialsSetDeepseekInvokesRotationHandler(t *testing.T) {
	var out bytes.Buffer
	called := ""
	if code := Run([]string{"credentials", "set", "deepseek"}, Options{
		Out: &out,
		Err: &out,
		SetCredential: func(provider string) error {
			called = provider
			return nil
		},
	}); code != ExitSuccess || called != "deepseek" {
		t.Fatalf("code=%d provider=%q output=%s", code, called, out.String())
	}
	if !strings.Contains(out.String(), "deepseek") {
		t.Fatalf("rotation completion output missing provider: %s", out.String())
	}
}

func TestDeepseekAPIKeyCommandInvokesRotationHandler(t *testing.T) {
	var out bytes.Buffer
	called := ""
	if code := Run([]string{"deepseek", "api_key"}, Options{
		Out: &out,
		Err: &out,
		SetCredential: func(provider string) error {
			called = provider
			return nil
		},
	}); code != ExitSuccess || called != "deepseek" {
		t.Fatalf("code=%d provider=%q output=%s", code, called, out.String())
	}
	if !strings.Contains(out.String(), "deepseek") {
		t.Fatalf("DeepSeek API-key completion output missing provider: %s", out.String())
	}
}

func TestCredentialsSetRejectsUnsupportedProvider(t *testing.T) {
	var out bytes.Buffer
	if code := Run([]string{"credentials", "set", "openai"}, Options{Out: &out, Err: &out}); code != ExitUnsupportedUpstream {
		t.Fatalf("unsupported provider code=%d output=%s", code, out.String())
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

func TestTimelineCommandPassesBoundedLocalOutputOptions(t *testing.T) {
	var out bytes.Buffer
	called := false
	code := Run([]string{"timeline", "--limit", "7", "--json"}, Options{
		Out: &out,
		Err: &out,
		TimelineOutput: func(limit int, jsonOutput bool) (string, error) {
			called = true
			if limit != 7 || !jsonOutput {
				t.Fatalf("timeline options limit=%d json=%v", limit, jsonOutput)
			}
			return "{\"events\":[]}\n", nil
		},
	})
	if code != ExitSuccess || !called || out.String() != "{\"events\":[]}\n" {
		t.Fatalf("timeline code=%d called=%v output=%q", code, called, out.String())
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
