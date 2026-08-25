package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/cli"
	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/continuity"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/credentials"
	"github.com/baron-shared-brain/baron/internal/doctor"
	"github.com/baron-shared-brain/baron/internal/hooks"
	"github.com/baron-shared-brain/baron/internal/install"
	"github.com/baron-shared-brain/baron/internal/knowledge"
	"github.com/baron-shared-brain/baron/internal/memory/tencent"
	"github.com/baron-shared-brain/baron/internal/project"
	"github.com/baron-shared-brain/baron/internal/release"
	"github.com/baron-shared-brain/baron/internal/storage"
	"github.com/baron-shared-brain/baron/internal/version"
)

type App struct {
	GlobalPath         string
	HTTPClient         *http.Client
	ProjectProvisioner func(context.Context, string, string) (contracts.ProjectBinding, error)
	// TencentRestore is an injectable restore boundary for acceptance fixtures.
	// The default path performs the real Docker/service/identity verification;
	// tests may replace it to prove staging order without contacting Tencent.
	TencentRestore TencentRestoreFunc
	CommandRunner  install.CommandRunner
	Input          io.Reader
	PromptOutput   io.Writer
	ReadSecret     credentials.SecretReader
	ReadLine       credentials.LineReader
	ReleaseClient  *release.Client
	ExecutablePath string
}

func (a *App) CLIOptions(out, errOut io.Writer) cli.Options {
	return cli.Options{
		Version: version.Value,
		Out:     out, Err: errOut, In: a.Input,
		Setup: func(path string) error {
			_, err := a.SetupProject(context.Background(), path)
			return classifyError(err)
		},
		TestOutput:   a.testOutput,
		StatusOutput: a.statusOutput,
		DoctorOutput: a.doctorOutput,
		Repair:       func() error { return classifyError(a.Repair()) },
		Backup:       func(destination string) error { return classifyError(a.Backup(context.Background(), destination)) },
		Restore:      func(archive string) error { return classifyError(a.Restore(context.Background(), archive)) },
		RestoreWithOptions: func(archive string, replaceExisting bool) error {
			return classifyError(a.restoreWithOptions(context.Background(), archive, replaceExisting))
		},
		Install: func() (string, error) {
			message, err := a.installAndBootstrap(context.Background())
			return message, classifyError(err)
		},
		Update: func() (string, error) { return a.installBaronBinary(false) },
		Hook: func(client, event string, input io.Reader, output io.Writer) error {
			return a.HandleHook(context.Background(), client, event, "", input, output)
		},
		Init: map[string]func() error{
			"deepseek-harness": func() error { return classifyError(a.DSHInit()) },
			"codex-cli":        func() error { return classifyError(a.CodexInit()) },
			"tencent-memory":   func() error { return classifyError(a.TencentInit(context.Background())) },
		},
	}
}

func (a *App) installBaronBinary(force bool) (string, error) {
	target := a.ExecutablePath
	if target == "" {
		var err error
		target, err = release.CurrentExecutablePath()
		if err != nil {
			return "", err
		}
	}
	client := release.Client{
		HTTPClient: a.HTTPClient,
		Repository: os.Getenv("BARON_RELEASE_REPOSITORY"),
		APIBaseURL: os.Getenv("BARON_RELEASE_API_BASE_URL"),
	}
	if a.ReleaseClient != nil {
		client = *a.ReleaseClient
		if client.HTTPClient == nil {
			client.HTTPClient = a.HTTPClient
		}
	}
	report, err := client.InstallLatest(context.Background(), target, version.Value, force)
	if err != nil {
		return "", classifyError(err)
	}
	if !report.Changed {
		return fmt.Sprintf("Baron is already up to date at version %s.", report.Version), nil
	}
	return fmt.Sprintf("Baron %s installed at %s.", report.Version, report.Target), nil
}

func (a *App) restoreWithOptions(ctx context.Context, archive string, replaceExisting bool) error {
	target, err := config.GlobalConfigDir("baron")
	if err != nil {
		return err
	}
	return a.RestoreArchiveWithOptions(ctx, archive, target, RestoreOptions{ReplaceExisting: replaceExisting})
}

func (a *App) testOutput(jsonOutput bool) (string, error) {
	report, err := a.readinessReport()
	if err != nil {
		return "", err
	}
	return renderReport(report, jsonOutput)
}

func (a *App) doctorOutput(jsonOutput bool) (string, error) {
	report, err := a.readinessReport()
	if err != nil {
		return "", err
	}
	if _, err := project.Resolve(""); err != nil {
		report.Ready = false
		report.Checks = append(report.Checks, doctor.CheckResult{Name: "project-mapping", Status: doctor.StatusIncomplete, Message: "Current directory is not a Baron project.", Suggestion: "baron setup"})
		if report.ExitCode == 0 {
			report.ExitCode = cli.ExitProjectNotInitialized
		}
	} else {
		report.Checks = append(report.Checks, doctor.CheckResult{Name: "project-mapping", Status: doctor.StatusReady, Message: "Current project identity and local mapping are readable."})
	}
	return renderReport(report, jsonOutput)
}

func renderReport(report doctor.Report, jsonOutput bool) (string, error) {
	var output string
	if jsonOutput {
		var buffer bytes.Buffer
		if err := report.WriteJSON(&buffer); err != nil {
			return "", err
		}
		output = buffer.String()
	} else {
		output = report.Human()
	}
	if !report.Ready {
		code := report.ExitCode
		if code == 0 {
			code = cli.ExitPartialResult
		}
		return output, &cli.ExitError{Code: code, Err: errors.New("readiness checks are incomplete")}
	}
	return output, nil
}

func (a *App) readinessReport() (doctor.Report, error) {
	global, globalPath, err := a.loadGlobal()
	if err != nil {
		return doctor.Report{}, err
	}
	credentialPaths := []string{globalPath}
	projectRoot := codexTrustProjectRoot()
	if resolved, resolveErr := project.Resolve(""); resolveErr == nil {
		credentialPaths = append(credentialPaths, resolved.EnvPath)
		projectRoot = resolved.Root
	}
	surfaceChecks := a.knowledgeSurfaceChecks(context.Background(), global)
	dshKey, dshKeyErr := install.ReadDSHProviderKey(processEnvironment())
	codexHooksPath := global.CodexHooksPath
	if canonicalPath, pathErr := install.CodexHooksPath(); pathErr == nil {
		// CODEX_HOME/~/.codex is authoritative for Codex hooks. A path saved
		// by an older Baron release under ~/.config/codex must not make the
		// readiness report claim that live Codex hooks are installed.
		codexHooksPath = canonicalPath
	}
	return doctor.Check(context.Background(), doctor.Options{
		DSHComponents:       global.DSHComponents,
		DSHProviderChecked:  true,
		DSHProviderReady:    dshKeyErr == nil && dshKey != "",
		CodexAuthenticated:  codexAuthReady(),
		CodexProjectTrusted: install.CodexProjectTrusted(projectRoot),
		TencentEndpoint:     global.Identity.Endpoint,
		HubEndpoint:         global.Identity.HubEndpoint,
		KnowledgeEndpoint:   global.Identity.KnowledgeEndpoint,
		ProxyEndpoint:       os.Getenv("BARON_TENCENT_PROXY_ENDPOINT"),
		CredentialPaths:     credentialPaths,
		CodexHooksPath:      codexHooksPath,
		SurfaceChecks:       surfaceChecks,
		HTTPClient:          a.HTTPClient,
		LinuxBootstrap:      runtime.GOOS == "linux",
	}), nil
}

