package managedruntime

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
)

const managerDefaultMaxDownloadBytes int64 = 8 << 30

type ProgressReporter interface {
	Step(string)
	Download(string, int64, int64)
}

type Downloader interface {
	Download(context.Context, Asset, io.Writer, ProgressReporter) (DownloadReceipt, error)
}

type DownloadReceipt struct {
	Bytes  int64
	SHA256 string
}

type LauncherPreparer func(Paths, []LauncherSpec) (*LauncherTransaction, LauncherReport, error)

type Probe interface {
	Verify(context.Context, ComponentPlan, string) error
}

// ComponentInstaller handles package formats whose verified artifact cannot
// be made runnable by archive extraction alone. It receives the staged local
// artifact and generation root so an implementation can invoke a managed
// dependency without consulting an arbitrary system installation.
type ComponentInstaller interface {
	Install(context.Context, ComponentPlan, string, string, string, ProgressReporter) error
}

type Manager struct {
	Paths            Paths
	Downloader       Downloader
	Probe            Probe
	Installer        ComponentInstaller
	Progress         ProgressReporter
	Clock            func() time.Time
	MaxDownloadBytes int64
	EnableLaunchers  bool
	LauncherPrepare  LauncherPreparer
}

type OperationReport struct {
	PlanID             string
	Generation         string
	Receipts           []Receipt
	Launchers          []Launcher
	LauncherCollisions []string
	RestartRequired    bool
	Resumed            bool
}

type activationState struct {
	PlanID      string    `json:"plan_id"`
	Generation  string    `json:"generation"`
	ActivatedAt time.Time `json:"activated_at"`
}

type operationState struct {
	PlanID     string    `json:"plan_id"`
	Generation string    `json:"generation,omitempty"`
	Status     string    `json:"status"`
	UpdatedAt  time.Time `json:"updated_at"`
	Error      string    `json:"error,omitempty"`
}

func (m Manager) Apply(ctx context.Context, plan ResolutionPlan) (report OperationReport, err error) {
	if err := plan.Validate(); err != nil {
		return OperationReport{}, err
	}
	if m.Downloader == nil {
		return OperationReport{}, errors.New("managed runtime downloader is not configured")
	}
	if m.Probe == nil {
		return OperationReport{}, errors.New("managed runtime probe is not configured")
	}
	for _, component := range plan.Components {
		if component.EffectiveInstallMethod() != InstallMethodArchive && m.Installer == nil {
			return OperationReport{}, fmt.Errorf("managed runtime component installer is not configured for %s", component.ID)
		}
	}
	if err := m.ensureDirectories(); err != nil {
		return OperationReport{}, err
	}
	now := m.now()
	generationID, resumed, err := m.resumableGeneration(plan.ID)
	if err != nil {
		return OperationReport{}, err
	}
	if generationID == "" {
		generationID = fmt.Sprintf("generation-%s-%d", plan.ID, now.UnixNano())
	}
	generation, err := m.Paths.Generation(generationID)
	if err != nil {
		return OperationReport{}, err
	}
	state := operationState{PlanID: plan.ID, Generation: generationID, Status: "staging", UpdatedAt: now}
	if err := m.writeOperation(state); err != nil {
		return OperationReport{}, err
	}
	if err := ctx.Err(); err != nil {
		state.Status, state.Error, state.UpdatedAt = "interrupted", "operation interrupted", m.now()
		_ = m.writeOperation(state)
		return OperationReport{}, err
	}
	if err := os.MkdirAll(generation, 0o700); err != nil {
		return OperationReport{}, fmt.Errorf("create managed runtime generation: %w", err)
	}
	keepStaging := false
	defer func() {
		if err != nil && !keepStaging {
			_ = os.RemoveAll(generation)
		}
	}()

	ordered, err := orderComponents(plan.Components)
	if err != nil {
		return OperationReport{}, err
	}
	receipts := make([]Receipt, 0, len(ordered))
	for _, component := range ordered {
		if err := ctx.Err(); err != nil {
			keepStaging = true
			state.Status, state.Error, state.UpdatedAt = "interrupted", "operation interrupted", m.now()
			_ = m.writeOperation(state)
			return OperationReport{}, err
		}
		if receipt, found, receiptErr := m.readStagedReceipt(ctx, generationID, component); receiptErr != nil {
			return OperationReport{}, receiptErr
		} else if found {
			receipts = append(receipts, receipt)
			continue
		}
		receipt, componentErr := m.stageComponent(ctx, generation, component, now)
		if componentErr != nil {
			if errors.Is(componentErr, context.Canceled) || errors.Is(componentErr, context.DeadlineExceeded) {
				keepStaging = true
				state.Status, state.Error, state.UpdatedAt = "interrupted", "operation interrupted", m.now()
			} else {
				state.Status, state.Error, state.UpdatedAt = "failed", safeError(componentErr), m.now()
			}
			_ = m.writeOperation(state)
			return OperationReport{}, componentErr
		}
		receipts = append(receipts, receipt)
	}
	var launcherTransaction *LauncherTransaction
	var launcherReport LauncherReport
	if m.EnableLaunchers || m.LauncherPrepare != nil {
		specs, specErr := launcherSpecsForReceipts(receipts)
		if specErr != nil {
			state.Status, state.Error, state.UpdatedAt = "failed", safeError(specErr), m.now()
			_ = m.writeOperation(state)
			return OperationReport{}, specErr
		}
		prepare := m.LauncherPrepare
		if prepare == nil {
			prepare = PrepareLaunchers
		}
		launcherTransaction, launcherReport, err = prepare(m.Paths, specs)
		if err != nil {
			state.Status, state.Error, state.UpdatedAt = "failed", safeError(err), m.now()
			_ = m.writeOperation(state)
			return OperationReport{}, fmt.Errorf("prepare managed runtime launchers: %w", err)
		}
	}
	if err := m.writeGenerationManifest(generation, GenerationManifest{
		PlanID: plan.ID, Generation: generationID, Platform: plan.Platform,
		Architecture: plan.Architecture, CompatibilityVersion: plan.CompatibilityVersion,
		CreatedAt: now.UTC(), Components: receipts, Launchers: launcherReport.Launchers,
	}); err != nil {
		state.Status, state.Error, state.UpdatedAt = "failed", safeError(err), m.now()
		_ = m.writeOperation(state)
		return OperationReport{}, fmt.Errorf("write managed runtime generation manifest: %w", err)
	}
	previousCurrent, currentExists, currentErr := readOptionalFile(m.Paths.Current)
	if currentErr != nil {
		state.Status, state.Error, state.UpdatedAt = "failed", safeError(currentErr), m.now()
		_ = m.writeOperation(state)
		return OperationReport{}, currentErr
	}
	previousPrevious, previousExists, previousErr := readOptionalFile(m.Paths.Previous)
	if previousErr != nil {
		state.Status, state.Error, state.UpdatedAt = "failed", safeError(previousErr), m.now()
		_ = m.writeOperation(state)
		return OperationReport{}, previousErr
	}
	if err := m.activate(plan.ID, generationID, now); err != nil {
		state.Status, state.Error, state.UpdatedAt = "failed", safeError(err), m.now()
		_ = m.writeOperation(state)
		return OperationReport{}, err
	}
	if launcherTransaction != nil {
		if err := launcherTransaction.Apply(); err != nil {
			launcherRollbackErr := launcherTransaction.Rollback()
			activationRollbackErr := restoreOptionalFiles(m.Paths.Current, previousCurrent, currentExists, m.Paths.Previous, previousPrevious, previousExists)
			state.Status, state.Error, state.UpdatedAt = "failed", safeError(err), m.now()
			_ = m.writeOperation(state)
			if launcherRollbackErr != nil || activationRollbackErr != nil {
				return OperationReport{}, fmt.Errorf("activate managed runtime launchers: %w; launcher rollback=%v; activation rollback=%v", err, launcherRollbackErr, activationRollbackErr)
			}
			return OperationReport{}, fmt.Errorf("activate managed runtime launchers: %w", err)
		}
		launcherTransaction.Commit()
	}
	state.Status, state.Error, state.UpdatedAt = "complete", "", m.now()
	if err := m.writeOperation(state); err != nil {
		return OperationReport{}, err
	}
	return OperationReport{
		PlanID: plan.ID, Generation: generationID, Receipts: receipts,
		Launchers: launcherReport.Launchers, LauncherCollisions: launcherReport.Collisions,
		Resumed: resumed,
	}, nil
}

