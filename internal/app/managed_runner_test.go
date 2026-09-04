package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/install"
	"github.com/baron-shared-brain/baron/internal/managedruntime"
)

type managedRunnerFixture struct {
	calls []string
}

func (f *managedRunnerFixture) LookPath(name string) (string, error) {
	return name, nil
}

func (f *managedRunnerFixture) Run(_ context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return "ok", nil
}

type isolatedManagedRunnerFixture struct {
	managedRunnerFixture
	environment map[string]string
	workDir     string
}

func (f *isolatedManagedRunnerFixture) RunWithEnvironment(_ context.Context, environment map[string]string, name string, args ...string) (string, error) {
	f.environment = environment
	return f.Run(context.Background(), name, args...)
}

func (f *isolatedManagedRunnerFixture) RunWithEnvironmentInDir(_ context.Context, environment map[string]string, workDir, name string, args ...string) (string, error) {
	f.environment = environment
	f.workDir = workDir
	return f.Run(context.Background(), name, args...)
}

func TestManagedCommandRunnerResolvesAgentBinaryInsideActiveGeneration(t *testing.T) {
	paths, err := managedruntime.ResolvePaths(filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := paths.Generation("generation-1")
	if err != nil {
		t.Fatal(err)
	}
	dshPath := filepath.Join(generation, "dsh", "bin", "dsh")
	if err := os.MkdirAll(filepath.Dir(dshPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dshPath, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := &managedRunnerFixture{}
	runner, err := newManagedCommandRunner(fixture, config.ManagedRuntimeState{Root: paths.Root, CurrentGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := runner.LookPath("dsh")
	if err != nil || resolved != dshPath {
		t.Fatalf("managed dsh path=%q err=%v", resolved, err)
	}
	if _, err := runner.Run(context.Background(), "dsh", "--version"); err != nil {
		t.Fatal(err)
	}
	if len(fixture.calls) != 1 || !strings.HasPrefix(fixture.calls[0], dshPath+" ") {
		t.Fatalf("managed runner used unexpected command: %#v", fixture.calls)
	}
}

func TestManagedCommandRunnerRejectsMissingGeneration(t *testing.T) {
	fixture := &managedRunnerFixture{}
	if _, err := newManagedCommandRunner(fixture, config.ManagedRuntimeState{Root: filepath.Join(t.TempDir(), "runtime"), CurrentGeneration: "missing"}); err == nil || !errors.Is(err, errManagedRuntimeExecutable) {
		t.Fatalf("missing generation error=%v", err)
	}
}

func TestManagedCommandRunnerExposesIsolatedEnvironmentBoundaries(t *testing.T) {
	paths, err := managedruntime.ResolvePaths(filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := paths.Generation("generation-1")
	if err != nil {
		t.Fatal(err)
	}
	dshPath := filepath.Join(generation, "dsh", "bin", "dsh")
	if err := os.MkdirAll(filepath.Dir(dshPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dshPath, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := &isolatedManagedRunnerFixture{}
	runner, err := newManagedCommandRunner(fixture, config.ManagedRuntimeState{Root: paths.Root, CurrentGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	isolated, ok := runner.(install.WorkingDirectoryEnvironmentCommandRunner)
	if !ok {
		t.Fatal("managed runner does not expose the working-directory environment boundary")
	}
	workDir := filepath.Join(t.TempDir(), "job")
	if _, err := isolated.RunWithEnvironmentInDir(context.Background(), map[string]string{"LLM_API_KEY": "secret"}, workDir, "dsh", "--help"); err != nil {
		t.Fatal(err)
	}
	if fixture.workDir != workDir || fixture.environment["LLM_API_KEY"] != "secret" || fixture.environment["PATH"] == "" {
		t.Fatalf("managed isolated invocation=%#v", fixture)
	}
	if !strings.Contains(fixture.environment["PATH"], filepath.Dir(dshPath)) {
		t.Fatalf("managed PATH=%q does not include the resolved executable directory", fixture.environment["PATH"])
	}
}

var _ install.CommandRunner = (*managedRunnerFixture)(nil)
