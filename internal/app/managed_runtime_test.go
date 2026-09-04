package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/doctor"
	"github.com/baron-shared-brain/baron/internal/install"
	"github.com/baron-shared-brain/baron/internal/managedruntime"
)

type appBundleDownloader struct {
	payload []byte
}

func (d appBundleDownloader) Download(_ context.Context, _ managedruntime.Asset, writer io.Writer, _ managedruntime.ProgressReporter) (managedruntime.DownloadReceipt, error) {
	if _, err := writer.Write(d.payload); err != nil {
		return managedruntime.DownloadReceipt{}, err
	}
	sum := sha256.Sum256(d.payload)
	return managedruntime.DownloadReceipt{Bytes: int64(len(d.payload)), SHA256: hex.EncodeToString(sum[:])}, nil
}

type appBundleProbe struct{}

func (appBundleProbe) Verify(context.Context, managedruntime.ComponentPlan, string) error { return nil }

func TestBootstrapPlanFromManagedRuntimeUsesResolvedVersions(t *testing.T) {
	plan := managedruntime.ResolutionPlan{
		ID: "plan-bootstrap", CreatedAt: time.Unix(1, 0).UTC(), Platform: "windows", Architecture: "amd64",
		Components: []managedruntime.ComponentPlan{
			{ID: managedruntime.ComponentDSH, Version: "0.9.1", Source: "catalog", URL: "https://example.invalid/dsh", SHA256: hex.EncodeToString(make([]byte, sha256.Size)), Platform: "windows", Architecture: "amd64"},
			{ID: managedruntime.ComponentCodex, Version: "1.2.3", Source: "catalog", URL: "https://example.invalid/codex", SHA256: hex.EncodeToString(make([]byte, sha256.Size)), Platform: "windows", Architecture: "amd64"},
		},
	}
	bootstrap, err := bootstrapPlanFromManagedRuntime(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ name, version string }{{"dsh", "0.9.1"}, {"codex", "1.2.3"}} {
		state, ok := bootstrap.State(item.name)
		if !ok || state.LatestVersion != item.version || state.LocalVersion != item.version || !state.Installed || state.NeedsUpdate {
			t.Fatalf("managed %s state=%#v exists=%v", item.name, state, ok)
		}
	}
}