func (m Manager) Verify(ctx context.Context, generation string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := m.readActivation("")
	if err != nil {
		return err
	}
	if strings.TrimSpace(generation) != "" && state.Generation != generation {
		return fmt.Errorf("managed runtime generation %s is not the active generation", generation)
	}
	path, err := m.Paths.Generation(state.Generation)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("managed runtime generation is unavailable: %w", err)
	}
	if !info.IsDir() {
		return errors.New("managed runtime generation is not a directory")
	}
	if m.Probe == nil {
		return errors.New("managed runtime probe is not configured")
	}
	manifest, err := m.readGenerationManifest(path)
	if err != nil {
		return err
	}
	if manifest.Platform != "" && !strings.EqualFold(manifest.Platform, runtime.GOOS) {
		return fmt.Errorf("managed runtime generation targets %s, not the current platform %s", manifest.Platform, runtime.GOOS)
	}
	if manifest.Architecture != "" && !strings.EqualFold(manifest.Architecture, runtime.GOARCH) {
		return fmt.Errorf("managed runtime generation targets %s, not the current architecture %s", manifest.Architecture, runtime.GOARCH)
	}
	if strings.TrimSpace(state.PlanID) != "" && state.PlanID != "rollback" && manifest.PlanID != state.PlanID {
		return errors.New("managed runtime generation manifest plan ID does not match the active activation")
	}
	receipts, err := m.receiptsForGeneration(state.Generation)
	if err != nil {
		return err
	}
	for _, receipt := range receipts {
		component := ComponentPlan{
			ID: receipt.Component, Version: receipt.Version, Source: receipt.Source,
			InstallMethod: receipt.InstallMethod, Package: receipt.Package, EntryPoint: receipt.EntryPoint,
			Platform: receipt.Platform, Architecture: receipt.Architecture,
		}
		if component.Platform == "" {
			component.Platform = manifest.Platform
		}
		if component.Architecture == "" {
			component.Architecture = manifest.Architecture
		}
		if component.Platform == "" {
			component.Platform = runtime.GOOS
		}
		if component.Architecture == "" {
			component.Architecture = runtime.GOARCH
		}
		if err := m.Probe.Verify(ctx, component, receipt.InstallPath); err != nil {
			return fmt.Errorf("verify managed runtime %s: %w", receipt.Component, err)
		}
	}
	if m.EnableLaunchers || len(manifest.Launchers) > 0 {
		if err := m.verifyGenerationLaunchers(path, manifest.Launchers); err != nil {
			return err
		}
	}
	return nil
}

