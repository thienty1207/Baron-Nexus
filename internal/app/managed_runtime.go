package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/doctor"
	"github.com/baron-shared-brain/baron/internal/install"
	"github.com/baron-shared-brain/baron/internal/managedruntime"
	"github.com/baron-shared-brain/baron/internal/project"
	"github.com/baron-shared-brain/baron/internal/version"
)

const defaultManagedRuntimeCatalogURL = "https://github.com/thienty1207/Baron-Nexus/releases/latest/download/managed-runtime-catalog.json"

var defaultManagedRuntimeComponents = []managedruntime.ComponentID{
	managedruntime.ComponentUV,
	managedruntime.ComponentPython,
	managedruntime.ComponentStrix,
	managedruntime.ComponentBun,
	managedruntime.ComponentGo,
	managedruntime.ComponentNode,
	managedruntime.ComponentNPM,
	managedruntime.ComponentPNPM,
	managedruntime.ComponentDSH,
	managedruntime.ComponentCodex,
}

// configureDefaultManagedRuntimeCoordinator installs the production wiring
// without doing network or filesystem mutation. The resolver only fetches the
// catalog when install/update explicitly executes it; tests can replace both
// exported coordinator fields after New().
func (a *App) configureDefaultManagedRuntimeCoordinator() error {
	globalPath, err := a.globalPath()
	if err != nil {
		return err
	}
	paths, err := managedruntime.ResolvePaths(filepath.Join(filepath.Dir(globalPath), "runtimes"))
	if err != nil {
		return err
	}
	if executable := a.executablePathForPermissions(); executable != "" {
		candidate := paths
		candidate.LauncherDirectory = filepath.Dir(executable)
		if _, launcherErr := candidate.LauncherDirectoryPath(); launcherErr == nil {
			paths = candidate
		}
	}
	client := a.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Minute}
	} else {
		clone := *client
		if clone.Timeout < 15*time.Minute {
			clone.Timeout = 15 * time.Minute
		}
		client = &clone
	}
	catalogURL := strings.TrimSpace(os.Getenv("BARON_RUNTIME_CATALOG_URL"))
	if catalogURL == "" {
		catalogURL = strings.TrimSpace(a.ManagedRuntimeCatalogURL)
	}
	if catalogURL == "" {
		catalogURL = defaultManagedRuntimeCatalogURL
	}
	catalogURLs := make(map[managedruntime.ComponentID]string, len(defaultManagedRuntimeComponents))
	for _, component := range defaultManagedRuntimeComponents {
		catalogURLs[component] = catalogURL
	}
	resolver := managedruntime.Resolver{
		HTTP: client, Metadata: managedruntime.HTTPMetadataClient{HTTP: client},
		Platform: runtime.GOOS, Architecture: runtime.GOARCH,
		TestedMatrix: managedruntime.CompatibilityMatrix{
			MinPythonMajor: 3, MinPythonMinor: 12, MaxPythonMajor: 3, MaxPythonMinor: 14,
		},
		CatalogURLs: catalogURLs, MetadataCache: &managedruntime.MetadataCache{Entries: map[string][]byte{}},
	}
	a.ManagedRuntimeCatalogURL = catalogURL
	a.ManagedRuntimeManager = &managedruntime.Manager{
		Paths: paths, Downloader: managedruntime.HTTPDownloader{HTTP: client}, Probe: managedruntime.NativeProbe{}, Installer: managedruntime.NativeComponentInstaller{}, EnableLaunchers: true,
	}
	a.ManagedRuntimePlanResolver = func(ctx context.Context, reporter install.ProgressReporter) (managedruntime.ResolutionPlan, error) {
		if reporter != nil {
			reporter.Step("Resolving the latest compatible managed runtime bundle...")
		}
		return resolver.Resolve(ctx, managedruntime.ResolverInput{
			Platform: runtime.GOOS, Architecture: runtime.GOARCH,
			Components:            append([]managedruntime.ComponentID(nil), defaultManagedRuntimeComponents...),
			CompatibilityVersion:  version.Value,
			Offline:               strings.TrimSpace(os.Getenv("BARON_OFFLINE")) == "1",
			RequireCompleteBundle: true,
		})
	}
	return nil
}