func codexTrustProjectRoot() string {
	if resolved, err := project.Resolve(""); err == nil {
		return resolved.Root
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		return resolved
	}
	return cwd
}

func (a *App) knowledgeSurfaceChecks(ctx context.Context, global config.GlobalState) []doctor.CheckResult {
	if global.Identity.KnowledgeEndpoint == "" {
		return nil
	}
	resolved, err := project.Resolve("")
	if err != nil {
		return nil
	}
	store, err := storage.Open(filepath.Join(resolved.Root, ".baron", "runtime", "state.db"))
	if err != nil {
		return nil
	}
	defer store.Close()
	registry, registryErr := store.GetKnowledgeRegistry(ctx, resolved.ProjectID)
	isolation := resolved.IsolationContext()
	isolation.TeamID = firstNonEmptyString(isolation.TeamID, global.Identity.TeamID)
	isolation.UserID = firstNonEmptyString(isolation.UserID, global.Identity.UserID)
	client := tencent.NewKnowledgeClient(tencent.Config{Endpoint: global.Identity.Endpoint, KnowledgeEndpoint: global.Identity.KnowledgeEndpoint, UserKey: global.Identity.UserKey, ServiceID: global.Identity.ServiceID, HTTPClient: a.HTTPClient})
	checks := make([]doctor.CheckResult, 0, 3)
	if registryErr != nil || registry.WikiID == "" {
		checks = append(checks, doctor.CheckResult{Name: "tencent-wiki", Status: doctor.StatusIncomplete, Message: "Baron Wiki asset is not provisioned for this project.", Suggestion: "baron setup"})
	} else if _, checkErr := client.GetWiki(ctx, isolation, registry.WikiID); checkErr != nil {
		checks = append(checks, knowledgeSurfaceCheck("tencent-wiki", checkErr))
	} else {
		checks = append(checks, doctor.CheckResult{Name: "tencent-wiki", Status: doctor.StatusReady, Message: "Tencent Wiki asset is reachable."})
	}
	if registryErr != nil || registry.CodeGraphID == "" {
		if registryErr == nil && strings.EqualFold(registry.CodeGraphStatus, "local_only") {
			checks = append(checks, doctor.CheckResult{Name: "tencent-codegraph", Status: doctor.StatusReady, Message: "CodeGraph is local-only because this project has no verified Git remote."})
		} else {
			checks = append(checks, doctor.CheckResult{Name: "tencent-codegraph", Status: doctor.StatusIncomplete, Message: "Baron CodeGraph asset is not provisioned for this project.", Suggestion: "baron setup"})
		}
	} else if _, checkErr := client.StatusCodeGraph(ctx, isolation, registry.CodeGraphID); checkErr != nil {
		checks = append(checks, knowledgeSurfaceCheck("tencent-codegraph", checkErr))
	} else {
		checks = append(checks, doctor.CheckResult{Name: "tencent-codegraph", Status: doctor.StatusReady, Message: "Tencent CodeGraph status endpoint is reachable."})
	}
	knowledgeID := ""
	if registryErr == nil {
		knowledgeID = firstNonEmptyString(registry.WikiID, registry.CodeGraphID)
	}
	if knowledgeID == "" {
		checks = append(checks, doctor.CheckResult{Name: "tencent-tools", Status: doctor.StatusReady, Message: "Tencent Knowledge tools discovery is deferred until a Wiki or CodeGraph asset exists."})
	} else if _, checkErr := client.ListTools(ctx, isolation, knowledgeID); checkErr != nil {
		checks = append(checks, knowledgeSurfaceCheck("tencent-tools", checkErr))
	} else {
		checks = append(checks, doctor.CheckResult{Name: "tencent-tools", Status: doctor.StatusReady, Message: "Tencent Knowledge tools discovery is reachable."})
	}
	return checks
}

func knowledgeSurfaceCheck(name string, err error) doctor.CheckResult {
	message := config.Redact(err.Error(), nil)
	status := doctor.StatusUnavailable
	suggestion := "baron tencent-memory init"
	if strings.Contains(message, "HTTP 404") || strings.Contains(message, "not configured") {
		status = doctor.StatusMissing
	}
	return doctor.CheckResult{Name: name, Status: status, Message: "Tencent surface check failed: " + message, Suggestion: suggestion}
}

func codexAuthReady() bool {
	if os.Getenv("BARON_CODEX_AUTH_READY") == "1" || os.Getenv("OPENAI_API_KEY") != "" {
		return true
	}
	if codexLoginStatusReady() {
		return true
	}
	return codexAuthFileReady()
}

func codexLoginStatusReady() bool {
	binary, err := exec.LookPath("codex")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "login", "status")
	command.Stdin = nil
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run() == nil
}

func codexAuthFileReady() bool {
	var candidates []string
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		candidates = append(candidates, filepath.Join(codexHome, "auth.json"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".codex", "auth.json"))
	}
	if configDir, err := os.UserConfigDir(); err == nil {
		candidates = append(candidates, filepath.Join(configDir, "codex", "auth.json"))
	}
	for _, path := range candidates {
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return true
		}
	}
	return false
}