func (m Manager) Rollback(ctx context.Context, generation string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.ensureDirectories(); err != nil {
		return err
	}
	current, err := os.ReadFile(m.Paths.Current)
	if err != nil {
		return fmt.Errorf("read active managed runtime generation: %w", err)
	}
	target := strings.TrimSpace(generation)
	if target == "" {
		previous, readErr := os.ReadFile(m.Paths.Previous)
		if readErr != nil {
			return fmt.Errorf("read previous managed runtime generation: %w", readErr)
		}
		state, stateErr := decodeActivation(previous)
		if stateErr != nil {
			return stateErr
		}
		if err := m.verifyGenerationDirectory(state.Generation); err != nil {
			return err
		}
		if err := config.AtomicWriteFile(m.Paths.Current, previous, 0o600); err != nil {
			return fmt.Errorf("activate previous managed runtime generation: %w", err)
		}
		return config.AtomicWriteFile(m.Paths.Previous, current, 0o600)
	}
	if _, err := m.Paths.Generation(target); err != nil {
		return err
	}
	if err := m.verifyGenerationDirectory(target); err != nil {
		return err
	}
	targetPath, err := m.Paths.Generation(target)
	if err != nil {
		return err
	}
	manifest, err := m.readGenerationManifest(targetPath)
	if err != nil {
		return fmt.Errorf("verify rollback generation: %w", err)
	}
	state := activationState{PlanID: manifest.PlanID, Generation: target, ActivatedAt: m.now()}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := config.AtomicWriteFile(m.Paths.Previous, current, 0o600); err != nil {
		return err
	}
	return config.AtomicWriteFile(m.Paths.Current, append(data, '\n'), 0o600)
}

func (m Manager) ensureDirectories() error {
	if strings.TrimSpace(m.Paths.Root) == "" {
		return errors.New("managed runtime paths are not configured")
	}
	for _, path := range []string{m.Paths.Root, m.Paths.Generations, m.Paths.Cache, m.Paths.Credentials, m.Paths.Receipts, m.Paths.Bin, m.Paths.Operations} {
		if err := m.Paths.ValidateOwned(path); err != nil {
			return err
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create managed runtime directory: %w", err)
		}
	}
	return nil
}

func (m Manager) stageComponent(ctx context.Context, generation string, component ComponentPlan, now time.Time) (Receipt, error) {
	componentPath := filepath.Join(generation, string(component.ID))
	if err := m.Paths.ValidateOwned(componentPath); err != nil {
		return Receipt{}, err
	}
	if err := os.MkdirAll(componentPath, 0o700); err != nil {
		return Receipt{}, fmt.Errorf("create %s staging directory: %w", component.ID, err)
	}
	_ = removeStaleDownloads(generation)
	cachePath, cached, err := m.cachedArchive(component)
	if err != nil {
		return Receipt{}, err
	}
	archivePath := cachePath
	actual := component.SHA256
	var size int64
	if cached {
		if m.Progress != nil {
			m.Progress.Step("Using cached " + string(component.ID))
		}
		actual, size, err = fileSHA256(cachePath, m.maxDownloadBytes())
		if err != nil {
			return Receipt{}, fmt.Errorf("verify cached %s archive: %w", component.ID, err)
		}
		if !strings.EqualFold(actual, component.SHA256) {
			return Receipt{}, fmt.Errorf("verify cached %s archive: checksum mismatch", component.ID)
		}
	} else {
		temp, createErr := os.CreateTemp(generation, ".download-*")
		if createErr != nil {
			return Receipt{}, fmt.Errorf("create %s download staging file: %w", component.ID, createErr)
		}
		tempPath := temp.Name()
		defer func() {
			_ = temp.Close()
			_ = os.Remove(tempPath)
		}()
		asset := Asset{URL: component.URL, SHA256: component.SHA256, Platform: component.Platform, Architecture: component.Architecture}
		if m.Progress != nil {
			m.Progress.Step("Downloading " + string(component.ID))
		}
		downloadReceipt, downloadErr := m.Downloader.Download(ctx, asset, temp, m.Progress)
		if downloadErr != nil {
			return Receipt{}, fmt.Errorf("download %s: %w", component.ID, downloadErr)
		}
		if err := temp.Sync(); err != nil {
			return Receipt{}, fmt.Errorf("sync %s download: %w", component.ID, err)
		}
		if err := temp.Close(); err != nil {
			return Receipt{}, fmt.Errorf("close %s download: %w", component.ID, err)
		}
		actual, size, err = fileSHA256(tempPath, m.maxDownloadBytes())
		if err != nil {
			return Receipt{}, fmt.Errorf("verify %s download: %w", component.ID, err)
		}
		if !strings.EqualFold(actual, component.SHA256) || (downloadReceipt.SHA256 != "" && !strings.EqualFold(actual, downloadReceipt.SHA256)) {
			return Receipt{}, fmt.Errorf("verify %s checksum: checksum mismatch", component.ID)
		}
		if downloadReceipt.Bytes > 0 && downloadReceipt.Bytes != size {
			return Receipt{}, fmt.Errorf("verify %s download: byte count mismatch", component.ID)
		}
		if err := m.storeCachedArchive(tempPath, cachePath); err != nil {
			return Receipt{}, fmt.Errorf("cache %s archive: %w", component.ID, err)
		}
		archivePath = cachePath
	}
	if err := os.RemoveAll(componentPath); err != nil {
		return Receipt{}, fmt.Errorf("reset %s staging directory: %w", component.ID, err)
	}
	if err := os.MkdirAll(componentPath, 0o700); err != nil {
		return Receipt{}, fmt.Errorf("recreate %s staging directory: %w", component.ID, err)
	}
	if component.EffectiveInstallMethod() == InstallMethodArchive {
		if err := extractOrInstall(archivePath, component.URL, componentPath, m.maxDownloadBytes()); err != nil {
			return Receipt{}, fmt.Errorf("extract %s archive: %w", component.ID, err)
		}
		switch component.ID {
		case ComponentNPM:
			if err := normalizeNPMArchiveLayout(componentPath, generation); err != nil {
				return Receipt{}, fmt.Errorf("normalize %s archive: %w", component.ID, err)
			}
		case ComponentPNPM:
			if err := normalizePNPMArchiveLayout(componentPath, generation); err != nil {
				return Receipt{}, fmt.Errorf("normalize %s archive: %w", component.ID, err)
			}
		}
	} else {
		if m.Installer == nil {
			return Receipt{}, fmt.Errorf("component installer is not configured for %s", component.ID)
		}
		if err := m.Installer.Install(ctx, component, archivePath, componentPath, generation, m.Progress); err != nil {
			return Receipt{}, fmt.Errorf("install %s package: %w", component.ID, err)
		}
	}
	if err := m.Probe.Verify(ctx, component, componentPath); err != nil {
		return Receipt{}, fmt.Errorf("probe %s: %w", component.ID, err)
	}
	receipt := Receipt{
		Component: component.ID, Version: component.Version, Source: component.Source,
		InstallMethod: component.EffectiveInstallMethod(), Package: component.Package, EntryPoint: component.EntryPoint,
		Platform: component.Platform, Architecture: component.Architecture,
		InstallPath: componentPath, Executables: []string{componentPath}, SHA256: actual,
		Generation: filepath.Base(generation), BaronOwned: true, VerifiedAt: now.UTC(),
	}
	if err := receipt.Validate(); err != nil {
		return Receipt{}, err
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return Receipt{}, fmt.Errorf("encode %s receipt: %w", component.ID, err)
	}
	receiptPath := filepath.Join(m.Paths.Receipts, receipt.Generation+"-"+string(component.ID)+".json")
	if err := m.Paths.ValidateOwned(receiptPath); err != nil {
		return Receipt{}, err
	}
	if err := config.AtomicWriteFile(receiptPath, append(data, '\n'), 0o600); err != nil {
		return Receipt{}, fmt.Errorf("write %s receipt: %w", component.ID, err)
	}
	return receipt, nil
}