// bootstrapPlanFromManagedRuntime converts the immutable runtime resolution
// into the existing DSH/Codex mutation contract. It deliberately copies the
// resolved version into both local and latest fields so those initializers do
// not query npm again during the same operation.
func bootstrapPlanFromManagedRuntime(plan managedruntime.ResolutionPlan) (BootstrapPlan, error) {
	if err := plan.Validate(); err != nil {
		return BootstrapPlan{}, err
	}
	components := make(map[string]install.ComponentState, 2)
	for _, component := range plan.Components {
		var name string
		switch component.ID {
		case managedruntime.ComponentDSH:
			name = "dsh"
		case managedruntime.ComponentCodex:
			name = "codex"
		default:
			continue
		}
		version, err := install.NormalizeVersion(component.Version)
		if err != nil {
			return BootstrapPlan{}, fmt.Errorf("managed %s version: %w", name, err)
		}
		components[name] = install.ComponentState{
			Name:          name,
			Installed:     true,
			LocalVersion:  version,
			LatestVersion: version,
			NeedsUpdate:   false,
		}
	}
	for _, name := range []string{"dsh", "codex"} {
		if _, ok := components[name]; !ok {
			return BootstrapPlan{}, fmt.Errorf("managed runtime plan is missing %s", name)
		}
	}
	return BootstrapPlan{Components: components}, nil
}

func (a *App) managedRuntimeConfigured() error {
	if a.ManagedRuntimePlanResolver == nil && a.ManagedRuntimeManager == nil {
		return nil
	}
	if a.ManagedRuntimePlanResolver == nil || a.ManagedRuntimeManager == nil {
		return errors.New("managed runtime coordinator is only partially configured")
	}
	return nil
}

func (a *App) resolveAndApplyManagedRuntimePlan(ctx context.Context, reporter install.ProgressReporter) (managedruntime.ResolutionPlan, managedruntime.OperationReport, error) {
	if err := a.managedRuntimeConfigured(); err != nil {
		return managedruntime.ResolutionPlan{}, managedruntime.OperationReport{}, err
	}
	if a.ManagedRuntimePlanResolver == nil || a.ManagedRuntimeManager == nil {
		return managedruntime.ResolutionPlan{}, managedruntime.OperationReport{}, errors.New("managed runtime coordinator is not configured")
	}
	plan, err := a.ManagedRuntimePlanResolver(ctx, reporter)
	if err != nil {
		return managedruntime.ResolutionPlan{}, managedruntime.OperationReport{}, fmt.Errorf("resolve managed runtime plan: %w", err)
	}
	if err := plan.Validate(); err != nil {
		return managedruntime.ResolutionPlan{}, managedruntime.OperationReport{}, fmt.Errorf("validate managed runtime plan: %w", err)
	}
	reportProgressStep(reporter, fmt.Sprintf("Resolved managed runtime plan %s with %d components.", plan.ID, len(plan.Components)))
	manager := *a.ManagedRuntimeManager
	manager.Progress = reporter
	report, err := manager.Apply(ctx, plan)
	if err != nil {
		return managedruntime.ResolutionPlan{}, managedruntime.OperationReport{}, err
	}
	if err := a.persistManagedRuntimeState(plan, report, manager.Paths); err != nil {
		rollbackErr := manager.Rollback(ctx, "")
		return managedruntime.ResolutionPlan{}, managedruntime.OperationReport{}, combineManagedRuntimeFailure("persist managed runtime activation", err, rollbackErr)
	}
	reportProgressStep(reporter, fmt.Sprintf("Managed runtime generation %s activated.", report.Generation))
	return plan, report, nil
}

// applyManagedRuntimePlan is intentionally small and testable: a coordinator
// resolves once, stages once, and persists only non-secret activation data.
func (a *App) applyManagedRuntimePlan(ctx context.Context, reporter install.ProgressReporter) (managedruntime.OperationReport, error) {
	_, report, err := a.resolveAndApplyManagedRuntimePlan(ctx, reporter)
	return report, err
}