func (a *App) statusOutput(jsonOutput bool) (string, error) {
	resolved, err := project.Resolve("")
	if err != nil {
		return "", &cli.ExitError{Code: cli.ExitProjectNotInitialized, Err: errors.New("project is not initialized; run baron setup")}
	}
	if err := a.validateProjectBinding(resolved); err != nil {
		return "", err
	}
	result := map[string]any{
		"project_id": resolved.ProjectID, "project_name": resolved.Metadata.Name, "root": resolved.Root,
		"team_id": resolved.Binding.TeamID, "agent_id": resolved.Binding.AgentID,
		"remote_bound": resolved.Binding.TeamID != "" && resolved.Binding.AgentID != "",
	}
	if global, _, globalErr := a.loadGlobal(); globalErr == nil && global.TencentInstallPath != "" {
		if manifest, manifestErr := install.ReadTencentDeploymentManifest(global.TencentInstallPath); manifestErr == nil {
			result["tencent_deployment"] = map[string]any{
				"repository":              manifest.Repository,
				"requested_ref":           manifest.RequestedRef,
				"resolved_commit":         manifest.ResolvedCommit,
				"container_image_digests": manifest.ContainerImageDigests,
				"unresolved_containers":   manifest.UnresolvedContainers,
				"updated_at":              manifest.UpdatedAt,
			}
		}
	}
	if store, storeErr := storage.Open(filepath.Join(resolved.Root, ".baron", "runtime", "state.db")); storeErr == nil {
		defer store.Close()
		if registry, registryErr := store.GetKnowledgeRegistry(context.Background(), resolved.ProjectID); registryErr == nil {
			pending, _ := store.QueueCount(context.Background(), resolved.ProjectID, "pending")
			deadLetter, _ := store.QueueCount(context.Background(), resolved.ProjectID, "dead_letter")
			result["knowledge"] = map[string]any{
				"wiki_id": registry.WikiID, "code_graph_id": registry.CodeGraphID,
				"wiki_status": registry.WikiStatus, "code_graph_status": registry.CodeGraphStatus,
				"wiki_ingest_status": registry.WikiIngestStatus, "code_graph_sync_status": registry.CodeGraphSyncStatus,
				"wiki_ingest_version": registry.WikiIngestVersion, "code_graph_commit": registry.CodeGraphCommit,
				"last_memory_sync_at": registry.LastMemorySyncAt, "conflict_status": registry.ConflictStatus,
				"superseded_by":    registry.SupersededBy,
				"last_sync_commit": registry.LastSyncCommit, "pending_queue": pending, "dead_letter_queue": deadLetter,
				"last_error": registry.LastError,
			}
		}
	}
	if jsonOutput {
		data, marshalErr := json.Marshal(result)
		return string(append(data, '\n')), marshalErr
	}
	text := fmt.Sprintf("Project: %s\nProject ID: %s\nTeam: %s\nAgent: %s\n", resolved.Metadata.Name, resolved.ProjectID, resolved.Binding.TeamID, resolved.Binding.AgentID)
	if knowledgeState, ok := result["knowledge"].(map[string]any); ok {
		text += fmt.Sprintf("Wiki: %v (%v; ingest=%v)\nCodeGraph: %v (%v; commit=%v)\nKnowledge queue: %v pending, %v dead-letter\n", knowledgeState["wiki_id"], knowledgeState["wiki_status"], knowledgeState["wiki_ingest_status"], knowledgeState["code_graph_id"], knowledgeState["code_graph_status"], knowledgeState["code_graph_commit"], knowledgeState["pending_queue"], knowledgeState["dead_letter_queue"])
	}
	if deployment, ok := result["tencent_deployment"].(map[string]any); ok {
		text += fmt.Sprintf("Tencent deployment: %v @ %v\n", deployment["resolved_commit"], deployment["repository"])
	}
	return text, nil
}

func (a *App) Repair() error {
	global, path, err := a.loadGlobal()
	if err != nil {
		return err
	}
	if global.DSHConfigPath != "" {
		if _, err := install.EnsureDSHBaseline(global.DSHConfigPath, install.DSHOptions{AdapterCommand: "baron hook dsh"}); err != nil {
			return err
		}
	}
	if global.DSHProfilePatchPath != "" {
		if err := install.EnsureDSHProfilePatch(global.DSHProfilePatchPath); err != nil {
			return err
		}
	}
	codexHooksPath := global.CodexHooksPath
	if canonicalPath, pathErr := install.CodexHooksPath(); pathErr == nil {
		codexHooksPath = canonicalPath
	}
	if codexHooksPath != "" {
		if err := install.MergeCodexHooks(codexHooksPath, "baron"); err != nil {
			return err
		}
	}
	if global.TencentInstallPath != "" {
		endpoint := firstNonEmptyString(global.Identity.Endpoint, os.Getenv("BARON_TENCENT_ENDPOINT"), "http://127.0.0.1:8420")
		hubEndpoint := firstNonEmptyString(global.Identity.HubEndpoint, os.Getenv("BARON_TENCENT_HUB_ENDPOINT"), "http://127.0.0.1:8125")
		proxyEndpoint := firstNonEmptyString(os.Getenv("BARON_TENCENT_PROXY_ENDPOINT"), "http://127.0.0.1:8096")
		knowledgeEndpoint := firstNonEmptyString(global.Identity.KnowledgeEndpoint, os.Getenv("BARON_TENCENT_KNOWLEDGE_ENDPOINT"), "http://127.0.0.1:8424")
		healthClient := tencent.NewClient(tencent.Config{Endpoint: endpoint, HubEndpoint: hubEndpoint, UserKey: global.Identity.UserKey, ServiceID: firstNonEmptyString(global.Identity.ServiceID, "default"), HTTPClient: a.HTTPClient})
		health := func() error {
			if err := healthClient.Health(context.Background()); err != nil {
				return err
			}
			if err := healthClient.HealthAt(context.Background(), hubEndpoint); err != nil {
				return fmt.Errorf("Tencent MemoryHub unavailable: %w", err)
			}
			if err := healthClient.HealthAt(context.Background(), proxyEndpoint); err != nil {
				return fmt.Errorf("Tencent proxy unavailable: %w", err)
			}
			if err := healthClient.HealthAt(context.Background(), knowledgeEndpoint); err != nil {
				return fmt.Errorf("Tencent Knowledge Service unavailable: %w", err)
			}
			return nil
		}
		runtimeConfig := install.ResolveTencentRuntimeConfig(processEnvironment())
		if err := ensureTencentRuntime(context.Background(), a.commandRunner(), health, func() error {
			return install.EnsureTencentDeployment(context.Background(), a.commandRunner(), install.TencentDeploymentOptions{
				Root: global.TencentInstallPath, UseSudo: runtime.GOOS == "linux", Runtime: runtimeConfig, PullLatest: false,
			})
		}); err != nil {
			return err
		}
	}
	if resolved, resolveErr := project.Resolve(""); resolveErr == nil && (global.Identity.Endpoint != "" || global.Identity.KnowledgeEndpoint != "") {
		store, openErr := storage.Open(filepath.Join(resolved.Root, ".baron", "runtime", "state.db"))
		if openErr != nil {
			return openErr
		}
		defer store.Close()
		identity := resolved.Identity
		if identity.UserKey == "" {
			identity.UserKey = global.Identity.UserKey
		}
		if identity.KnowledgeEndpoint == "" {
			identity.KnowledgeEndpoint = global.Identity.KnowledgeEndpoint
		}
		isolation := resolved.IsolationContext()
		if isolation.UserID == "" {
			isolation.UserID = global.Identity.UserID
		}
		var backend *tencent.Client
		if global.Identity.Endpoint != "" {
			backend = tencent.NewClient(tencent.Config{Endpoint: global.Identity.Endpoint, HubEndpoint: global.Identity.HubEndpoint, UserKey: identity.UserKey, ServiceID: global.Identity.ServiceID, HTTPClient: a.HTTPClient})
		}
		secrets := []string{identity.UserKey}
		if env, envErr := config.ReadEnvFile(resolved.EnvPath); envErr == nil {
			secrets = append(secrets, env["BARON_TENCENT_USER_KEY"])
		}
		syncer := continuity.NewSyncer(store, backend, isolation, secrets)
		if identity.KnowledgeEndpoint != "" && isolation.TeamID != "" && isolation.AgentID != "" && isolation.UserID != "" {
			if registry, registryErr := store.GetKnowledgeRegistry(context.Background(), resolved.ProjectID); registryErr == nil {
				knowledgeClient := tencent.NewKnowledgeClient(tencent.Config{Endpoint: identity.Endpoint, KnowledgeEndpoint: identity.KnowledgeEndpoint, UserKey: identity.UserKey, ServiceID: identity.ServiceID, HTTPClient: a.HTTPClient})
				core := backend
				handler := knowledge.NewQueueHandler(core, knowledgeClient, store, isolation, registry)
				syncer.SetQueueOperationHandler(handler.Handle)
			}
		}
		if _, flushErr := syncer.Flush(context.Background(), 20); flushErr != nil {
			return fmt.Errorf("flush project memory queue: %w", flushErr)
		}
	}
	return a.saveGlobal(path, global)
}

