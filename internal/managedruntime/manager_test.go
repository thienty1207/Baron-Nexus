package managedruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type managerProgress struct{}

func (managerProgress) Step(string)                   {}
func (managerProgress) Download(string, int64, int64) {}

type fixtureDownloader struct {
	payload []byte
	err     error
}

func (d fixtureDownloader) Download(ctx context.Context, _ Asset, destination io.Writer, _ ProgressReporter) (DownloadReceipt, error) {
	if d.err != nil {
		return DownloadReceipt{}, d.err
	}
	if err := ctx.Err(); err != nil {
		return DownloadReceipt{}, err
	}
	if _, err := destination.Write(d.payload); err != nil {
		return DownloadReceipt{}, err
	}
	sum := sha256.Sum256(d.payload)
	return DownloadReceipt{Bytes: int64(len(d.payload)), SHA256: hex.EncodeToString(sum[:])}, nil
}

type fixtureProbe struct {
	err error
}

func (p fixtureProbe) Verify(context.Context, ComponentPlan, string) error {
	return p.err
}

type fixtureComponentInstaller struct {
	calls int
}

func (i *fixtureComponentInstaller) Install(_ context.Context, component ComponentPlan, _ string, destination, _ string, _ ProgressReporter) error {
	i.calls++
	path := filepath.Join(destination, "bin", string(component.ID))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700)
}

func testPlan(payload []byte) ResolutionPlan {
	sum := sha256.Sum256(payload)
	return ResolutionPlan{
		ID: "plan-test", CreatedAt: time.Unix(1, 0).UTC(), Platform: runtime.GOOS, Architecture: runtime.GOARCH,
		CompatibilityVersion: "test",
		Components:           []ComponentPlan{{ID: ComponentBun, Version: "1.0.0", Source: "fixture", URL: "https://example.invalid/bun", SHA256: hex.EncodeToString(sum[:]), Platform: runtime.GOOS, Architecture: runtime.GOARCH}},
	}
}

func newFixtureManager(t *testing.T, payload []byte, probe Probe) Manager {
	t.Helper()
	paths, err := ResolvePaths(filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	return Manager{
		Paths: paths, Downloader: fixtureDownloader{payload: payload}, Probe: probe,
		Progress: managerProgress{}, Clock: func() time.Time { return time.Unix(100, 0).UTC() },
	}
}

func TestManagerRejectsChecksumMismatchWithoutActivation(t *testing.T) {
	payload := []byte("managed runtime payload")
	manager := newFixtureManager(t, payload, fixtureProbe{})
	plan := testPlan([]byte("different payload"))
	if _, err := manager.Apply(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("checksum mismatch was not rejected: %v", err)
	}
	if _, err := os.Stat(manager.Paths.Current); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed apply changed current generation: %v", err)
	}
}

func TestManagerRejectsPackagePlanWithoutComponentInstaller(t *testing.T) {
	payload := []byte("npm package payload")
	manager := newFixtureManager(t, payload, fixtureProbe{})
	plan := testPlan(payload)
	plan.Components[0].InstallMethod = InstallMethodNPM
	plan.Components[0].Package = "bun"
	if _, err := manager.Apply(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "component installer") {
		t.Fatalf("package plan was staged without an installer: %v", err)
	}
}

func TestManagerDispatchesPackageComponentToInstaller(t *testing.T) {
	payload := []byte("npm package payload")
	manager := newFixtureManager(t, payload, fixtureProbe{})
	installer := &fixtureComponentInstaller{}
	manager.Installer = installer
	plan := testPlan(payload)
	plan.Components[0].InstallMethod = InstallMethodNPM
	plan.Components[0].Package = "bun"
	plan.Components[0].EntryPoint = "bun"
	report, err := manager.Apply(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if installer.calls != 1 || len(report.Receipts) != 1 || report.Receipts[0].InstallMethod != InstallMethodNPM || report.Receipts[0].Package != "bun" {
		t.Fatalf("package installer dispatch/receipt=%d/%#v", installer.calls, report.Receipts)
	}
}

func TestManagerRejectsArchiveTraversal(t *testing.T) {
	payload := archiveFixture(t, "../escaped", []byte("unsafe"))
	manager := newFixtureManager(t, payload, fixtureProbe{})
	plan := testPlan(payload)
	plan.Components[0].URL = "https://example.invalid/runtime.tar.gz"
	if _, err := manager.Apply(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "archive") {
		t.Fatalf("archive traversal was not rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(manager.Paths.Root), "escaped")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive escaped the managed root: %v", err)
	}
}

func TestManagerKeepsPreviousGenerationWhenProbeFails(t *testing.T) {
	payload := []byte("managed runtime payload")
	manager := newFixtureManager(t, payload, fixtureProbe{})
	plan := testPlan(payload)
	if _, err := manager.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(manager.Paths.Current)
	if err != nil {
		t.Fatal(err)
	}
	manager.Probe = fixtureProbe{err: errors.New("probe failed")}
	plan.ID = "plan-second"
	if _, err := manager.Apply(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "probe") {
		t.Fatalf("probe failure was not returned: %v", err)
	}
	current, err := os.ReadFile(manager.Paths.Current)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, current) {
		t.Fatal("probe failure replaced the active generation")
	}
}