func (a *App) persistManagedRuntimeState(plan managedruntime.ResolutionPlan, report managedruntime.OperationReport, paths managedruntime.Paths) error {
	if err := paths.ValidateOwned(paths.Root); err != nil {
		return err
	}
	global, path, err := a.loadGlobal()
	if err != nil {
		return err
	}
	previous := ""
	if global.ManagedRuntime != nil {
		previous = global.ManagedRuntime.CurrentGeneration
	}
	launcherDirectory, err := paths.LauncherDirectoryPath()
	if err != nil {
		return err
	}
	receipts := make([]string, 0, len(report.Receipts))
	for _, receipt := range report.Receipts {
		if err := receipt.Validate(); err != nil {
			return err
		}
		receiptPath := filepath.Join(paths.Receipts, report.Generation+"-"+string(receipt.Component)+".json")
		if err := paths.ValidateOwned(receiptPath); err != nil {
			return err
		}
		receipts = append(receipts, receiptPath)
	}
	launchers := make([]string, 0, len(report.Launchers))
	for _, launcher := range report.Launchers {
		if err := paths.ValidateLauncherPath(launcher.Path); err != nil {
			return err
		}
		launchers = append(launchers, filepath.Clean(launcher.Path))
	}
	global.ManagedRuntime = &config.ManagedRuntimeState{
		Root: paths.Root, CurrentGeneration: report.Generation, PreviousGeneration: previous,
		PlanID: plan.ID, Receipts: receipts, LauncherDirectory: launcherDirectory,
		Launchers: launchers, RestartRequired: report.RestartRequired,
	}
	return a.saveGlobal(path, global)
}

func combineManagedRuntimeFailure(operation string, operationErr, rollbackErr error) error {
	if rollbackErr == nil {
		return fmt.Errorf("%s: %w; managed runtime generation rolled back", operation, operationErr)
	}
	return fmt.Errorf("%s: %w; managed runtime rollback failed: %v", operation, operationErr, rollbackErr)
}

func (a *App) installFullBundle(ctx context.Context, reporter install.ProgressReporter) (string, error) {
	if a.managedRuntimeDefault {
		if err := a.configureDefaultManagedRuntimeCoordinator(); err != nil {
			return "", err
		}
	}
	if err := a.managedRuntimeConfigured(); err != nil {
		return "", err
	}
	if a.ManagedRuntimePlanResolver == nil && a.ManagedRuntimeManager == nil {
		return a.installAndBootstrap(ctx, reporter)
	}
	return a.runFullBundle(ctx, reporter, "install")
}

func (a *App) updateFullBundle(ctx context.Context, reporter install.ProgressReporter) (string, error) {
	if a.managedRuntimeDefault {
		if err := a.configureDefaultManagedRuntimeCoordinator(); err != nil {
			return "", err
		}
	}
	if err := a.managedRuntimeConfigured(); err != nil {
		return "", err
	}
	if a.ManagedRuntimePlanResolver == nil && a.ManagedRuntimeManager == nil {
		return a.installBaronBinary(false, reporter)
	}
	return a.runFullBundle(ctx, reporter, "update")
}

func (a *App) runFullBundle(ctx context.Context, reporter install.ProgressReporter, operation string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timer := newBootstrapTimer(reporter)
	defer timer.finish()
	reportProgressStep(reporter, fmt.Sprintf("Starting Baron %s; managed dependencies may take several minutes.", operation))
	if err := a.preflightBootstrap(ctx, reporter); err != nil {
		return "", err
	}
	timer.mark("preflight")
	plan, runtimeReport, err := a.resolveAndApplyManagedRuntimePlan(ctx, reporter)
	if err != nil {
		return "", err
	}
	timer.mark("managed runtime")
	bootstrapPlan, err := bootstrapPlanFromManagedRuntime(plan)
	if err != nil {
		rollbackErr := a.ManagedRuntimeManager.Rollback(ctx, "")
		return "", combineManagedRuntimeFailure("prepare managed bootstrap", err, rollbackErr)
	}
	if err := runBootstrap(ctx, BootstrapSteps{
		Preflight: func(context.Context) error { return nil },
		DSH:       func() error { return a.dshInitWithPlan(ctx, bootstrapPlan, reporter) },
		Codex:     func() error { return a.codexInitWithPlan(ctx, bootstrapPlan, reporter) },
		Tencent:   a.TencentInit,
		Setup:     a.refreshCurrentProject,
		Progress:  reporter,
		Timer:     timer,
	}); err != nil {
		rollbackErr := a.ManagedRuntimeManager.Rollback(ctx, "")
		return "", combineManagedRuntimeFailure("run managed bootstrap", err, rollbackErr)
	}
	timer.mark("bootstrap")
	// The Baron executable is deliberately replaced last. A failed binary
	// validation therefore cannot leave a new runtime generation advertised as
	// fully active without a rollback attempt.
	releaseMessage, err := a.installBaronBinary(false, reporter)
	if err != nil {
		rollbackErr := a.ManagedRuntimeManager.Rollback(ctx, "")
		return "", combineManagedRuntimeFailure("install Baron binary", err, rollbackErr)
	}
	timer.mark("Baron")
	return fmt.Sprintf("%s Managed runtime plan %s is active at generation %s. Bootstrap complete.", releaseMessage, runtimeReport.PlanID, runtimeReport.Generation), nil
}