func (a *App) validateProjectBinding(current project.Project) error {
	global, _, err := a.loadGlobal()
	if err != nil {
		return err
	}
	if expected, ok := global.ProjectBindings[current.ProjectID]; ok {
		if err := project.ValidateBinding(current, expected); err != nil {
			return &cli.ExitError{Code: cli.ExitIntegrityFailure, Err: err}
		}
	}
	if global.Identity.TeamID != "" && current.Binding.TeamID != "" && global.Identity.TeamID != current.Binding.TeamID {
		return &cli.ExitError{Code: cli.ExitIntegrityFailure, Err: errors.New("project Tencent team does not match Baron global identity")}
	}
	return nil
}

func classifyError(err error) error {
	if err == nil {
		return nil
	}
	var existing *cli.ExitError
	if errors.As(err, &existing) {
		return err
	}
	message := strings.ToLower(err.Error())
	code := cli.ExitUsage
	switch {
	case strings.Contains(message, "integrity") || strings.Contains(message, "symlink") || strings.Contains(message, "checksum") || strings.Contains(message, "manifest") || strings.Contains(message, "candidate"):
		code = cli.ExitIntegrityFailure
	case strings.Contains(message, "baron release") || strings.Contains(message, "github release") || strings.Contains(message, "windows update staged"):
		code = cli.ExitReleaseUnavailable
	case strings.Contains(message, "auth") || strings.Contains(message, "credential"):
		code = cli.ExitAuthIncomplete
	case strings.Contains(message, "tencent") || strings.Contains(message, "memorycore") || strings.Contains(message, "network") || strings.Contains(message, "connection"):
		code = cli.ExitTencentUnavailable
	case strings.Contains(message, "not installed") || strings.Contains(message, "not on path") || strings.Contains(message, "required") || strings.Contains(message, "missing"):
		code = cli.ExitMissingDependency
	case strings.Contains(message, "unsupported"):
		code = cli.ExitUnsupportedUpstream
	}
	return &cli.ExitError{Code: code, Err: err}
}

func New() *App {
	return &App{HTTPClient: &http.Client{Timeout: 3 * time.Second}, CommandRunner: doctor.OSProbe{}}
}

func (a *App) commandRunner() install.CommandRunner {
	if a.CommandRunner != nil {
		return a.CommandRunner
	}
	return doctor.OSProbe{}
}

func (a *App) globalPath() (string, error) {
	if a.GlobalPath != "" {
		return a.GlobalPath, nil
	}
	return config.DefaultGlobalStatePath()
}

func (a *App) loadGlobal() (config.GlobalState, string, error) {
	path, err := a.globalPath()
	if err != nil {
		return config.GlobalState{}, "", err
	}
	state, err := config.LoadGlobalState(path)
	return state, path, err
}

func (a *App) saveGlobal(path string, state config.GlobalState) error {
	return config.SaveGlobalState(path, state)
}

