package uninstall

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/managedruntime"
)

func TestManagedPurgeTargetsAreReceiptBounded(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	targets, err := ManagedPurgeTargets(config.ManagedRuntimeState{Root: root, CurrentGeneration: "generation-current", PreviousGeneration: "generation-previous"})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) < 10 {
		t.Fatalf("managed purge plan is too small: %#v", targets)
	}
	for _, target := range targets {
		if !target.BaronOwned || (!samePath(root, target.Path) && !pathWithin(root, target.Path)) {
			t.Fatalf("target escaped managed ownership boundary: %#v", target)
		}
	}
}

func TestPurgeManagedRuntimeRemovesOwnedRootAndPreservesUnownedTarget(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	external := filepath.Join(filepath.Dir(root), "unrelated-runtime")
	if err := os.MkdirAll(filepath.Join(root, "generations", "current", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "generations", "current", "bin", "strix"), []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := PurgeManagedRuntime(context.Background(), PurgeOptions{
		Root: root,
		Targets: []PurgeTarget{
			{Path: filepath.Join(root, "generations"), Kind: "generations", BaronOwned: true},
			{Path: root, Kind: "managed-root", BaronOwned: true},
			{Path: external, Kind: "unrelated", BaronOwned: false},
		},
	})
	if len(report.Failed) != 0 {
		t.Fatalf("managed purge failed: %#v", report)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("managed runtime remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(external, "keep")); err != nil {
		t.Fatalf("unowned runtime was removed: %v", err)
	}
	if !strings.Contains(strings.Join(report.Preserved, "\n"), external) {
		t.Fatalf("unowned target was not reported as preserved: %#v", report)
	}
}

func TestManagedPurgeRejectsUnsafeOwnedTargetWithoutMutating(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	owned := filepath.Join(root, "owned")
	if err := os.MkdirAll(owned, 0o700); err != nil {
		t.Fatal(err)
	}
	report := PurgeManagedRuntime(context.Background(), PurgeOptions{
		Root: root,
		Targets: []PurgeTarget{
			{Path: owned, Kind: "owned", BaronOwned: true},
			{Path: filepath.Join(root, "..", "escape"), Kind: "escape", BaronOwned: true},
		},
	})
	if len(report.Failed) == 0 {
		t.Fatalf("unsafe target was not rejected: %#v", report)
	}
	if _, err := os.Stat(owned); err != nil {
		t.Fatalf("safe target was mutated after unsafe target: %v", err)
	}
}

func TestManagedPurgeTargetsIncludeOnlyReceiptBackedExternalLaunchers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	launcherDirectory := filepath.Join(t.TempDir(), "baron-bin")
	launcherPath := filepath.Join(launcherDirectory, "dsh")
	if err := os.MkdirAll(launcherDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcherPath, []byte(managedruntime.LauncherMarker+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	targets, err := ManagedPurgeTargets(config.ManagedRuntimeState{
		Root:              root,
		LauncherDirectory: launcherDirectory,
		Launchers:         []string{launcherPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, target := range targets {
		if samePath(target.Path, launcherPath) {
			found = true
			if target.Kind != "launcher" || !target.BaronOwned {
				t.Fatalf("launcher target lost ownership metadata: %#v", target)
			}
		}
	}
	if !found {
		t.Fatalf("receipt-backed external launcher missing from purge plan: %#v", targets)
	}
}

func TestPurgeManagedRuntimeRemovesMarkedExternalLauncherOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	launcherDirectory := filepath.Join(t.TempDir(), "baron-bin")
	managedLauncher := filepath.Join(launcherDirectory, "dsh")
	userLauncher := filepath.Join(launcherDirectory, "codex")
	if err := os.MkdirAll(launcherDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedLauncher, []byte(managedruntime.LauncherMarker+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userLauncher, []byte("user launcher\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	report := PurgeManagedRuntime(context.Background(), PurgeOptions{
		Root:              root,
		LauncherDirectory: launcherDirectory,
		Targets: []PurgeTarget{
			{Path: managedLauncher, Kind: "launcher", BaronOwned: true},
			{Path: userLauncher, Kind: "launcher", BaronOwned: false},
		},
	})
	if len(report.Failed) != 0 {
		t.Fatalf("managed launcher purge failed: %#v", report)
	}
	if _, err := os.Lstat(managedLauncher); !os.IsNotExist(err) {
		t.Fatalf("marked managed launcher remains: %v", err)
	}
	if _, err := os.Stat(userLauncher); err != nil {
		t.Fatalf("unowned launcher was removed: %v", err)
	}
}

func TestPurgeManagedRuntimePreservesReplacedExternalLauncher(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	launcherDirectory := filepath.Join(t.TempDir(), "baron-bin")
	launcherPath := filepath.Join(launcherDirectory, "dsh")
	if err := os.MkdirAll(launcherDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcherPath, []byte("user replacement\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	report := PurgeManagedRuntime(context.Background(), PurgeOptions{
		Root:              root,
		LauncherDirectory: launcherDirectory,
		Targets:           []PurgeTarget{{Path: launcherPath, Kind: "launcher", BaronOwned: true}},
	})
	if len(report.Failed) != 0 {
		t.Fatalf("replacement launcher caused purge failure: %#v", report)
	}
	if _, err := os.Stat(launcherPath); err != nil {
		t.Fatalf("replacement launcher was removed: %v", err)
	}
	if !strings.Contains(strings.Join(report.Preserved, "\n"), launcherPath) {
		t.Fatalf("replacement launcher was not reported as preserved: %#v", report)
	}
}