// normalizeNPMArchiveLayout turns the npm package tarball into the prefix
// layout expected by its own launchers. A raw npm tarball extracts to
// prefix/package, while npm.cmd and npm's POSIX launcher resolve
// prefix/node_modules/npm relative to the prefix root.
func normalizeNPMArchiveLayout(destination, generation string) error {
	packageRoot := filepath.Join(destination, "package")
	packageInfo, err := os.Lstat(packageRoot)
	if err != nil {
		return fmt.Errorf("npm archive package directory is missing: %w", err)
	}
	if packageInfo.Mode()&os.ModeSymlink != 0 || !packageInfo.IsDir() {
		return errors.New("npm archive package directory is unsafe")
	}
	nodeModules := filepath.Join(destination, "node_modules")
	if err := os.MkdirAll(nodeModules, 0o700); err != nil {
		return err
	}
	installedRoot := filepath.Join(nodeModules, "npm")
	if _, err := os.Lstat(installedRoot); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("npm archive install target already exists")
		}
		return err
	}
	if err := os.Rename(packageRoot, installedRoot); err != nil {
		return fmt.Errorf("move npm package into prefix: %w", err)
	}
	node, err := findGenerationExecutableForPlan(generation, ComponentPlan{ID: ComponentNode, EntryPoint: "node"}, []ComponentID{ComponentNode})
	if err != nil {
		return fmt.Errorf("resolve managed Node for npm launcher: %w", err)
	}
	for _, item := range []struct {
		name  string
		entry string
	}{
		{name: "npm", entry: "npm-cli.js"},
		{name: "npx", entry: "npx-cli.js"},
	} {
		content, renderErr := renderNPMLauncher(node, filepath.Join(installedRoot, "bin", item.entry))
		if renderErr != nil {
			return fmt.Errorf("render npm launcher %s: %w", item.name, renderErr)
		}
		for _, suffix := range launcherSuffixes() {
			path := filepath.Join(destination, item.name+suffix)
			if err := config.AtomicWriteFile(path, content, 0o700); err != nil {
				return fmt.Errorf("write npm launcher %s: %w", item.name+suffix, err)
			}
		}
	}
	return nil
}

func launcherSuffixes() []string {
	if runtime.GOOS == "windows" {
		return []string{".cmd"}
	}
	return []string{""}
}