func (a *App) SetupProject(ctx context.Context, path string) (project.Project, error) {
	global, globalPath, err := a.loadGlobal()
	if err != nil {
		return project.Project{}, err
	}
	provisioner := a.ProjectProvisioner
	if provisioner == nil && global.Identity.Endpoint != "" && global.Identity.TeamID != "" && global.Identity.UserID != "" {
		client := tencent.NewClient(tencent.Config{Endpoint: global.Identity.Endpoint, HubEndpoint: global.Identity.HubEndpoint, UserKey: global.Identity.UserKey, ServiceID: global.Identity.ServiceID, HTTPClient: a.HTTPClient})
		provisioner = func(ctx context.Context, projectID, name string) (contracts.ProjectBinding, error) {
			return client.EnsureProjectAgent(ctx, contracts.IsolationContext{ProjectID: projectID, TeamID: global.Identity.TeamID, UserID: global.Identity.UserID, ServiceID: global.Identity.ServiceID}, name)
		}
	}
	var existingBinding contracts.ProjectBinding
	if resolved, resolveErr := project.Resolve(path); resolveErr == nil {
		existingBinding = global.ProjectBindings[resolved.ProjectID]
	}
	result, err := project.Setup(ctx, path, project.SetupOptions{Identity: global.Identity, Binding: existingBinding, Provision: provisioner})
	if err != nil {
		return project.Project{}, err
	}
	if global.Identity.Endpoint != "" && result.Binding.TeamID != "" && result.Binding.AgentID != "" && result.Binding.UserID != "" {
		client := tencent.NewClient(tencent.Config{Endpoint: global.Identity.Endpoint, HubEndpoint: global.Identity.HubEndpoint, UserKey: global.Identity.UserKey, ServiceID: global.Identity.ServiceID, HTTPClient: a.HTTPClient})
		if _, err := client.Search(ctx, result.IsolationContext(), contracts.MemoryQuery{Text: "baron setup verification", Limit: 1}); err != nil {
			return project.Project{}, fmt.Errorf("verify Tencent project isolation: %w", err)
		}
	}
	if global.Identity.KnowledgeEndpoint != "" && result.Binding.TeamID != "" && result.Binding.AgentID != "" && result.Binding.UserID != "" {
		statePath := filepath.Join(result.Root, ".baron", "runtime", "state.db")
		store, storeErr := storage.Open(statePath)
		if storeErr != nil {
			return project.Project{}, fmt.Errorf("open local state for knowledge registry: %w", storeErr)
		}
		core := tencent.NewClient(tencent.Config{Endpoint: global.Identity.Endpoint, UserKey: global.Identity.UserKey, ServiceID: global.Identity.ServiceID, HTTPClient: a.HTTPClient})
		knowledgeClient := tencent.NewKnowledgeClient(tencent.Config{Endpoint: global.Identity.Endpoint, KnowledgeEndpoint: global.Identity.KnowledgeEndpoint, UserKey: global.Identity.UserKey, ServiceID: global.Identity.ServiceID, HTTPClient: a.HTTPClient})
		_, provisionErr := knowledge.ProvisionProject(ctx, knowledge.ProvisionOptions{
			Root: result.Root, ProjectID: result.ProjectID, ProjectName: result.Metadata.Name,
			Isolation: result.IsolationContext(), Core: core, Knowledge: knowledgeClient,
			ServiceURL: global.Identity.KnowledgeEndpoint, Store: store, Secrets: []string{global.Identity.UserKey},
		})
		closeErr := store.Close()
		if closeErr != nil {
			if provisionErr != nil {
				return project.Project{}, fmt.Errorf("Tencent knowledge provisioning failed: %v; close local state: %w", provisionErr, closeErr)
			}
			return project.Project{}, fmt.Errorf("close local state after knowledge provisioning: %w", closeErr)
		}
		// Remote knowledge is a sidecar. The registry retains a redacted
		// diagnostic so a later doctor/repair can retry without losing local
		// continuity or blocking the agent session.
	}
	global.ProjectBindings[result.ProjectID] = result.Binding
	if err := a.saveGlobal(globalPath, global); err != nil {
		return project.Project{}, err
	}
	return result, nil
}

func (a *App) HandleHook(ctx context.Context, clientName, eventName, root string, input io.Reader, output io.Writer) error {
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return writeHookFailureForClient(output, clientName, eventName, err)
		}
	}
	resolved, err := project.Resolve(root)
	if err != nil {
		return writeHookFailureForClient(output, clientName, eventName, err)
	}
	if err := a.validateProjectBinding(resolved); err != nil {
		return writeHookFailureForClient(output, clientName, eventName, err)
	}
	store, err := storage.Open(filepath.Join(resolved.Root, ".baron", "runtime", "state.db"))
	if err != nil {
		return writeHookFailureForClient(output, clientName, eventName, err)
	}
	defer store.Close()
	engine := continuity.NewEngine(store, resolved.ProjectID, resolved.Metadata.Name, continuity.CheckpointPath(resolved.Root))
	runtime := hooks.NewRuntime(store, engine, resolved.ProjectID)
	runtime.SetRepositoryRoot(resolved.Root)
	global, _, globalErr := a.loadGlobal()
	var hookSecrets []string
	if env, envErr := config.ReadEnvFile(resolved.EnvPath); envErr == nil {
		hookSecrets = append(hookSecrets, env["BARON_TENCENT_USER_KEY"])
	}
	if globalErr == nil {
		hookSecrets = append(hookSecrets, global.Identity.UserKey)
	}
	hookSecrets = append(hookSecrets, resolved.Identity.UserKey)
	runtime.SetSecrets(hookSecrets)
	// Remote memory is an optional sidecar. A configured project binding is
	// enough to use it; malformed or missing global memory configuration leaves
	// the local hook path fully operational.
	identity := resolved.Identity
	if globalErr == nil {
		if identity.Endpoint == "" {
			identity.Endpoint = global.Identity.Endpoint
		}
		if identity.HubEndpoint == "" {
			identity.HubEndpoint = global.Identity.HubEndpoint
		}
		if identity.KnowledgeEndpoint == "" {
			identity.KnowledgeEndpoint = global.Identity.KnowledgeEndpoint
		}
		if identity.UserKey == "" {
			identity.UserKey = global.Identity.UserKey
		}
		if identity.ServiceID == "" {
			identity.ServiceID = global.Identity.ServiceID
		}
	}
	isolation := resolved.IsolationContext()
	if isolation.UserID == "" {
		if globalErr == nil {
			isolation.UserID = global.Identity.UserID
		}
	}
	var memoryClient *tencent.Client
	if identity.Endpoint != "" && isolation.TeamID != "" && isolation.AgentID != "" && isolation.UserID != "" {
		memoryClient = tencent.NewClient(tencent.Config{
			Endpoint: identity.Endpoint, HubEndpoint: identity.HubEndpoint, UserKey: identity.UserKey,
			ServiceID: identity.ServiceID, HTTPClient: a.HTTPClient,
		})
		runtime.SetMemoryBackend(memoryClient, isolation)
	}
	if identity.KnowledgeEndpoint != "" && isolation.TeamID != "" && isolation.AgentID != "" && isolation.UserID != "" {
		if registry, registryErr := store.GetKnowledgeRegistry(ctx, resolved.ProjectID); registryErr == nil {
			knowledgeClient := tencent.NewKnowledgeClient(tencent.Config{
				Endpoint: identity.Endpoint, KnowledgeEndpoint: identity.KnowledgeEndpoint, UserKey: identity.UserKey,
				ServiceID: identity.ServiceID, HTTPClient: a.HTTPClient,
			})
			runtime.SetKnowledgeBackend(knowledge.NewRetriever(knowledgeClient, registry), isolation)
			coreClient := memoryClient
			if coreClient == nil && identity.Endpoint != "" {
				coreClient = tencent.NewClient(tencent.Config{Endpoint: identity.Endpoint, UserKey: identity.UserKey, ServiceID: identity.ServiceID, HTTPClient: a.HTTPClient})
			}
			runtime.SetQueueOperationHandler(knowledge.NewQueueHandler(coreClient, knowledgeClient, store, isolation, registry).Handle)
		}
	}
	data, err := io.ReadAll(io.LimitReader(input, 2*1024*1024))
	if err != nil {
		return writeHookFailureForClient(output, clientName, eventName, err)
	}
	request := hooks.Request{}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &request); err != nil {
			return writeHookFailureForClient(output, clientName, eventName, err)
		}
		if len(bytes.TrimSpace(request.Payload)) == 0 {
			// Upstream Codex/DSH payloads are not required to wrap their fields in
			// Baron’s payload property; preserve the original bounded JSON as the
			// evidence body instead of silently discarding it.
			request.Payload = json.RawMessage(data)
		}
	}
	request.Client = contracts.HookClient(strings.ToLower(clientName))
	request.Event = canonicalEvent(eventName)
	if request.ProjectID == "" {
		request.ProjectID = resolved.ProjectID
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return writeHookFailureForClient(output, clientName, eventName, err)
	}
	var hookOutput bytes.Buffer
	if err := hooks.ServeJSON(ctx, runtime, bytes.NewReader(encoded), &hookOutput); err != nil {
		return writeHookFailureForClient(output, clientName, eventName, err)
	}
	if strings.EqualFold(clientName, "codex") {
		var response hooks.Response
		if err := json.Unmarshal(hookOutput.Bytes(), &response); err == nil {
			codexOutput := map[string]any{"continue": true}
			if response.Context != "" {
				codexOutput["hookSpecificOutput"] = map[string]any{
					"hookEventName": eventName, "additionalContext": response.Context,
				}
			}
			return json.NewEncoder(output).Encode(codexOutput)
		}
	}
	_, err = io.Copy(output, &hookOutput)
	return err
}

