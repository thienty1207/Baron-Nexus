package managedruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeProbeRejectsComponentWithoutExecutable(t *testing.T) {
	root := t.TempDir()
	if err := (NativeProbe{}).Verify(context.Background(), ComponentPlan{ID: ComponentUV, Version: "1.0.0"}, root); err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("empty component probe error=%v", err)
	}
}

func TestFindExecutableNamedResolvesNestedOfficialArchiveLayout(t *testing.T) {
	root := t.TempDir()
	noise := filepath.Join(root, "go", "api")
	if err := os.MkdirAll(noise, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= defaultProbeEntries; index++ {
		path := filepath.Join(noise, fmt.Sprintf("file-%05d.txt", index))
		if err := os.WriteFile(path, []byte("noise"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	executable := filepath.Join(root, "go", "bin", "go")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := FindExecutableNamed(root, "go")
	if err != nil || resolved != executable {
		t.Fatalf("nested official archive executable=%q err=%v, want %q", resolved, err, executable)
	}
}

func TestFindExecutableNamedAcceptsContainedPackageShim(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "package", "dsh")
	shim := filepath.Join(root, "node_modules", ".bin", "dsh")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "package", "dsh"), shim); err != nil {
		t.Skipf("contained symlink fixture is unavailable: %v", err)
	}
	resolved, err := FindExecutableNamed(root, "dsh")
	if err != nil || resolved != shim {
		t.Fatalf("contained package shim was not resolved: path=%q err=%v", resolved, err)
	}
}

func TestFindExecutableNamedResolvesPackageBinBeforeEntryLimit(t *testing.T) {
	root := t.TempDir()
	shim := filepath.Join(root, "node_modules", ".bin", "dsh")
	if err := os.MkdirAll(filepath.Dir(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	noise := filepath.Join(root, "node_modules", ".pnpm")
	if err := os.MkdirAll(noise, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= defaultProbeEntries; index++ {
		path := filepath.Join(noise, fmt.Sprintf("file-%05d.txt", index))
		if err := os.WriteFile(path, []byte("noise"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := FindExecutableNamed(root, "dsh")
	if err != nil || resolved != shim {
		t.Fatalf("package bin executable=%q err=%v, want %q", resolved, err, shim)
	}
}

func TestFindExecutableNamedExpandsWindowsCommandShim(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows command shims are only resolved on Windows")
	}
	root := t.TempDir()
	path := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(path, []byte("@echo dsh 1.2.3\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := FindExecutableNamed(root, "dsh")
	if err != nil || resolved != path {
		t.Fatalf("Windows command shim was not resolved: path=%q err=%v", resolved, err)
	}
}

func TestFindExecutableNamedPrefersWindowsCommandShimOverUnixScript(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows executable selection is only resolved on Windows")
	}
	root := t.TempDir()
	unixScript := filepath.Join(root, "npm")
	commandShim := filepath.Join(root, "npm.cmd")
	if err := os.WriteFile(unixScript, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(commandShim, []byte("@echo npm 12.0.2\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := FindExecutableNamed(root, "npm")
	if err != nil || resolved != commandShim {
		t.Fatalf("Windows command shim selection=%q err=%v, want %q", resolved, err, commandShim)
	}
}

func TestNativeProbeRequiresExpectedVersion(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "dsh")
	contents := "#!/bin/sh\nprintf 'dsh 1.2.2\\n'\n"
	if runtime.GOOS == "windows" {
		executable += ".cmd"
		contents = "@echo dsh 1.2.2\r\n"
	}
	if err := os.WriteFile(executable, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}

	err := (NativeProbe{}).Verify(context.Background(), ComponentPlan{
		ID: ComponentDSH, Version: "1.2.3",
	}, root)
	if err == nil || !strings.Contains(err.Error(), "version mismatch") {
		t.Fatalf("probe accepted an unexpected runtime version: %v", err)
	}
}

func TestNativeProbeIncludesBoundedCommandOutputOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix fixture uses a shell executable")
	}
	root := t.TempDir()
	executable := filepath.Join(root, "dsh")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'upstream probe failure\\n'\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := (NativeProbe{}).Verify(context.Background(), ComponentPlan{ID: ComponentDSH, Version: "1.2.3"}, root)
	if err == nil || !strings.Contains(err.Error(), "upstream probe failure") {
		t.Fatalf("probe error omitted command output: %v", err)
	}
}

func TestNativeProbeUsesCatalogEntryPoint(t *testing.T) {
	root := t.TempDir()
	name := "custom-runtime"
	contents := "#!/bin/sh\nprintf 'custom-runtime 1.2.3\\n'\n"
	if runtime.GOOS == "windows" {
		name += ".cmd"
		contents = "@echo custom-runtime 1.2.3\r\n"
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (NativeProbe{}).Verify(context.Background(), ComponentPlan{
		ID: ComponentDSH, Version: "1.2.3", EntryPoint: "custom-runtime",
	}, root); err != nil {
		t.Fatalf("probe ignored catalog entry point: %v", err)
	}
}

func TestNativeProbeRunsWindowsBatchEntryPoint(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows batch shims are only probed on Windows")
	}
	root := t.TempDir()
	path := filepath.Join(root, "custom-runtime.bat")
	if err := os.WriteFile(path, []byte("@echo custom-runtime 1.2.3\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (NativeProbe{}).Verify(context.Background(), ComponentPlan{
		ID: ComponentDSH, Version: "1.2.3", EntryPoint: "custom-runtime",
	}, root); err != nil {
		t.Fatalf("probe did not run a Windows batch entry point: %v", err)
	}
}

func TestNativeProbeRunsJavaScriptEntryPointWithManagedNode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix fixture uses executable JavaScript and shell files")
	}
	generation := t.TempDir()
	componentRoot := filepath.Join(generation, string(ComponentPNPM))
	if err := os.MkdirAll(componentRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	entryPoint := filepath.Join(componentRoot, "pnpm.mjs")
	if err := os.WriteFile(entryPoint, []byte("console.log('ignored')\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	nodeRoot := filepath.Join(generation, string(ComponentNode), "bin")
	if err := os.MkdirAll(nodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(nodeRoot, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\nprintf 'pnpm 11.25.0\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (NativeProbe{}).Verify(context.Background(), ComponentPlan{ID: ComponentPNPM, Version: "11.25.0", EntryPoint: "pnpm.mjs"}, componentRoot); err != nil {
		t.Fatalf("JavaScript entry point was not run through managed Node: %v", err)
	}
}

func TestVersionInOutputRequiresAnExactSemanticVersion(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected string
		want     bool
	}{
		{name: "go prefix", output: "go version go1.27.0 windows/amd64", expected: "1.27.0", want: true},
		{name: "node v prefix", output: "v22.14.0", expected: "22.14.0", want: true},
		{name: "longer patch is rejected", output: "tool 1.2.30", expected: "1.2.3", want: false},
		{name: "release candidate is not stable", output: "tool 1.2.3-rc.1", expected: "1.2.3", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := versionInOutput([]byte(test.output), test.expected); got != test.want {
				t.Fatalf("versionInOutput(%q, %q)=%v, want %v", test.output, test.expected, got, test.want)
			}
		})
	}
}