func renderNPMLauncher(node, cli string) ([]byte, error) {
	if !filepath.IsAbs(node) || !filepath.IsAbs(cli) {
		return nil, errors.New("npm launcher paths must be absolute")
	}
	if runtime.GOOS == "windows" {
		return []byte(fmt.Sprintf("@echo off\r\nrem baron-nexus: managed npm launcher v1\r\n\"%s\" \"%s\" %%*\r\nexit /b %%errorlevel%%\r\n", strings.ReplaceAll(node, "\"", "\"\""), strings.ReplaceAll(cli, "\"", "\"\""))), nil
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	return []byte("#!/bin/sh\nset -eu\nexec " + quote(node) + " " + quote(cli) + " \"$@\"\n"), nil
}

func normalizePNPMArchiveLayout(destination, generation string) error {
	packageRoot := filepath.Join(destination, "package")
	packageInfo, err := os.Lstat(packageRoot)
	if err != nil {
		return fmt.Errorf("pnpm archive package directory is missing: %w", err)
	}
	if packageInfo.Mode()&os.ModeSymlink != 0 || !packageInfo.IsDir() {
		return errors.New("pnpm archive package directory is unsafe")
	}
	node, err := findGenerationExecutableForPlan(generation, ComponentPlan{ID: ComponentNode, EntryPoint: "node"}, []ComponentID{ComponentNode})
	if err != nil {
		return fmt.Errorf("resolve managed Node for pnpm launcher: %w", err)
	}
	for _, item := range []struct {
		name  string
		entry string
	}{
		{name: "pnpm", entry: "pnpm.cjs"},
		{name: "pnpx", entry: "pnpx.cjs"},
	} {
		entry := filepath.Join(packageRoot, "bin", item.entry)
		info, statErr := os.Lstat(entry)
		if statErr != nil {
			return fmt.Errorf("pnpm archive entry %s is unavailable: %w", item.entry, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("pnpm archive entry %s is unsafe", item.entry)
		}
		content, renderErr := renderPNPMLauncher(node, entry)
		if renderErr != nil {
			return fmt.Errorf("render pnpm launcher %s: %w", item.name, renderErr)
		}
		for _, suffix := range launcherSuffixes() {
			path := filepath.Join(destination, item.name+suffix)
			if err := config.AtomicWriteFile(path, content, 0o700); err != nil {
				return fmt.Errorf("write pnpm launcher %s: %w", item.name+suffix, err)
			}
		}
	}
	return nil
}

func renderPNPMLauncher(node, script string) ([]byte, error) {
	if !filepath.IsAbs(node) || !filepath.IsAbs(script) {
		return nil, errors.New("pnpm launcher paths must be absolute")
	}
	if runtime.GOOS == "windows" {
		return []byte(fmt.Sprintf("@echo off\r\nrem baron-nexus: managed pnpm launcher v1\r\n\"%s\" \"%s\" %%*\r\nexit /b %%errorlevel%%\r\n", escapeBatchValue(node), escapeBatchValue(script))), nil
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	return []byte("#!/bin/sh\nset -eu\nexec " + quote(node) + " " + quote(script) + " \"$@\"\n"), nil
}

func (m Manager) resumableGeneration(planID string) (string, bool, error) {
	if strings.TrimSpace(planID) == "" || strings.ContainsAny(planID, `/\\`) {
		return "", false, errors.New("managed runtime operation ID is not a safe path component")
	}
	path := filepath.Join(m.Paths.Operations, planID+".json")
	if err := m.Paths.ValidateOwned(path); err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read managed runtime operation state: %w", err)
	}
	var state operationState
	if err := json.Unmarshal(data, &state); err != nil {
		return "", false, fmt.Errorf("decode managed runtime operation state: %w", err)
	}
	if state.PlanID != planID {
		return "", false, nil
	}
	if state.Status != "staging" && state.Status != "interrupted" {
		return "", false, nil
	}
	generationID := strings.TrimSpace(state.Generation)
	if generationID == "" {
		return "", false, nil
	}
	if _, err := m.Paths.Generation(generationID); err != nil {
		return "", false, err
	}
	if info, err := os.Stat(filepath.Join(m.Paths.Generations, generationID)); err != nil || !info.IsDir() {
		return "", false, nil
	}
	return generationID, true, nil
}

func (m Manager) readStagedReceipt(ctx context.Context, generationID string, component ComponentPlan) (Receipt, bool, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, false, err
	}
	path := filepath.Join(m.Paths.Receipts, generationID+"-"+string(component.ID)+".json")
	if err := m.Paths.ValidateOwned(path); err != nil {
		return Receipt{}, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, err
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return Receipt{}, false, fmt.Errorf("decode staged %s receipt: %w", component.ID, err)
	}
	if receipt.Component != component.ID || receipt.Version != component.Version || receipt.Generation != generationID || !receiptMatchesPlan(receipt, component) {
		return Receipt{}, false, fmt.Errorf("staged %s receipt does not match the resolution plan", component.ID)
	}
	if err := receipt.Validate(); err != nil {
		return Receipt{}, false, err
	}
	if err := m.Paths.ValidateOwned(receipt.InstallPath); err != nil {
		return Receipt{}, false, err
	}
	generation, err := m.Paths.Generation(generationID)
	if err != nil {
		return Receipt{}, false, err
	}
	if err := validateReceiptPathForGeneration(generation, receipt.InstallPath); err != nil {
		return Receipt{}, false, err
	}
	if _, err := os.Stat(receipt.InstallPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Receipt{}, false, nil
		}
		return Receipt{}, false, fmt.Errorf("inspect staged %s receipt content: %w", component.ID, err)
	}
	if _, cached, cacheErr := m.cachedArchive(component); cacheErr != nil {
		return Receipt{}, false, cacheErr
	} else if !cached {
		// The staged receipt is not enough to resume: its verified source artifact
		// must still be available or be downloaded again.
		return Receipt{}, false, nil
	}
	if err := m.Probe.Verify(ctx, component, receipt.InstallPath); err != nil {
		return Receipt{}, false, nil
	}
	return receipt, true, nil
}

func receiptMatchesPlan(receipt Receipt, component ComponentPlan) bool {
	if receiptMethod(receipt.InstallMethod) != component.EffectiveInstallMethod() || receipt.Package != component.Package || receipt.EntryPoint != component.EntryPoint {
		return false
	}
	if receipt.Platform != "" && !strings.EqualFold(receipt.Platform, component.Platform) {
		return false
	}
	if receipt.Architecture != "" && !strings.EqualFold(receipt.Architecture, component.Architecture) {
		return false
	}
	return component.SHA256 == "" || strings.EqualFold(receipt.SHA256, component.SHA256)
}

func receiptMatchesReceipt(left, right Receipt) bool {
	return receiptMethod(left.InstallMethod) == receiptMethod(right.InstallMethod) &&
		left.Package == right.Package && left.EntryPoint == right.EntryPoint &&
		(left.Platform == "" || right.Platform == "" || strings.EqualFold(left.Platform, right.Platform)) &&
		(left.Architecture == "" || right.Architecture == "" || strings.EqualFold(left.Architecture, right.Architecture))
}

func receiptMethod(method InstallMethod) InstallMethod {
	if strings.TrimSpace(string(method)) == "" {
		return InstallMethodArchive
	}
	return method
}