func writeHookFailure(output io.Writer, err error) error {
	if output == nil {
		return nil
	}
	return json.NewEncoder(output).Encode(hooks.Response{OK: false, Error: err.Error()})
}

func writeHookFailureForClient(output io.Writer, clientName, eventName string, err error) error {
	if strings.EqualFold(clientName, "codex") {
		if output == nil {
			return nil
		}
		response := map[string]any{"continue": true}
		if err != nil {
			response["hookSpecificOutput"] = map[string]any{
				"hookEventName":     eventName,
				"additionalContext": "Baron hook diagnostic (fail-open): " + config.Redact(err.Error(), nil),
			}
		}
		return json.NewEncoder(output).Encode(response)
	}
	return writeHookFailure(output, err)
}

func canonicalEvent(value string) contracts.EventType {
	key := strings.ToLower(strings.NewReplacer("-", "_", " ", "_", "/", "_").Replace(value))
	switch key {
	case "sessionstart", "session_started":
		return contracts.EventSessionStarted
	case "sessionend", "session_end", "session_ended", "session_clean_closed":
		return contracts.EventSessionCleanClose
	case "userpromptsubmit", "user_prompt":
		return contracts.EventUserPrompt
	case "posttooluse", "tool_finished":
		return contracts.EventToolFinished
	case "pretooluse", "tool_started":
		return contracts.EventToolStarted
	case "stop", "assistant_final":
		return contracts.EventAssistantFinal
	case "precompact", "postcompact", "checkpoint_updated":
		return contracts.EventCheckpointUpdated
	case "file_changed":
		return contracts.EventFileChanged
	case "test_started":
		return contracts.EventTestStarted
	case "test_finished":
		return contracts.EventTestFinished
	case "error_observed":
		return contracts.EventErrorObserved
	default:
		return contracts.EventType(key)
	}
}

func (a *App) DSHInit() error {
	if _, err := a.resolveDSHCredential(); err != nil {
		return err
	}
	global, path, err := a.loadGlobal()
	if err != nil {
		return err
	}
	if err := install.InstallDSH(context.Background(), a.commandRunner(), install.PinnedDSHVersion); err != nil {
		return err
	}
	if err := install.InstallDSHPlugins(context.Background(), a.commandRunner(), install.PinnedDSHVersion); err != nil {
		return err
	}
	globalDir := filepath.Dir(path)
	adapterPath := filepath.Join(globalDir, "dsh-adapter")
	if err := install.InstallEmbeddedDSHAdapter(adapterPath); err != nil {
		return err
	}
	profilePatchPath := ""
	for _, profile := range []string{"web", "headless"} {
		if _, err := a.commandRunner().Run(context.Background(), "dsh", "plugin", "--profile", profile, "add", adapterPath); err != nil {
			return fmt.Errorf("install the Baron DSH adapter into the %s profile: %w", profile, err)
		}
		patchPath, patchErr := install.DSHProfilePatchPath(profile)
		if patchErr != nil {
			return patchErr
		}
		if err := install.EnsureDSHProfilePatch(patchPath); err != nil {
			return err
		}
		if profile == "web" {
			profilePatchPath = patchPath
		}
	}
	if err := install.VerifyDSHProfile(context.Background(), a.commandRunner()); err != nil {
		return err
	}
	if err := install.ProbeDSHStartup(context.Background(), a.commandRunner()); err != nil {
		return err
	}
	global.DSHConfigPath = filepath.Join(globalDir, "dsh.json")
	global.DSHProfilePatchPath = profilePatchPath
	report, err := installDSH(global.DSHConfigPath)
	if err != nil {
		return err
	}
	global.DSHComponents = map[string]bool{}
	for _, component := range report.Components {
		switch component {
		case "duckduckgo-search":
			global.DSHComponents[component] = true
		case "dsh-superpowers":
			global.DSHComponents["superpowers-dsh"] = true
		default:
			global.DSHComponents[component] = true
		}
	}
	receiptPath := filepath.Join(globalDir, "receipts", "dsh.json")
	if err := install.WriteReceipt(receiptPath, install.Receipt{Component: "deepseek-harness", Version: install.PinnedDSHVersion, Source: "npm:@deepseek-ai/dsh"}); err != nil {
		return err
	}
	global.Receipts = appendReceipt(global.Receipts, receiptPath)
	return a.saveGlobal(path, global)
}

