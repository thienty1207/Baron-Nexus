package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baron-shared-brain/baron/internal/managedruntime"
)

type strixRunnerFixture struct {
	available map[string]bool
	outputs   map[string]string
	calls     []string
}

func (f *strixRunnerFixture) LookPath(name string) (string, error) {
	if f.available[name] {
		return "/managed/" + name, nil
	}
	return "", errors.New("command unavailable")
}

func (f *strixRunnerFixture) Run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if !f.available[name] {
		return "", errors.New("command unavailable")
	}
	return f.outputs[call], nil
}

func TestProbeStrixRuntimeChecksAbsoluteCLIAndHelp(t *testing.T) {
	runner := &strixRunnerFixture{
		available: map[string]bool{"/managed/strix": true},
		outputs:   map[string]string{"/managed/strix --version": "strix 0.4.0", "/managed/strix --help": "usage: strix"},
	}
	runtime := StrixRuntime{StrixPath: "/managed/strix", Runner: runner}
	if err := ProbeStrixRuntime(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 || runner.calls[0] != "/managed/strix --version" || runner.calls[1] != "/managed/strix --help" {
		t.Fatalf("Strix probe calls=%#v", runner.calls)
	}
}

func TestEnsureStrixRuntimeRejectsMissingDependencyGeneration(t *testing.T) {
	paths, err := managedruntime.ResolvePaths(filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	plan := managedruntime.ResolutionPlan{
		ID: "plan-strix", CreatedAt: testRuntimeTime(), Platform: "windows", Architecture: "amd64",
		Components: []managedruntime.ComponentPlan{
			{ID: managedruntime.ComponentStrix, Version: "0.4.0", Source: "fixture", Platform: "windows", Architecture: "amd64", Dependencies: []managedruntime.ComponentID{managedruntime.ComponentPython}},
		},
	}
	if _, err := EnsureStrixRuntime(context.Background(), ManagedRuntimeInput{Paths: paths, Plan: plan, Generation: "generation-1"}); err == nil || !strings.Contains(err.Error(), "uv") {
		t.Fatalf("missing Strix dependency was not rejected: %v", err)
	}
}

func TestEnsureStrixRuntimeResolvesManagedExecutablePaths(t *testing.T) {
	paths, err := managedruntime.ResolvePaths(filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := paths.Generation("generation-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id   managedruntime.ComponentID
		name string
	}{
		{managedruntime.ComponentUV, "uv"},
		{managedruntime.ComponentPython, "python"},
		{managedruntime.ComponentStrix, "strix"},
	} {
		root := filepath.Join(generation, string(item.id))
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, item.name)
		if err := os.WriteFile(path, []byte("managed executable"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	plan := managedruntime.ResolutionPlan{
		ID: "plan-strix", CreatedAt: testRuntimeTime(), Platform: "windows", Architecture: "amd64",
		Components: []managedruntime.ComponentPlan{
			{ID: managedruntime.ComponentUV, Version: "0.9.0", Source: "fixture", Platform: "windows", Architecture: "amd64"},
			{ID: managedruntime.ComponentPython, Version: "3.13.0", Source: "fixture", Platform: "windows", Architecture: "amd64"},
			{ID: managedruntime.ComponentStrix, Version: "0.4.0", Source: "fixture", Platform: "windows", Architecture: "amd64"},
		},
	}
	result, err := EnsureStrixRuntime(context.Background(), ManagedRuntimeInput{Paths: paths, Plan: plan, Generation: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		got  string
		want string
	}{
		{result.UVPath, filepath.Join(generation, "uv", "uv")},
		{result.PythonPath, filepath.Join(generation, "python", "python")},
		{result.StrixPath, filepath.Join(generation, "strix", "strix")},
	} {
		if item.got != item.want {
			t.Fatalf("managed executable path=%q, want %q", item.got, item.want)
		}
	}
}

func TestEnsureStrixRuntimeUsesCatalogEntryPoint(t *testing.T) {
	paths, err := managedruntime.ResolvePaths(filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := paths.Generation("generation-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id   managedruntime.ComponentID
		name string
	}{
		{managedruntime.ComponentUV, "uv"},
		{managedruntime.ComponentPython, "python"},
		{managedruntime.ComponentStrix, "strix-custom"},
	} {
		root := filepath.Join(generation, string(item.id))
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, item.name), []byte("managed executable"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	plan := managedruntime.ResolutionPlan{
		ID: "plan-strix-entry-point", CreatedAt: testRuntimeTime(), Platform: "windows", Architecture: "amd64",
		Components: []managedruntime.ComponentPlan{
			{ID: managedruntime.ComponentUV, Version: "0.9.0", Source: "fixture", Platform: "windows", Architecture: "amd64"},
			{ID: managedruntime.ComponentPython, Version: "3.13.0", Source: "fixture", Platform: "windows", Architecture: "amd64"},
			{ID: managedruntime.ComponentStrix, Version: "0.4.0", Source: "fixture", EntryPoint: "strix-custom", Platform: "windows", Architecture: "amd64"},
		},
	}
	result, err := EnsureStrixRuntime(context.Background(), ManagedRuntimeInput{Paths: paths, Plan: plan, Generation: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(generation, "strix", "strix-custom")
	if result.StrixPath != want {
		t.Fatalf("Strix entry point=%q, want %q", result.StrixPath, want)
	}
}

func testRuntimeTime() (result time.Time) { return time.Unix(1, 0).UTC() }