// refreshCurrentProject intentionally touches only the project resolved from
// the current working directory. Other registered projects are left for their
// next managed launch or an explicit `baron setup`.
func (a *App) refreshCurrentProject(ctx context.Context) error {
	if _, err := project.Resolve(""); err != nil {
		return nil
	}
	_, err := a.SetupProject(ctx, "")
	return err
}

func (a *App) managedRuntimeReadiness(ctx context.Context) []doctor.CheckResult {
	if a.ManagedRuntimeManager == nil {
		return nil
	}
	if check, blocked := managedRuntimeHostCheck(runtime.GOOS); blocked {
		return []doctor.CheckResult{check}
	}
	global, _, err := a.loadGlobal()
	if err != nil {
		return []doctor.CheckResult{{Name: "managed-runtime", Status: doctor.StatusIncomplete, Message: "Managed runtime state could not be read safely.", Suggestion: "baron install"}}
	}
	if global.ManagedRuntime == nil || strings.TrimSpace(global.ManagedRuntime.CurrentGeneration) == "" {
		return []doctor.CheckResult{{Name: "managed-runtime", Status: doctor.StatusIncomplete, Message: "Baron managed runtime has not been activated.", Suggestion: "baron install"}}
	}
	manager, managerErr := a.managedRuntimeManagerForState(*global.ManagedRuntime)
	if managerErr != nil {
		return []doctor.CheckResult{{Name: "managed-runtime", Status: doctor.StatusIncomplete, Message: "Baron managed runtime state is unsafe or unavailable.", Suggestion: "baron repair"}}
	}
	if err := manager.Verify(ctx, global.ManagedRuntime.CurrentGeneration); err != nil {
		return []doctor.CheckResult{{Name: "managed-runtime", Status: doctor.StatusIncomplete, Message: "Baron managed runtime generation is unavailable.", Suggestion: "baron repair"}}
	}
	return []doctor.CheckResult{{Name: "managed-runtime", Status: doctor.StatusReady, Message: "Baron managed runtime generation is active and verified."}}
}

func managedRuntimeHostCheck(goos string) (doctor.CheckResult, bool) {
	if err := validateStrixExecutionHost(goos); err != nil {
		return doctor.CheckResult{
			Name:       "managed-runtime",
			Status:     doctor.StatusIncomplete,
			Message:    "Baron managed runtime cannot advertise Strix readiness: " + err.Error(),
			Suggestion: "verify the Ubuntu WSL2 + Docker bridge, then rerun baron doctor",
		}, true
	}
	return doctor.CheckResult{}, false
}

// managedRuntimeManagerForState binds verification to the persisted runtime
// root instead of trusting a manager initialized before a custom global path
// or launcher directory was loaded. The configured manager is copied so the
// app's dependency injection remains unchanged for other operations.
func (a *App) managedRuntimeManagerForState(state config.ManagedRuntimeState) (*managedruntime.Manager, error) {
	if a.ManagedRuntimeManager == nil {
		return nil, errors.New("managed runtime manager is not configured")
	}
	paths, err := managedruntime.ResolvePaths(state.Root)
	if err != nil {
		return nil, err
	}
	if launcherDirectory := strings.TrimSpace(state.LauncherDirectory); launcherDirectory != "" {
		paths.LauncherDirectory = filepath.Clean(launcherDirectory)
	}
	manager := *a.ManagedRuntimeManager
	manager.Paths = paths
	return &manager, nil
}