func TestApplyManagedRuntimePlanStagesOneImmutablePlan(t *testing.T) {
	payload := []byte("bundle payload")
	sum := sha256.Sum256(payload)
	paths, err := managedruntime.ResolvePaths(filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	plan := managedruntime.ResolutionPlan{
		ID: "plan-app", CreatedAt: time.Unix(1, 0).UTC(), Platform: "windows", Architecture: "amd64",
		Components: []managedruntime.ComponentPlan{{ID: managedruntime.ComponentBun, Version: "1.0.0", Source: "catalog", URL: "https://example.invalid/bun", SHA256: hex.EncodeToString(sum[:]), Platform: "windows", Architecture: "amd64"}},
	}
	application := New()
	application.GlobalPath = filepath.Join(t.TempDir(), "global.json")
	resolverCalls := 0
	application.ManagedRuntimePlanResolver = func(context.Context, install.ProgressReporter) (managedruntime.ResolutionPlan, error) {
		resolverCalls++
		return plan, nil
	}
	application.ManagedRuntimeManager = &managedruntime.Manager{
		Paths: paths, Downloader: appBundleDownloader{payload: payload}, Probe: appBundleProbe{},
		Clock: func() time.Time { return time.Unix(2, 0).UTC() },
	}
	report, err := application.applyManagedRuntimePlan(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 1 || report.PlanID != plan.ID || report.Generation == "" {
		t.Fatalf("plan orchestration calls=%d report=%#v", resolverCalls, report)
	}
}

func TestNewConfiguresDefaultManagedRuntimeCoordinator(t *testing.T) {
	application := New()
	if application.ManagedRuntimeManager == nil || application.ManagedRuntimePlanResolver == nil {
		t.Fatal("New did not configure the managed runtime coordinator")
	}
	if application.ManagedRuntimeManager.Downloader == nil || application.ManagedRuntimeManager.Probe == nil {
		t.Fatal("default managed runtime coordinator is missing downloader or probe")
	}
	if !strings.Contains(application.ManagedRuntimeCatalogURL, "managed-runtime-catalog.json") {
		t.Fatalf("default managed runtime catalog URL=%q", application.ManagedRuntimeCatalogURL)
	}
}

func TestManagedRuntimeReadinessBlocksNativeWindowsStrix(t *testing.T) {
	check, blocked := managedRuntimeHostCheck("windows")
	if !blocked || check.Name != "managed-runtime" || check.Status != doctor.StatusIncomplete {
		t.Fatalf("Windows native Strix was not blocked in readiness: blocked=%v check=%#v", blocked, check)
	}
	if !strings.Contains(strings.ToLower(check.Message), "wsl2") {
		t.Fatalf("Windows readiness message lacks WSL2 guidance: %q", check.Message)
	}
	if _, blocked := managedRuntimeHostCheck("linux"); blocked {
		t.Fatal("Linux Strix readiness was incorrectly blocked")
	}
}

func TestManagedRuntimeManagerUsesPersistedStatePaths(t *testing.T) {
	persistedRoot := filepath.Join(t.TempDir(), "persisted-runtime")
	persistedLaunchers := filepath.Join(t.TempDir(), "persisted-bin")
	originalRoot := filepath.Join(t.TempDir(), "original-runtime")
	originalPaths, err := managedruntime.ResolvePaths(originalRoot)
	if err != nil {
		t.Fatal(err)
	}
	application := &App{ManagedRuntimeManager: &managedruntime.Manager{Paths: originalPaths}}
	manager, err := application.managedRuntimeManagerForState(config.ManagedRuntimeState{
		Root:              persistedRoot,
		LauncherDirectory: persistedLaunchers,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manager.Paths.Root != persistedRoot || manager.Paths.LauncherDirectory != persistedLaunchers {
		t.Fatalf("manager paths=%#v, want persisted runtime state", manager.Paths)
	}
	if application.ManagedRuntimeManager.Paths.Root != originalPaths.Root {
		t.Fatal("building a state-scoped manager mutated the configured manager")
	}
}

func TestPersistManagedRuntimeStateRecordsLauncherOwnership(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	paths, err := managedruntime.ResolvePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	paths.LauncherDirectory = filepath.Join(t.TempDir(), "baron-bin")
	application := &App{GlobalPath: filepath.Join(t.TempDir(), "global.json")}
	plan := managedruntime.ResolutionPlan{
		ID: "plan-launcher-state", CreatedAt: time.Unix(1, 0).UTC(), Platform: "windows", Architecture: "amd64",
		Components: []managedruntime.ComponentPlan{{ID: managedruntime.ComponentBun, Version: "1.0.0", Source: "catalog", URL: "https://example.invalid/bun", SHA256: strings.Repeat("a", 64), Platform: "windows", Architecture: "amd64"}},
	}
	launcherPath := filepath.Join(paths.LauncherDirectory, "bun")
	report := managedruntime.OperationReport{
		PlanID: "plan-launcher-state", Generation: "generation-1",
		Receipts:  []managedruntime.Receipt{{Component: managedruntime.ComponentBun, Version: "1.0.0", Source: "catalog", InstallPath: filepath.Join(paths.Root, "generations", "generation-1", "bun"), Generation: "generation-1", BaronOwned: true, VerifiedAt: time.Unix(1, 0).UTC()}},
		Launchers: []managedruntime.Launcher{{Name: "bun", Path: launcherPath, Target: filepath.Join(paths.Root, "generations", "generation-1", "bun", "bun")}},
	}
	if err := application.persistManagedRuntimeState(plan, report, paths); err != nil {
		t.Fatal(err)
	}
	global, err := config.LoadGlobalState(application.GlobalPath)
	if err != nil {
		t.Fatal(err)
	}
	if global.ManagedRuntime == nil || global.ManagedRuntime.LauncherDirectory != paths.LauncherDirectory || len(global.ManagedRuntime.Launchers) != 1 || global.ManagedRuntime.Launchers[0] != launcherPath {
		t.Fatalf("launcher ownership was not persisted: %#v", global.ManagedRuntime)
	}
}