func TestManagerRemovesDownloadTemporaryFilesAfterStaging(t *testing.T) {
	payload := []byte("managed runtime payload")
	manager := newFixtureManager(t, payload, fixtureProbe{})
	report, err := manager.Apply(context.Background(), testPlan(payload))
	if err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(manager.Paths.Root, "generations", report.Generation, ".download-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary download files remain after staging: %v", matches)
	}
}

func TestManagerRollsBackToPreviousGeneration(t *testing.T) {
	payload := []byte("managed runtime payload")
	manager := newFixtureManager(t, payload, fixtureProbe{})
	firstPlan := testPlan(payload)
	if _, err := manager.Apply(context.Background(), firstPlan); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(manager.Paths.Current)
	if err != nil {
		t.Fatal(err)
	}
	secondPlan := firstPlan
	secondPlan.ID = "plan-second"
	secondPlan.CreatedAt = time.Unix(2, 0).UTC()
	if _, err := manager.Apply(context.Background(), secondPlan); err != nil {
		t.Fatal(err)
	}
	if err := manager.Rollback(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(manager.Paths.Current)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, current) {
		t.Fatalf("rollback did not restore previous generation:\n%s\n%s", first, current)
	}
}

func TestManagerVerifyRejectsGenerationWithMissingReceipt(t *testing.T) {
	payload := []byte("verified managed runtime payload")
	manager := newFixtureManager(t, payload, fixtureProbe{})
	report, err := manager.Apply(context.Background(), testPlan(payload))
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := filepath.Glob(filepath.Join(manager.Paths.Receipts, report.Generation+"-*.json"))
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipt fixture=%v err=%v", receipts, err)
	}
	if err := os.Remove(receipts[0]); err != nil {
		t.Fatal(err)
	}
	if err := manager.Verify(context.Background(), report.Generation); err == nil || !strings.Contains(err.Error(), "receipt") {
		t.Fatalf("missing receipt was treated as ready: %v", err)
	}
}

func TestManagerVerifyRejectsGenerationWithMissingManifest(t *testing.T) {
	payload := []byte("verified managed runtime manifest payload")
	manager := newFixtureManager(t, payload, fixtureProbe{})
	report, err := manager.Apply(context.Background(), testPlan(payload))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := manager.Paths.Generation(report.Generation)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(generation, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"generation":"`+report.Generation+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	if err := manager.Verify(context.Background(), report.Generation); err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("missing generation manifest was treated as ready: %v", err)
	}
}

func TestManagerWritesGenerationManifestBeforeActivation(t *testing.T) {
	payload := []byte("managed runtime generation manifest")
	manager := newFixtureManager(t, payload, fixtureProbe{})
	report, err := manager.Apply(context.Background(), testPlan(payload))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := manager.Paths.Generation(report.Generation)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(generation, "manifest.json"))
	if err != nil {
		t.Fatalf("generation manifest was not written: %v", err)
	}
	if !strings.Contains(string(data), `"plan_id": "plan-test"`) || !strings.Contains(string(data), `"component": "bun"`) {
		t.Fatalf("generation manifest omitted plan/receipt evidence: %s", data)
	}
}

func TestManagerInstallsManagedLauncherForActiveGeneration(t *testing.T) {
	payload := executableArchiveFixture(t, "bin/bun", []byte("#!/bin/sh\nexit 0\n"))
	manager := newFixtureManager(t, payload, fixtureProbe{})
	manager.EnableLaunchers = true
	plan := testPlan(payload)
	plan.Components[0].URL = "https://example.invalid/bun.tar.gz"
	report, err := manager.Apply(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Launchers) != 1 {
		t.Fatalf("managed launcher was not reported: %#v", report)
	}
	launcherPath := filepath.Join(manager.Paths.Bin, launcherFilename("bun"))
	data, err := os.ReadFile(launcherPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), LauncherMarker) || !strings.Contains(string(data), report.Generation) {
		t.Fatalf("launcher does not target the active generation: %s", data)
	}
	if err := manager.Verify(context.Background(), report.Generation); err != nil {
		t.Fatalf("managed launcher with PATH binding failed verification: %v", err)
	}
}