func (m Manager) receiptsForGeneration(generation string) ([]Receipt, error) {
	generationPath, err := m.Paths.Generation(generation)
	if err != nil {
		return nil, err
	}
	manifest, err := m.readGenerationManifest(generationPath)
	if err != nil {
		return nil, err
	}
	expected := make(map[ComponentID]Receipt, len(manifest.Components))
	for _, receipt := range manifest.Components {
		expected[receipt.Component] = receipt
	}
	entries, err := os.ReadDir(m.Paths.Receipts)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("managed runtime generation has no receipts")
	}
	if err != nil {
		return nil, fmt.Errorf("read managed runtime receipts: %w", err)
	}
	prefix := generation + "-"
	receipts := make([]Receipt, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(m.Paths.Receipts, entry.Name())
		info, lstatErr := os.Lstat(path)
		if lstatErr != nil {
			return nil, lstatErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("managed runtime receipt %s is not a regular file", entry.Name())
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		var receipt Receipt
		if err := json.Unmarshal(data, &receipt); err != nil {
			return nil, fmt.Errorf("decode managed runtime receipt %s: %w", entry.Name(), err)
		}
		if receipt.Generation != generation {
			return nil, fmt.Errorf("managed runtime receipt %s generation mismatch", entry.Name())
		}
		if err := receipt.Validate(); err != nil {
			return nil, err
		}
		if err := m.Paths.ValidateOwned(receipt.InstallPath); err != nil {
			return nil, err
		}
		generationPath, err := m.Paths.Generation(generation)
		if err != nil {
			return nil, err
		}
		if err := validateReceiptPathForGeneration(generationPath, receipt.InstallPath); err != nil {
			return nil, err
		}
		if _, err := os.Stat(receipt.InstallPath); err != nil {
			return nil, fmt.Errorf("managed runtime receipt %s points to missing content: %w", entry.Name(), err)
		}
		expectedReceipt, ok := expected[receipt.Component]
		if !ok {
			return nil, fmt.Errorf("managed runtime receipt %s is not listed in the generation manifest", entry.Name())
		}
		if receipt.Version != expectedReceipt.Version || !samePath(receipt.InstallPath, expectedReceipt.InstallPath) || !strings.EqualFold(receipt.SHA256, expectedReceipt.SHA256) || !receiptMatchesReceipt(receipt, expectedReceipt) {
			return nil, fmt.Errorf("managed runtime receipt %s differs from the generation manifest", entry.Name())
		}
		delete(expected, receipt.Component)
		receipts = append(receipts, receipt)
	}
	if len(expected) != 0 {
		return nil, errors.New("managed runtime generation is missing receipts listed in its manifest")
	}
	if len(receipts) == 0 {
		return nil, errors.New("managed runtime generation has no receipts")
	}
	return receipts, nil
}

func (m Manager) writeGenerationManifest(generation string, manifest GenerationManifest) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	path := filepath.Join(generation, "manifest.json")
	if err := m.Paths.ValidateOwned(path); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(path, append(data, '\n'), 0o600)
}

func (m Manager) readGenerationManifest(generation string) (GenerationManifest, error) {
	path := filepath.Join(generation, "manifest.json")
	if err := m.Paths.ValidateOwned(path); err != nil {
		return GenerationManifest{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return GenerationManifest{}, fmt.Errorf("managed runtime generation manifest is unavailable: %w", err)
	}
	var manifest GenerationManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return GenerationManifest{}, fmt.Errorf("decode managed runtime generation manifest: %w", err)
	}
	if manifest.Generation != filepath.Base(generation) {
		return GenerationManifest{}, errors.New("managed runtime generation manifest has a generation mismatch")
	}
	if err := manifest.Validate(); err != nil {
		return GenerationManifest{}, err
	}
	return manifest, nil
}

func (m Manager) verifyGenerationLaunchers(generation string, launchers []Launcher) error {
	if m.EnableLaunchers && len(launchers) == 0 {
		return errors.New("managed runtime generation has no launcher manifest")
	}
	launcherDirectory, err := m.Paths.launcherDirectory()
	if err != nil {
		return err
	}
	listed := make(map[string]struct{}, len(launchers))
	for _, launcher := range launchers {
		name, err := validateLauncherName(launcher.Name)
		if err != nil {
			return err
		}
		identity, err := normalizeLauncherClientIdentity(launcher.ClientIdentity)
		if err != nil {
			return fmt.Errorf("managed runtime launcher %s client identity: %w", launcher.Name, err)
		}
		path := filepath.Clean(launcher.Path)
		if !samePath(filepath.Dir(path), launcherDirectory) || filepath.Base(path) != launcherFilename(name) {
			return fmt.Errorf("managed runtime launcher %s is outside the managed launcher directory", name)
		}
		target, err := validateLauncherTarget(m.Paths, launcher.Target)
		if err != nil {
			return fmt.Errorf("managed runtime launcher %s target: %w", name, err)
		}
		if err := validateReceiptPathForGeneration(generation, target); err != nil {
			return fmt.Errorf("managed runtime launcher %s target: %w", name, err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("managed runtime launcher %s is unavailable: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("managed runtime launcher %s is not a regular file", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		expected := renderLauncher(target, identity, launcher.ManagedPath)
		if !strings.Contains(string(data), LauncherMarker) || !strings.Contains(string(data), target) || string(data) != string(expected) {
			return fmt.Errorf("managed runtime launcher %s is not bound to its verified target", name)
		}
		listed[path] = struct{}{}
	}
	entries, err := os.ReadDir(launcherDirectory)
	if errors.Is(err, os.ErrNotExist) {
		if len(launchers) == 0 {
			return nil
		}
		return errors.New("managed runtime launcher directory is unavailable")
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(launcherDirectory, entry.Name())
		info, lstatErr := os.Lstat(path)
		if lstatErr != nil {
			return lstatErr
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), LauncherMarker) {
			if _, ok := listed[path]; !ok {
				return fmt.Errorf("managed runtime launcher %s is not listed in the generation manifest", entry.Name())
			}
		}
	}
	return nil
}

func validateReceiptPathForGeneration(generation, installPath string) error {
	relative, err := filepath.Rel(generation, filepath.Clean(installPath))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("staged receipt install path escapes its generation")
	}
	return nil
}

func (m Manager) cachedArchive(component ComponentPlan) (string, bool, error) {
	if strings.TrimSpace(component.SHA256) == "" {
		return "", false, fmt.Errorf("managed runtime %s has no checksum for cache identity", component.ID)
	}
	path := filepath.Join(m.Paths.Cache, strings.ToLower(component.SHA256)+".archive")
	if err := m.Paths.ValidateOwned(path); err != nil {
		return "", false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", false, errors.New("managed runtime cache entry is not a regular file")
	}
	actual, _, hashErr := fileSHA256(path, m.maxDownloadBytes())
	if hashErr == nil && strings.EqualFold(actual, component.SHA256) {
		return path, true, nil
	}
	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return "", false, fmt.Errorf("remove invalid managed runtime cache entry: %w", removeErr)
	}
	return path, false, nil
}