func (a *App) CodexInit() error {
	codexSource, err := install.InstallCodexWithSource(context.Background(), a.commandRunner(), "0.149.0")
	if err != nil {
		return err
	}
	global, path, err := a.loadGlobal()
	if err != nil {
		return err
	}
	codexHooksPath, err := install.CodexHooksPath()
	if err != nil {
		return err
	}
	global.CodexHooksPath = codexHooksPath
	globalDir := filepath.Dir(path)
	global.CodexAdapterPath = filepath.Join(globalDir, "codex-adapter")
	if err := install.InstallEmbeddedCodexAdapter(global.CodexAdapterPath); err != nil {
		return err
	}
	if err := mergeCodex(global.CodexHooksPath); err != nil {
		return err
	}
	global.CodexHooksInstalled = true
	receiptPath := filepath.Join(globalDir, "receipts", "codex.json")
	if err := install.WriteReceipt(receiptPath, install.Receipt{Component: "codex-cli", Version: "0.149.0", Source: codexSource}); err != nil {
		return err
	}
	global.Receipts = appendReceipt(global.Receipts, receiptPath)
	adapterReceiptPath := filepath.Join(globalDir, "receipts", "codex-adapter.json")
	if err := install.WriteReceipt(adapterReceiptPath, install.Receipt{Component: "codex-adapter", Version: install.PinnedCodexAdapterVersion, Source: "embedded:adapters/codex"}); err != nil {
		return err
	}
	global.Receipts = appendReceipt(global.Receipts, adapterReceiptPath)
	return a.saveGlobal(path, global)
}

func (a *App) TencentInit(ctx context.Context) error {
	runner := a.commandRunner()
	global, path, err := a.loadGlobal()
	if err != nil {
		return err
	}
	endpoint := os.Getenv("BARON_TENCENT_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8420"
	}
	hubEndpoint := os.Getenv("BARON_TENCENT_HUB_ENDPOINT")
	if hubEndpoint == "" {
		hubEndpoint = "http://127.0.0.1:8125"
	}
	proxyEndpoint := os.Getenv("BARON_TENCENT_PROXY_ENDPOINT")
	if proxyEndpoint == "" {
		proxyEndpoint = "http://127.0.0.1:8096"
	}
	knowledgeEndpoint := os.Getenv("BARON_TENCENT_KNOWLEDGE_ENDPOINT")
	if knowledgeEndpoint == "" {
		knowledgeEndpoint = "http://127.0.0.1:8424"
	}
	serviceID := firstNonEmptyString(os.Getenv("BARON_TENCENT_SERVICE_ID"), "default")
	deploymentRoot := os.Getenv("BARON_TENCENT_MEMORY_DIR")
	if deploymentRoot == "" {
		deploymentRoot = filepath.Join(filepath.Dir(path), "tencent-memory")
	}
	runtimeConfig, err := a.resolveTencentRuntimeConfig(deploymentRoot)
	if err != nil {
		return err
	}
	adminKey := os.Getenv("BARON_TENCENT_ADMIN_KEY")
	if adminKey == "" {
		if key, keyErr := install.TencentAdminKey(deploymentRoot); keyErr == nil {
			adminKey = key
		}
	}
	client := tencent.NewClient(tencent.Config{Endpoint: endpoint, HubEndpoint: hubEndpoint, AdminKey: adminKey, ServiceID: serviceID, HTTPClient: a.HTTPClient})
	health := func() error {
		if err := client.Health(ctx); err != nil {
			return err
		}
		if err := client.HealthAt(ctx, hubEndpoint); err != nil {
			return fmt.Errorf("Tencent MemoryHub unavailable: %w", err)
		}
		if err := client.HealthAt(ctx, proxyEndpoint); err != nil {
			return fmt.Errorf("Tencent proxy unavailable: %w", err)
		}
		if err := client.HealthAt(ctx, knowledgeEndpoint); err != nil {
			return fmt.Errorf("Tencent Knowledge Service unavailable: %w", err)
		}
		return nil
	}
	// A healthy managed stack is already a usable runtime. Avoid asking for a
	// second sudo authorization on an idempotent init; Docker/sudo is required
	// only when the health probe proves that repair or deployment is needed.
	dockerReport, err := ensureTencentDocker(ctx, runner, health)
	if err != nil {
		return err
	}
	if healthErr := health(); healthErr != nil {
		if os.Getenv("BARON_TENCENT_SKIP_DEPLOY") == "1" {
			return healthErr
		}
		if err := install.EnsureTencentDeployment(ctx, runner, install.TencentDeploymentOptions{Root: deploymentRoot, UseSudo: dockerReport.UsedSudo, PullLatest: true, Runtime: runtimeConfig}); err != nil {
			return fmt.Errorf("Tencent services are unavailable and automatic deployment failed: %w", err)
		}
		if adminKey == "" {
			adminKey, err = install.TencentAdminKey(deploymentRoot)
			if err != nil {
				adminKey, err = a.resolveTencentAdminKey(deploymentRoot)
				if err != nil {
					return err
				}
			}
			client = tencent.NewClient(tencent.Config{Endpoint: endpoint, HubEndpoint: hubEndpoint, AdminKey: adminKey, ServiceID: serviceID, HTTPClient: a.HTTPClient})
		}
		if err := health(); err != nil {
			return err
		}
	}
	if adminKey == "" {
		adminKey, err = a.resolveTencentAdminKey(deploymentRoot)
		if err != nil {
			return err
		}
		client = tencent.NewClient(tencent.Config{Endpoint: endpoint, HubEndpoint: hubEndpoint, AdminKey: adminKey, ServiceID: serviceID, HTTPClient: a.HTTPClient})
	}
	existingUserKey := ""
	if global.Identity.Endpoint == "" || global.Identity.Endpoint == endpoint {
		existingUserKey = global.Identity.UserKey
	}
	identity, identityProvision, err := client.EnsureIdentityWithExistingUserKey(ctx, contracts.IdentitySpec{UserName: "baron", TeamName: "baron-projects"}, existingUserKey)
	if err != nil {
		return err
	}
	rollbackIdentity := func(cause error) error {
		if rollbackErr := identityProvision.Rollback(ctx); rollbackErr != nil {
			return fmt.Errorf("%v; newly created Baron metadata rollback failed: %w", cause, rollbackErr)
		}
		return cause
	}
	identity.KnowledgeEndpoint = knowledgeEndpoint
	identityClient := tencent.NewClient(tencent.Config{Endpoint: endpoint, HubEndpoint: hubEndpoint, UserKey: identity.UserKey, ServiceID: identity.ServiceID, HTTPClient: a.HTTPClient})
	if err := identityClient.VerifyAuth(ctx); err != nil {
		return rollbackIdentity(err)
	}
	global.Identity = identity
	global.TencentInstallPath = deploymentRoot
	receiptPath := filepath.Join(filepath.Dir(path), "receipts", "tencent-memory.json")
	if err := install.WriteReceipt(receiptPath, install.Receipt{Component: "tencent-memory", Version: "v3", Source: "TencentDB Agent Memory local deployment"}); err != nil {
		return rollbackIdentity(err)
	}
	global.Receipts = appendReceipt(global.Receipts, receiptPath)
	if err := a.saveGlobal(path, global); err != nil {
		return rollbackIdentity(err)
	}
	identityProvision.Commit()
	return nil
}