func TestManagerVerifyRejectsChangedManagedAgentIdentity(t *testing.T) {
	payload := executableArchiveFixture(t, "bin/dsh", []byte("#!/bin/sh\nexit 0\n"))
	manager := newFixtureManager(t, payload, fixtureProbe{})
	manager.EnableLaunchers = true
	plan := testPlan(payload)
	plan.Components[0].ID = ComponentDSH
	plan.Components[0].URL = "https://example.invalid/dsh.tar.gz"
	report, err := manager.Apply(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Launchers) != 1 || report.Launchers[0].ClientIdentity != "dsh" {
		t.Fatalf("DSH launcher identity=%#v", report.Launchers)
	}
	if err := os.WriteFile(report.Launchers[0].Path, renderLauncher(report.Launchers[0].Target, "codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := manager.Verify(context.Background(), report.Generation); err == nil || !strings.Contains(err.Error(), "launcher") {
		t.Fatalf("changed managed agent identity was treated as valid: %v", err)
	}
}

func TestManagerVerifyRejectsMissingManagedLauncher(t *testing.T) {
	payload := executableArchiveFixture(t, "bin/bun", []byte("#!/bin/sh\nexit 0\n"))
	manager := newFixtureManager(t, payload, fixtureProbe{})
	manager.EnableLaunchers = true
	plan := testPlan(payload)
	plan.Components[0].URL = "https://example.invalid/bun.tar.gz"
	report, err := manager.Apply(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(manager.Paths.Bin, launcherFilename("bun"))); err != nil {
		t.Fatal(err)
	}
	if err := manager.Verify(context.Background(), report.Generation); err == nil || !strings.Contains(err.Error(), "launcher") {
		t.Fatalf("missing managed launcher was treated as ready: %v", err)
	}
}

func TestManagerVerifyRejectsManifestPlanMismatch(t *testing.T) {
	payload := []byte("managed runtime manifest plan identity")
	manager := newFixtureManager(t, payload, fixtureProbe{})
	report, err := manager.Apply(context.Background(), testPlan(payload))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := manager.Paths.Generation(report.Generation)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(generation, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"plan_id": "plan-test"`), []byte(`"plan_id": "plan-forged"`), 1)
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Verify(context.Background(), report.Generation); err == nil || !strings.Contains(err.Error(), "plan") {
		t.Fatalf("manifest plan mismatch was treated as ready: %v", err)
	}
}

func TestManagerPreservesInterruptedStagingForRepair(t *testing.T) {
	payload := []byte("managed runtime payload")
	manager := newFixtureManager(t, payload, fixtureProbe{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.Apply(ctx, testPlan(payload)); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted apply error=%v, want context cancellation", err)
	}
	entries, err := os.ReadDir(manager.Paths.Operations)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("interrupted apply did not leave resumable operation state")
	}
}

type countingManagerDownloader struct {
	payload []byte
	calls   *int
}

func (d countingManagerDownloader) Download(ctx context.Context, _ Asset, destination io.Writer, _ ProgressReporter) (DownloadReceipt, error) {
	*d.calls++
	if err := ctx.Err(); err != nil {
		return DownloadReceipt{}, err
	}
	if _, err := destination.Write(d.payload); err != nil {
		return DownloadReceipt{}, err
	}
	sum := sha256.Sum256(d.payload)
	return DownloadReceipt{Bytes: int64(len(d.payload)), SHA256: hex.EncodeToString(sum[:])}, nil
}

func TestManagerReusesVerifiedContentAddressedCache(t *testing.T) {
	payload := []byte("cacheable managed runtime payload")
	calls := 0
	manager := newFixtureManager(t, payload, fixtureProbe{})
	manager.Downloader = countingManagerDownloader{payload: payload, calls: &calls}
	if _, err := manager.Apply(context.Background(), testPlan(payload)); err != nil {
		t.Fatal(err)
	}
	second := testPlan(payload)
	second.CreatedAt = time.Unix(2, 0).UTC()
	if _, err := manager.Apply(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("verified cache was not reused; downloader calls=%d", calls)
	}
}

func TestManagerResumesInterruptedGenerationOnTheSamePlan(t *testing.T) {
	payload := []byte("resumable managed runtime payload")
	manager := newFixtureManager(t, payload, fixtureProbe{})
	ctx, cancel := context.WithCancel(context.Background())
	manager.Downloader = cancelAfterWriteManagerDownloader{payload: payload, cancel: cancel}
	if _, err := manager.Apply(ctx, testPlan(payload)); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("initial interrupted apply error=%v", err)
	}
	manager.Downloader = fixtureDownloader{payload: payload}
	report, err := manager.Apply(context.Background(), testPlan(payload))
	if err != nil {
		t.Fatal(err)
	}
	if !report.Resumed {
		t.Fatal("manager did not resume the interrupted generation")
	}
}

func TestLauncherSpecsUsePython3WhenPythonAliasIsAbsent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "python")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	python3 := filepath.Join(root, "python3")
	if err := os.WriteFile(python3, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	specs, err := launcherSpecsForReceipts([]Receipt{{
		Component: ComponentPython, Version: "3.13.0", Source: "fixture",
		InstallPath: root, Generation: "generation-1", BaronOwned: true,
		VerifiedAt: time.Unix(1, 0).UTC(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 || specs[0].Name != "python" || specs[1].Name != "python3" || specs[0].Target != python3 || specs[1].Target != python3 {
		t.Fatalf("Python aliases were not resolved through python3: %#v", specs)
	}
}

func TestLauncherSpecsInjectIdentityForManagedAgentExecutables(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agents")
	if err := os.MkdirAll(filepath.Join(root, "dsh", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "codex", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dsh", "codex"} {
		path := filepath.Join(root, name, "bin", name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	receipts := []Receipt{
		{Component: ComponentDSH, Version: "0.1.1", Source: "fixture", InstallPath: filepath.Join(root, "dsh"), Generation: "generation-1", BaronOwned: true, VerifiedAt: time.Unix(1, 0).UTC()},
		{Component: ComponentCodex, Version: "0.1.0", Source: "fixture", InstallPath: filepath.Join(root, "codex"), Generation: "generation-1", BaronOwned: true, VerifiedAt: time.Unix(1, 0).UTC()},
	}
	specs, err := launcherSpecsForReceipts(receipts)
	if err != nil {
		t.Fatal(err)
	}
	identities := make(map[string]string, len(specs))
	for _, spec := range specs {
		identities[spec.Name] = spec.ClientIdentity
	}
	if identities["dsh"] != "dsh" || identities["codex"] != "codex" {
		t.Fatalf("managed agent launcher identities=%#v", identities)
	}
}

func TestNormalizeNPMArchiveLayoutCreatesManagedPrefix(t *testing.T) {
	destination := t.TempDir()
	generation := filepath.Dir(destination)
	node := filepath.Join(generation, string(ComponentNode), "bin", "node")
	if err := os.MkdirAll(filepath.Dir(node), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node, []byte("managed node"), 0o700); err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Join(destination, "package")
	if err := os.MkdirAll(filepath.Join(packageRoot, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packageRoot, "node_modules", "npm-dependency"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"npm", "npm.cmd", "npm.ps1", "npx", "npx.cmd", "npx.ps1"} {
		if err := os.WriteFile(filepath.Join(packageRoot, "bin", name), []byte("launcher"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := normalizeNPMArchiveLayout(destination, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "node_modules", "npm", "bin", "npm")); err != nil {
		t.Fatalf("npm package was not moved into the prefix: %v", err)
	}
	if _, err := os.Stat(packageRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("raw npm package directory remains after normalization: %v", err)
	}
	for _, name := range []string{"npm", "npx"} {
		for _, suffix := range launcherSuffixes() {
			path := filepath.Join(destination, name+suffix)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("managed npm launcher %s was not written: %v", name+suffix, err)
			}
			if !strings.Contains(string(data), node) {
				t.Fatalf("managed npm launcher %s does not bind managed Node: %q", name+suffix, string(data))
			}
		}
	}
}

func TestNormalizePNPMArchiveLayoutCreatesManagedLauncher(t *testing.T) {
	destination := filepath.Join(t.TempDir(), string(ComponentPNPM))
	generation := filepath.Dir(destination)
	node := filepath.Join(generation, string(ComponentNode), "bin", "node")
	if err := os.MkdirAll(filepath.Dir(node), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node, []byte("managed node"), 0o700); err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Join(destination, "package")
	if err := os.MkdirAll(filepath.Join(packageRoot, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pnpm.cjs", "pnpx.cjs"} {
		if err := os.WriteFile(filepath.Join(packageRoot, "bin", name), []byte("console.log('pnpm')"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := normalizePNPMArchiveLayout(destination, generation); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pnpm", "pnpx"} {
		for _, suffix := range launcherSuffixes() {
			path := filepath.Join(destination, name+suffix)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("managed pnpm launcher %s was not written: %v", name+suffix, err)
			}
			if !strings.Contains(string(data), node) || !strings.Contains(string(data), filepath.Join(packageRoot, "bin")) {
				t.Fatalf("managed pnpm launcher %s is not bound to managed Node/package: %q", name+suffix, string(data))
			}
		}
	}
}

type cancelAfterWriteManagerDownloader struct {
	payload []byte
	cancel  context.CancelFunc
}

func (d cancelAfterWriteManagerDownloader) Download(_ context.Context, _ Asset, destination io.Writer, _ ProgressReporter) (DownloadReceipt, error) {
	if _, err := destination.Write(d.payload); err != nil {
		return DownloadReceipt{}, err
	}
	d.cancel()
	return DownloadReceipt{}, context.Canceled
}

func archiveFixture(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func executableArchiveFixture(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o700, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