func (m Manager) storeCachedArchive(source, destination string) error {
	if err := m.Paths.ValidateOwned(destination); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(m.Paths.Cache, ".cache-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	copied, copyErr := io.CopyN(temporary, input, m.maxDownloadBytes()+1)
	_ = input.Close()
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		return copyErr
	}
	if copied > m.maxDownloadBytes() {
		return errors.New("managed runtime cache entry exceeds the size limit")
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func removeStaleDownloads(generation string) error {
	matches, err := filepath.Glob(filepath.Join(generation, ".download-*"))
	if err != nil {
		return err
	}
	for _, path := range matches {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

var managedLauncherAliases = []struct {
	component ComponentID
	aliases   []string
}{
	{ComponentUV, []string{"uv", "uvx"}},
	{ComponentPython, []string{"python", "python3"}},
	{ComponentBun, []string{"bun"}},
	{ComponentGo, []string{"go"}},
	{ComponentNode, []string{"node"}},
	{ComponentNPM, []string{"npm", "npx"}},
	{ComponentPNPM, []string{"pnpm"}},
	{ComponentDSH, []string{"dsh"}},
	{ComponentCodex, []string{"codex"}},
}

func launcherSpecsForReceipts(receipts []Receipt) ([]LauncherSpec, error) {
	aliasesByComponent := make(map[ComponentID][]string, len(managedLauncherAliases))
	for _, item := range managedLauncherAliases {
		aliasesByComponent[item.component] = item.aliases
	}
	specs := make([]LauncherSpec, 0)
	managedPathByGeneration := make(map[string][]string)
	for _, receipt := range receipts {
		aliases, ok := aliasesByComponent[receipt.Component]
		if !ok {
			continue
		}
		generation := filepath.Dir(filepath.Clean(receipt.InstallPath))
		managedPath, ok := managedPathByGeneration[generation]
		if !ok {
			var pathErr error
			managedPath, pathErr = managedLauncherPathEntries(generation)
			if pathErr != nil {
				return nil, pathErr
			}
			managedPathByGeneration[generation] = managedPath
		}
		resolved := make(map[string]string, len(aliases))
		for _, alias := range aliases {
			target, err := FindExecutableNamed(receipt.InstallPath, alias)
			if err != nil {
				if receipt.Component == ComponentPython {
					continue
				}
				return nil, fmt.Errorf("resolve managed launcher %s for %s: %w", alias, receipt.Component, err)
			}
			resolved[alias] = target
		}
		if receipt.Component == ComponentPython {
			fallback := ""
			for _, alias := range aliases {
				if target := resolved[alias]; target != "" {
					fallback = target
					break
				}
			}
			if fallback == "" {
				return nil, fmt.Errorf("resolve managed Python launcher: no Python executable was found")
			}
			for _, alias := range aliases {
				if resolved[alias] == "" {
					resolved[alias] = fallback
				}
			}
		}
		for _, alias := range aliases {
			if target := resolved[alias]; target != "" {
				identity := ""
				if receipt.Component == ComponentDSH {
					identity = "dsh"
				} else if receipt.Component == ComponentCodex {
					identity = "codex"
				}
				specs = append(specs, LauncherSpec{Name: alias, Target: target, ClientIdentity: identity, ManagedPath: managedPath})
			}
		}
	}
	return specs, nil
}

func managedLauncherPathEntries(generation string) ([]string, error) {
	generation, err := filepath.Abs(filepath.Clean(generation))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(generation)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("managed runtime generation is not a directory")
	}
	entries := []string{generation}
	for _, component := range []ComponentID{ComponentUV, ComponentPython, ComponentStrix, ComponentBun, ComponentGo, ComponentNode, ComponentNPM, ComponentPNPM, ComponentDSH, ComponentCodex} {
		root := filepath.Join(generation, string(component))
		if rootInfo, statErr := os.Stat(root); statErr == nil && rootInfo.IsDir() {
			entries = append(entries, root, filepath.Join(root, "bin"), filepath.Join(root, "Scripts"))
		}
	}
	entries = append(entries, managedExecutableDirectories(generation)...)
	return normalizeLauncherManagedPath(entries)
}

func readOptionalFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func restoreOptionalFiles(firstPath string, firstData []byte, firstExists bool, secondPath string, secondData []byte, secondExists bool) error {
	var firstErr error
	restore := func(path string, data []byte, exists bool) {
		if firstErr != nil {
			return
		}
		if exists {
			firstErr = config.AtomicWriteFile(path, data, 0o600)
			return
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			firstErr = err
		}
	}
	restore(firstPath, firstData, firstExists)
	restore(secondPath, secondData, secondExists)
	return firstErr
}

func (m Manager) activate(planID, generation string, now time.Time) error {
	if _, err := m.Paths.Generation(generation); err != nil {
		return err
	}
	if current, err := os.ReadFile(m.Paths.Current); err == nil {
		if err := config.AtomicWriteFile(m.Paths.Previous, current, 0o600); err != nil {
			return fmt.Errorf("retain previous managed runtime generation: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read current managed runtime generation: %w", err)
	}
	state := activationState{PlanID: planID, Generation: generation, ActivatedAt: now.UTC()}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(m.Paths.Current, append(data, '\n'), 0o600)
}

func (m Manager) readActivation(generation string) (activationState, error) {
	if strings.TrimSpace(generation) == "" {
		data, err := os.ReadFile(m.Paths.Current)
		if err != nil {
			return activationState{}, err
		}
		return decodeActivation(data)
	}
	if _, err := m.Paths.Generation(generation); err != nil {
		return activationState{}, err
	}
	return activationState{Generation: generation}, nil
}

func (m Manager) verifyGenerationDirectory(generation string) error {
	path, err := m.Paths.Generation(generation)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("managed runtime generation is not a directory")
	}
	return nil
}

func (m Manager) writeOperation(state operationState) error {
	path := filepath.Join(m.Paths.Operations, state.PlanID+".json")
	if err := m.Paths.ValidateOwned(path); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(path, append(data, '\n'), 0o600)
}

func (m Manager) maxDownloadBytes() int64 {
	if m.MaxDownloadBytes > 0 {
		return m.MaxDownloadBytes
	}
	return managerDefaultMaxDownloadBytes
}

func (m Manager) now() time.Time {
	if m.Clock != nil {
		return m.Clock().UTC()
	}
	return time.Now().UTC()
}

func orderComponents(components []ComponentPlan) ([]ComponentPlan, error) {
	byID := make(map[ComponentID]ComponentPlan, len(components))
	for _, component := range components {
		byID[component.ID] = component
	}
	ordered := make([]ComponentPlan, 0, len(components))
	visiting := map[ComponentID]bool{}
	visited := map[ComponentID]bool{}
	var visit func(ComponentID) error
	visit = func(id ComponentID) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("managed runtime dependency cycle includes %s", id)
		}
		component, ok := byID[id]
		if !ok {
			return fmt.Errorf("managed runtime dependency %s is missing", id)
		}
		visiting[id] = true
		for _, dependency := range component.Dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, id)
		visited[id] = true
		ordered = append(ordered, component)
		return nil
	}
	for _, component := range components {
		if err := visit(component.ID); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func decodeActivation(data []byte) (activationState, error) {
	var state activationState
	if err := json.Unmarshal(data, &state); err != nil {
		return activationState{}, fmt.Errorf("decode managed runtime activation: %w", err)
	}
	if strings.TrimSpace(state.Generation) == "" {
		return activationState{}, errors.New("managed runtime activation has no generation")
	}
	return state, nil
}

func fileSHA256(path string, maxBytes int64) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.CopyN(hash, file, maxBytes+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", written, err
	}
	if written > maxBytes {
		return "", written, errors.New("download exceeds the size limit")
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

func extractOrInstall(source, rawURL, destination string, maxBytes int64) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	name := strings.ToLower(parsed.Path)
	switch {
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		return extractTarGzip(source, destination, maxBytes)
	case strings.HasSuffix(name, ".tar"):
		return extractTar(source, destination, maxBytes)
	case strings.HasSuffix(name, ".zip"):
		return extractZip(source, destination, maxBytes)
	default:
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		return config.AtomicWriteFile(filepath.Join(destination, "payload"), data, 0o700)
	}
}

func extractTarGzip(source, destination string, maxBytes int64) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer compressed.Close()
	return extractTarReader(tar.NewReader(compressed), destination, maxBytes)
}

func extractTar(source, destination string, maxBytes int64) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	return extractTarReader(tar.NewReader(file), destination, maxBytes)
}