// ensureTencentDocker first checks the data-plane health. A repeated init of
// an already-running stack must remain idempotent even when the current shell
// does not have a cached sudo ticket. If repair/deployment is necessary, the
// Linux bootstrap still performs its sudo preflight before any Docker package
// or Tencent image download.
func ensureTencentDocker(ctx context.Context, runner install.CommandRunner, health func() error) (install.DockerBootstrapReport, error) {
	if health != nil {
		if err := health(); err == nil {
			return install.DockerBootstrapReport{Ready: true, Message: "Tencent services are already healthy."}, nil
		}
	}
	return install.EnsureDocker(ctx, runner, install.DockerBootstrapOptions{})
}

// ensureTencentRuntime keeps repair idempotent for an already-healthy managed
// stack. A repair should be able to flush remote queues and restore Baron-owned
// integrations without asking for a second sudo authorization or redeploying
// healthy services. Bootstrap/deployment remains the fallback when health is
// unavailable.
func ensureTencentRuntime(ctx context.Context, runner install.CommandRunner, health func() error, deploy func() error) error {
	if health != nil {
		if err := health(); err == nil {
			return nil
		}
	}
	if runtime.GOOS == "linux" {
		if _, err := install.EnsureDocker(ctx, runner, install.DockerBootstrapOptions{}); err != nil {
			return err
		}
	}
	if deploy == nil {
		return errors.New("Tencent deployment repair is not configured")
	}
	return deploy()
}

func (a *App) resolveDSHCredential() (string, error) {
	values := processEnvironment()
	key, err := install.ReadDSHProviderKey(values)
	if err != nil {
		return "", err
	}
	if key != "" {
		return key, nil
	}

	// Allow either initializer to be run first. A provider key already present
	// in the managed Tencent environment can be copied into DSH's own official
	// store, but it is never copied into Baron state or printed.
	if global, _, globalErr := a.loadGlobal(); globalErr == nil && global.TencentInstallPath != "" {
		managed, managedErr := install.LoadTencentRuntimeConfig(global.TencentInstallPath)
		if managedErr != nil {
			return "", managedErr
		}
		if !missingCredentialValue(managed.MemoryLLMAPIKey) {
			if err := install.EnsureDSHProviderKey(values, managed.MemoryLLMAPIKey); err != nil {
				return "", err
			}
			return managed.MemoryLLMAPIKey, nil
		}
	}

	key, err = a.prompter().Secret("DeepSeek API key (DEEPSEEK_API_KEY)")
	if err != nil {
		return "", credentialPromptFailure("DeepSeek", "DEEPSEEK_API_KEY", "baron deepseek-harness init", err)
	}
	if err := install.EnsureDSHProviderKey(values, key); err != nil {
		return "", err
	}
	return key, nil
}

func (a *App) resolveTencentRuntimeConfig(deploymentRoot string) (install.TencentRuntimeConfig, error) {
	values := processEnvironment()
	managed, err := install.LoadTencentRuntimeConfig(deploymentRoot)
	if err != nil {
		return install.TencentRuntimeConfig{}, err
	}
	dshKey, err := install.ReadDSHProviderKey(values)
	if err != nil {
		return install.TencentRuntimeConfig{}, err
	}
	resolved := install.ResolveTencentRuntimeConfigWithSources(values, managed, dshKey)
	if len(resolved.MissingProviderValues()) == 0 {
		return resolved, nil
	}

	for attempt := 0; attempt < 3; attempt++ {
		key, promptErr := a.prompter().Secret("Tencent provider API key (DEEPSEEK_API_KEY)")
		if promptErr != nil {
			if errors.Is(promptErr, credentials.ErrEmptyValue) {
				continue
			}
			return install.TencentRuntimeConfig{}, credentialPromptFailure("Tencent", "DEEPSEEK_API_KEY", "baron tencent-memory init", promptErr)
		}
		resolved.MemoryLLMAPIKey = key
		if missingCredentialValue(resolved.ProxyUpstreamAPIKey) {
			resolved.ProxyUpstreamAPIKey = key
		}
		resolved = install.MergeTencentRuntimeConfig(resolved, install.DefaultTencentRuntimeConfig())
		if len(resolved.MissingProviderValues()) == 0 {
			return resolved, nil
		}
	}
	return install.TencentRuntimeConfig{}, credentialPromptFailure("Tencent", "DEEPSEEK_API_KEY", "baron tencent-memory init", credentials.ErrEmptyValue)
}

func (a *App) resolveTencentAdminKey(deploymentRoot string) (string, error) {
	if key := strings.TrimSpace(os.Getenv("BARON_TENCENT_ADMIN_KEY")); key != "" {
		return key, nil
	}
	if key, err := install.TencentAdminKey(deploymentRoot); err == nil {
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	key, err := a.prompter().Secret("Tencent admin key (BARON_TENCENT_ADMIN_KEY)")
	if err != nil {
		return "", credentialPromptFailure("Tencent admin", "BARON_TENCENT_ADMIN_KEY", "baron tencent-memory init", err)
	}
	return key, nil
}

func (a *App) prompter() *credentials.Prompter {
	return &credentials.Prompter{In: a.Input, Out: a.PromptOutput, ReadSecret: a.ReadSecret, ReadLine: a.ReadLine}
}

func credentialPromptFailure(component, environmentName, command string, err error) error {
	if errors.Is(err, credentials.ErrNonInteractive) {
		return fmt.Errorf("%s provider credential is required; set %s in the launching environment or rerun %s from an interactive terminal: %w", component, environmentName, command, err)
	}
	if errors.Is(err, credentials.ErrEmptyValue) {
		return fmt.Errorf("%s provider credential is required; set %s or rerun %s with a non-empty value", component, environmentName, command)
	}
	return fmt.Errorf("%s provider credential prompt failed; set %s or rerun %s: %w", component, environmentName, command, err)
}

func missingCredentialValue(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || strings.EqualFold(value, "REPLACE_ME")
}

func appendReceipt(receipts []string, path string) []string {
	for _, existing := range receipts {
		if existing == path {
			return receipts
		}
	}
	return append(receipts, path)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func processEnvironment() map[string]string {
	values := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}

// The imports are kept behind these tiny adapters so app wiring remains easy
// to replace with fixture runners in tests.
func installDSH(path string) (installReport, error) {
	report, err := install.EnsureDSHBaseline(path, install.DSHOptions{AdapterCommand: "baron hook dsh"})
	return installReport{Components: report.Components}, err
}

func mergeCodex(path string) error { return install.MergeCodexHooks(path, "baron") }

type installReport struct{ Components []string }
