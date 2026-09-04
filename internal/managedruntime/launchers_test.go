package managedruntime

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func launcherFixturePaths(t *testing.T) Paths {
	t.Helper()
	paths, err := ResolvePaths(filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Bin, 0o700); err != nil {
		t.Fatal(err)
	}
	return paths
}

func launcherTarget(t *testing.T, paths Paths, name string) string {
	t.Helper()
	target := filepath.Join(paths.Generations, "generation-1", "bin", name)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return target
}

func launcherFile(t *testing.T, paths Paths, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return filepath.Join(paths.Bin, name+".cmd")
	}
	return filepath.Join(paths.Bin, name)
}

func TestInstallLaunchersUsesVerifiedGenerationTargets(t *testing.T) {
	paths := launcherFixturePaths(t)
	target := launcherTarget(t, paths, "dsh")
	report, err := InstallLaunchers(paths, []LauncherSpec{{Name: "dsh", Target: target}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Launchers) != 1 || report.Launchers[0].Path != launcherFile(t, paths, "dsh") {
		t.Fatalf("launcher report=%#v", report)
	}
	data, err := os.ReadFile(launcherFile(t, paths, "dsh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), LauncherMarker) || !strings.Contains(string(data), target) {
		t.Fatalf("launcher does not contain managed marker/target: %s", data)
	}
}

func TestInstallLaunchersInjectsManagedClientIdentity(t *testing.T) {
	paths := launcherFixturePaths(t)
	target := launcherTarget(t, paths, "dsh")
	if _, err := InstallLaunchers(paths, []LauncherSpec{{Name: "dsh", Target: target, ClientIdentity: "dsh"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(launcherFile(t, paths, "dsh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "BARON_CLIENT") || !strings.Contains(string(data), "dsh") {
		t.Fatalf("launcher does not inject managed client identity: %s", data)
	}
}

func TestInstallLaunchersBindsManagedRuntimePath(t *testing.T) {
	paths := launcherFixturePaths(t)
	target := launcherTarget(t, paths, "dsh")
	managedNodeDirectory := filepath.Join(paths.Generations, "generation-1", "node", "bin")
	if err := os.MkdirAll(managedNodeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallLaunchers(paths, []LauncherSpec{
		{Name: "dsh", Target: target, ClientIdentity: "dsh", ManagedPath: []string{managedNodeDirectory}},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(launcherFile(t, paths, "dsh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), managedNodeDirectory) || !strings.Contains(string(data), "PATH") {
		t.Fatalf("launcher does not bind managed runtime PATH: %s", data)
	}
}

func TestInstallLaunchersUsesConfiguredDiscoverableDirectory(t *testing.T) {
	paths := launcherFixturePaths(t)
	paths.LauncherDirectory = filepath.Join(t.TempDir(), "baron-bin")
	target := launcherTarget(t, paths, "dsh")
	if _, err := InstallLaunchers(paths, []LauncherSpec{{Name: "dsh", Target: target}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(paths.LauncherDirectory, launcherFilename("dsh"))); err != nil {
		t.Fatalf("configured launcher directory was not used: %v", err)
	}
}

func TestInstallLaunchersPreservesUserCollision(t *testing.T) {
	paths := launcherFixturePaths(t)
	target := launcherTarget(t, paths, "dsh")
	bare := launcherFile(t, paths, "dsh")
	if err := os.WriteFile(bare, []byte("user launcher\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	report, err := InstallLaunchers(paths, []LauncherSpec{{Name: "dsh", Target: target}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Collisions) != 1 || !report.Launchers[0].Collision {
		t.Fatalf("collision was not recorded: %#v", report)
	}
	data, err := os.ReadFile(bare)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "user launcher\n" {
		t.Fatalf("user launcher was changed: %q", data)
	}
	alias := launcherFile(t, paths, "baron-dsh")
	if _, err := os.Stat(alias); err != nil {
		t.Fatalf("collision-free Baron alias was not created: %v", err)
	}
}

func TestLauncherTransactionRollbackRestoresOwnedLauncher(t *testing.T) {
	paths := launcherFixturePaths(t)
	firstTarget := launcherTarget(t, paths, "dsh-old")
	if _, err := InstallLaunchers(paths, []LauncherSpec{{Name: "dsh", Target: firstTarget}}); err != nil {
		t.Fatal(err)
	}
	secondTarget := launcherTarget(t, paths, "dsh-new")
	transaction, _, err := PrepareLaunchers(paths, []LauncherSpec{{Name: "dsh", Target: secondTarget}})
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Apply(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(launcherFile(t, paths, "dsh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), firstTarget) || strings.Contains(string(data), secondTarget) {
		t.Fatalf("rollback did not restore old launcher: %s", data)
	}
}

func TestPrepareLaunchersRejectsOutsideTarget(t *testing.T) {
	paths := launcherFixturePaths(t)
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("outside"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareLaunchers(paths, []LauncherSpec{{Name: "dsh", Target: target}}); err == nil || !strings.Contains(err.Error(), "managed runtime") {
		t.Fatalf("outside launcher target was accepted: %v", err)
	}
}