func extractTarReader(reader *tar.Reader, destination string, maxBytes int64) error {
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		path, err := safeArchivePath(destination, header.Name)
		if err != nil {
			return fmt.Errorf("archive path: %w", err)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxBytes || total > maxBytes-header.Size {
				return errors.New("archive exceeds the size limit")
			}
			if err := writeArchiveFile(path, io.LimitReader(reader, header.Size), header.Size, 0o700); err != nil {
				return err
			}
			total += header.Size
		default:
			return errors.New("archive contains an unsupported link or special entry")
		}
	}
}

func extractZip(source, destination string, maxBytes int64) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	archive, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer archive.Close()
	var total int64
	for _, entry := range archive.File {
		path, err := safeArchivePath(destination, entry.Name)
		if err != nil {
			return fmt.Errorf("archive path: %w", err)
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 {
			return errors.New("archive contains an unsupported link or special entry")
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
			continue
		}
		if entry.UncompressedSize64 > uint64(maxBytes) || total > maxBytes-int64(entry.UncompressedSize64) {
			return errors.New("archive exceeds the size limit")
		}
		file, err := entry.Open()
		if err != nil {
			return err
		}
		writeErr := writeArchiveFile(path, io.LimitReader(file, int64(entry.UncompressedSize64)), int64(entry.UncompressedSize64), 0o700)
		_ = file.Close()
		if writeErr != nil {
			return writeErr
		}
		total += int64(entry.UncompressedSize64)
	}
	_ = info
	return nil
}

func writeArchiveFile(path string, source io.Reader, expected int64, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, source)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != expected {
		return errors.New("archive entry size mismatch")
	}
	return nil
}

func safeArchivePath(root, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", errors.New("archive contains an absolute path")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("archive path escapes destination")
	}
	path := filepath.Join(root, clean)
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", errors.New("archive path escapes destination")
	}
	return path, nil
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return config.Redact(err.Error(), nil)
}
