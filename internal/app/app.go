package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
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
	"github.com/baron-shared-brain/baron/internal/permissions"
	"github.com/baron-shared-brain/baron/internal/project"
	"github.com/baron-shared-brain/baron/internal/release"
	"github.com/baron-shared-brain/baron/internal/storage"
	baronuninstall "github.com/baron-shared-brain/baron/internal/uninstall"
	"github.com/baron-shared-brain/baron/internal/version"
)

type App struct {
	GlobalPath         string
	HTTPClient         *http.Client
	ProjectProvisioner func(context.Context, string, string) (contracts.ProjectBinding, error)
	// TencentRestore is an injectable restore boundary for acceptance fixtures.
	// The default path performs the real Docker/service/identity verification;
	// tests may replace it to prove staging order without contacting Tencent.
	TencentRestore  TencentRestoreFunc
	CommandRunner   install.CommandRunner
	Input           io.Reader
	PromptOutput    io.Writer
	ReadSecret      credentials.SecretReader
	ReadLine        credentials.LineReader
	prepareForInput func()
	// ValidateProviderCredential is injectable for deterministic tests. A nil
	// value uses the live OpenAI-compatible /models validator.
	ValidateProviderCredential func(context.Context, string, string) error
	ReleaseClient              *release.Client
	ExecutablePath             string
}

func (a *App) CLIOptions(out, errOut io.Writer) cli.Options {
	ui := install.NewProgressUI(out)
	a.prepareForInput = nil
	if ui != nil {
		a.prepareForInput = ui.PrepareForInput
	}
	var progress install.ProgressReporter
	if ui != nil {
		progress = ui
	}
	runWithLoading := func(label string, action func() error) error {
		if ui == nil {
			return action()
		}
		return ui.Run(label, action)
	}
	return cli.Options{
		Version: version.Value,
		Out:     out, Err: errOut, In: a.Input,
		Setup: func(path string) error {
			_, err := a.SetupProject(context.Background(), path)
			return classifyError(err)
		},
		TestOutput:     a.testOutput,
		StatusOutput:   a.statusOutput,
		TimelineOutput: a.timelineOutput,
		DoctorOutput:   a.doctorOutput,
		Repair:         func() error { return classifyError(a.Repair()) },
		Backup:         func(destination string) error { return classifyError(a.Backup(context.Background(), destination)) },
		Restore:        func(archive string) error { return classifyError(a.Restore(context.Background(), archive)) },
		RestoreWithOptions: func(archive string, replaceExisting bool) error {
			return classifyError(a.restoreWithOptions(context.Background(), archive, replaceExisting))
		},
		Install: func() (string, error) {
			message, err := a.installAndBootstrap(context.Background(), progress)
			return message, classifyError(err)
		},
		Update:         func() (string, error) { return a.installBaronBinary(false, progress) },
		RunWithLoading: runWithLoading,
		PermissionsEnable: func() (string, error) {
			return a.enablePermissions()
		},
		PermissionsDisable: func() (string, error) {
			return a.disablePermissions()
		},
		PermissionsStatus: func() (string, error) {
			return a.permissionsStatus()
		},
		UninstallPlan: func(purgeShared bool) (string, error) {
			return a.uninstallPlan(purgeShared)
		},
		Uninstall: func(purgeShared bool) (string, error) {
			return a.uninstall(purgeShared)
		},
		SetCredential: func(provider string) error { return classifyError(a.SetCredential(provider)) },
		Hook: func(client, event string, input io.Reader, output io.Writer) error {
			return a.HandleHook(context.Background(), client, event, "", input, output)
		},
		Init: map[string]func() error{
			"deepseek-harness": func() error { return classifyError(a.DSHInit()) },
			"codex-cli":        func() error { return classifyError(a.CodexInit()) },
			"tencent-memory":   func() error { return classifyError(a.TencentInit(context.Background())) },
		},
		InitNoticeFunc: map[string]func() string{
			"codex-cli": codexLoginNotice,
		},
	}
}

const (
	codexLoginAction          = "If Codex is not authenticated, run codex and complete ChatGPT sign-in once; Baron reuses Codex's global auth afterward."
	deepseekCredentialCommand = "baron deepseek api_key"
)

const releaseDownloadTimeout = 2 * time.Minute

func releaseHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		return &http.Client{Timeout: releaseDownloadTimeout}
	}
	clone := *base
	if clone.Timeout == 0 || clone.Timeout < releaseDownloadTimeout {
		clone.Timeout = releaseDownloadTimeout
	}
	return &clone
}

func codexLoginNotice() string {
	if codexAuthReady() {
		return ""
	}
	return codexLoginAction
}

func (a *App) installBaronBinary(force bool, reporter install.ProgressReporter) (string, error) {
	target := a.ExecutablePath
	if target == "" {
		var err error
		target, err = release.CurrentExecutablePath()
		if err != nil {
			return "", err
		}
	}
	client := release.Client{
		Repository: os.Getenv("BARON_RELEASE_REPOSITORY"),
		APIBaseURL: os.Getenv("BARON_RELEASE_API_BASE_URL"),
	}
	if a.ReleaseClient != nil {
		client = *a.ReleaseClient
	}
	client.Progress = reporter
	if client.HTTPClient == nil {
		client.HTTPClient = a.HTTPClient
	}
	client.HTTPClient = releaseHTTPClient(client.HTTPClient)
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
	dshStatus := doctor.StatusIncomplete
	dshMessage := "DSH provider credential is not configured."
	dshSuggestion := deepseekCredentialCommand + " (or set DEEPSEEK_API_KEY)"
	if dshKeyErr != nil {
		dshMessage = "DSH provider credential could not be read safely."
	} else if dshKey != "" {
		if validationErr := a.validateProviderCredential(context.Background(), dshProviderBaseURL(processEnvironment()), dshKey); validationErr == nil {
			dshStatus = doctor.StatusReady
			dshMessage = "DSH provider credential is configured and accepted by the provider."
			dshSuggestion = ""
		} else if errors.Is(validationErr, credentials.ErrProviderUnavailable) {
			dshStatus = doctor.StatusUnavailable
			dshMessage = "DSH provider credential could not be validated because the provider or network is unavailable."
			dshSuggestion = "restore provider connectivity, then rerun baron test"
		} else {
			dshMessage = "DSH provider credential was rejected by the provider."
			dshSuggestion = deepseekCredentialCommand
		}
	}
	if check := a.tencentProviderCredentialCheck(context.Background(), global); check != nil {
		surfaceChecks = append(surfaceChecks, *check)
	}
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
		DSHProviderReady:    dshStatus == doctor.StatusReady,
		DSHProviderStatus:   dshStatus,
		DSHProviderMessage:  dshMessage,
		DSHProviderSuggest:  dshSuggestion,
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

func (a *App) tencentProviderCredentialCheck(ctx context.Context, global config.GlobalState) *doctor.CheckResult {
	if strings.TrimSpace(global.TencentInstallPath) == "" {
		return nil
	}
	managed, err := install.LoadTencentRuntimeConfig(global.TencentInstallPath)
	if err != nil {
		return &doctor.CheckResult{Name: "tencent-provider-credential", Status: doctor.StatusIncomplete, Message: "Tencent managed provider credential could not be read safely.", Suggestion: "baron tencent-memory init"}
	}
	if missingCredentialValue(managed.MemoryLLMAPIKey) {
		return &doctor.CheckResult{Name: "tencent-provider-credential", Status: doctor.StatusIncomplete, Message: "Tencent provider credential is not configured in the managed runtime.", Suggestion: "baron tencent-memory init"}
	}
	if validationErr := a.validateProviderCredential(ctx, firstNonEmptyString(managed.MemoryLLMBaseURL, dshProviderBaseURL(processEnvironment())), managed.MemoryLLMAPIKey); validationErr == nil {
		return &doctor.CheckResult{Name: "tencent-provider-credential", Status: doctor.StatusReady, Message: "Tencent provider credential is configured and accepted by the provider."}
	} else if errors.Is(validationErr, credentials.ErrProviderUnavailable) {
		return &doctor.CheckResult{Name: "tencent-provider-credential", Status: doctor.StatusUnavailable, Message: "Tencent provider credential could not be validated because the provider or network is unavailable.", Suggestion: "restore provider connectivity, then rerun baron test"}
	}
	return &doctor.CheckResult{Name: "tencent-provider-credential", Status: doctor.StatusIncomplete, Message: "Tencent provider credential was rejected by the provider.", Suggestion: deepseekCredentialCommand}
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

type localStatusTask struct {
	TaskID                  string                     `json:"task_id"`
	Goal                    string                     `json:"goal,omitempty"`
	Status                  contracts.TaskStatus       `json:"status"`
	CurrentStep             string                     `json:"current_step,omitempty"`
	NextAction              string                     `json:"next_action,omitempty"`
	CompletionVerified      bool                       `json:"completion_verified"`
	CompletionPolicy        contracts.CompletionPolicy `json:"completion_policy"`
	LatestVerificationRef   string                     `json:"latest_verification_ref,omitempty"`
	LatestVerificationKind  contracts.VerificationKind `json:"latest_verification_kind,omitempty"`
	LatestVerificationScope string                     `json:"latest_verification_scope,omitempty"`
	LatestErrorRef          string                     `json:"latest_error_ref,omitempty"`
	UpdatedAt               time.Time                  `json:"updated_at"`
}

type localStatusTest struct {
	Command    string `json:"command,omitempty"`
	Status     string `json:"status,omitempty"`
	Summary    string `json:"summary,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
}

type localStatusState struct {
	Available    bool                   `json:"available"`
	Error        string                 `json:"error,omitempty"`
	SessionID    string                 `json:"session_id,omitempty"`
	SessionState contracts.SessionState `json:"session_state,omitempty"`
	LastClient   contracts.HookClient   `json:"last_client,omitempty"`
	UpdatedAt    time.Time              `json:"updated_at,omitempty"`
	Task         *localStatusTask       `json:"task,omitempty"`
	LatestTest   localStatusTest        `json:"latest_test"`
}

type localStatusQueue struct {
	Pending    int `json:"pending"`
	Sending    int `json:"sending"`
	DeadLetter int `json:"dead_letter"`
}

type localStatusSnapshot struct {
	Authority         string                        `json:"authority"`
	GeneratedAt       time.Time                     `json:"generated_at"`
	ProjectID         string                        `json:"project_id"`
	ProjectName       string                        `json:"project_name"`
	Root              string                        `json:"root"`
	TeamID            string                        `json:"team_id,omitempty"`
	AgentID           string                        `json:"agent_id,omitempty"`
	RemoteBound       bool                          `json:"remote_bound"`
	Repository        continuity.RepositoryEvidence `json:"repository"`
	RepositoryError   string                        `json:"repository_error,omitempty"`
	LocalState        localStatusState              `json:"local_state"`
	ActiveTask        *localStatusTask              `json:"active_task,omitempty"`
	ActiveSessionID   string                        `json:"active_session_id,omitempty"`
	UnresolvedTasks   []localStatusTask             `json:"unresolved_tasks"`
	EventCount        int                           `json:"event_count"`
	Queue             localStatusQueue              `json:"queue"`
	Knowledge         map[string]any                `json:"knowledge,omitempty"`
	TencentDeployment map[string]any                `json:"tencent_deployment,omitempty"`
}

func (a *App) statusOutput(jsonOutput bool) (string, error) {
	resolved, err := project.Resolve("")
	if err != nil {
		return "", &cli.ExitError{Code: cli.ExitProjectNotInitialized, Err: errors.New("project is not initialized; run baron setup")}
	}
	if err := a.validateProjectBinding(resolved); err != nil {
		return "", err
	}
	ctx := context.Background()
	repository, repositoryErr := continuity.InspectRepository(ctx, resolved.Root)
	snapshot := localStatusSnapshot{
		Authority:       "git_worktree+sqlite",
		GeneratedAt:     time.Now().UTC(),
		ProjectID:       resolved.ProjectID,
		ProjectName:     resolved.Metadata.Name,
		Root:            resolved.Root,
		TeamID:          resolved.Binding.TeamID,
		AgentID:         resolved.Binding.AgentID,
		RemoteBound:     resolved.Binding.TeamID != "" && resolved.Binding.AgentID != "",
		Repository:      repository,
		UnresolvedTasks: []localStatusTask{},
	}
	if repositoryErr != nil {
		snapshot.RepositoryError = config.Redact(repositoryErr.Error(), nil)
	}

	store, err := storage.Open(filepath.Join(resolved.Root, ".baron", "runtime", "state.db"))
	if err != nil {
		return "", fmt.Errorf("open local Baron state: %w", err)
	}
	defer store.Close()
	if snapshot.EventCount, err = store.CountEvents(ctx, resolved.ProjectID); err != nil {
		return "", fmt.Errorf("read local event count: %w", err)
	}
	statuses := []contracts.TaskStatus{contracts.TaskPlanned, contracts.TaskInProgress, contracts.TaskBlocked, contracts.TaskFailed, contracts.TaskInterrupted}
	tasks, err := store.ListTasks(ctx, resolved.ProjectID, statuses, 100)
	if err != nil {
		return "", fmt.Errorf("read local task ledger: %w", err)
	}
	for _, task := range tasks {
		snapshot.UnresolvedTasks = append(snapshot.UnresolvedTasks, localStatusTaskFromRecord(task))
	}
	if taskID, sessionID, activeErr := store.ActiveTask(ctx, resolved.ProjectID); activeErr == nil {
		snapshot.ActiveSessionID = sessionID
		if task, taskErr := store.GetTask(ctx, resolved.ProjectID, taskID); taskErr == nil {
			converted := localStatusTaskFromRecord(task)
			snapshot.ActiveTask = &converted
		}
	} else if !errors.Is(activeErr, sql.ErrNoRows) {
		return "", fmt.Errorf("read active local task: %w", activeErr)
	}
	for _, status := range []string{"pending", "sending", "dead_letter"} {
		count, countErr := store.QueueCount(ctx, resolved.ProjectID, status)
		if countErr != nil {
			return "", fmt.Errorf("read local queue: %w", countErr)
		}
		switch status {
		case "pending":
			snapshot.Queue.Pending = count
		case "sending":
			snapshot.Queue.Sending = count
		case "dead_letter":
			snapshot.Queue.DeadLetter = count
		}
	}
	engine := continuity.NewEngine(store, resolved.ProjectID, resolved.Metadata.Name, continuity.CheckpointPath(resolved.Root))
	if state, stateErr := engine.Load(ctx); stateErr == nil {
		snapshot.LocalState = localStatusStateFromWorkState(state)
	} else if !errors.Is(stateErr, sql.ErrNoRows) {
		snapshot.LocalState.Error = config.Redact(stateErr.Error(), nil)
	} else {
		snapshot.LocalState.Available = false
	}
	if registry, registryErr := store.GetKnowledgeRegistry(ctx, resolved.ProjectID); registryErr == nil {
		pending := snapshot.Queue.Pending
		deadLetter := snapshot.Queue.DeadLetter
		snapshot.Knowledge = map[string]any{
			"wiki_id": registry.WikiID, "code_graph_id": registry.CodeGraphID,
			"wiki_status": registry.WikiStatus, "code_graph_status": registry.CodeGraphStatus,
			"wiki_ingest_status": registry.WikiIngestStatus, "code_graph_sync_status": registry.CodeGraphSyncStatus,
			"wiki_ingest_version": registry.WikiIngestVersion, "code_graph_commit": registry.CodeGraphCommit,
			"last_memory_sync_at": registry.LastMemorySyncAt, "conflict_status": registry.ConflictStatus,
			"superseded_by": registry.SupersededBy, "last_sync_commit": registry.LastSyncCommit,
			"pending_queue": pending, "dead_letter_queue": deadLetter,
			"last_error": config.Redact(registry.LastError, nil),
		}
	}
	if global, _, globalErr := a.loadGlobal(); globalErr == nil && global.TencentInstallPath != "" {
		if manifest, manifestErr := install.ReadTencentDeploymentManifest(global.TencentInstallPath); manifestErr == nil {
			snapshot.TencentDeployment = map[string]any{
				"repository": manifest.Repository, "requested_ref": manifest.RequestedRef,
				"resolved_commit": manifest.ResolvedCommit, "container_image_digests": manifest.ContainerImageDigests,
				"unresolved_containers": manifest.UnresolvedContainers, "updated_at": manifest.UpdatedAt,
			}
		}
	}
	return renderLocalStatus(snapshot, jsonOutput)
}

func localStatusTaskFromRecord(task storage.TaskRecord) localStatusTask {
	return localStatusTask{
		TaskID: task.TaskID, Goal: statusText(task.Goal, 1024), Status: task.Status,
		CurrentStep: statusText(task.CurrentStep, 512), NextAction: statusText(task.NextAction, 512),
		CompletionVerified: task.CompletionVerified, CompletionPolicy: task.CompletionPolicy,
		LatestVerificationRef: statusText(task.LatestVerificationRef, 256), LatestVerificationKind: task.LatestVerificationKind,
		LatestVerificationScope: statusText(task.LatestVerificationScope, 256), LatestErrorRef: statusText(task.LatestErrorRef, 256),
		UpdatedAt: task.UpdatedAt,
	}
}

func localStatusTaskFromState(task continuity.TaskState) *localStatusTask {
	if task.TaskID == "" && task.Goal == "" && task.Status == "" && task.CurrentStep == "" && task.NextAction == "" {
		return nil
	}
	result := localStatusTask{
		TaskID: task.TaskID, Goal: statusText(task.Goal, 1024), Status: task.Status,
		CurrentStep: statusText(task.CurrentStep, 512), NextAction: statusText(task.NextAction, 512),
		CompletionVerified: task.CompletionVerified, CompletionPolicy: task.CompletionPolicy,
		LatestVerificationKind: task.LatestVerificationKind, LatestVerificationScope: statusText(task.LatestVerificationScope, 256),
		LatestErrorRef: statusText(task.LatestErrorRef, 256),
	}
	if task.LatestVerificationEventID != "" {
		result.LatestVerificationRef = statusText(task.LatestVerificationEventID, 256)
	}
	return &result
}

func localStatusStateFromWorkState(state continuity.WorkState) localStatusState {
	return localStatusState{
		Available: true, SessionID: statusText(state.SessionID, 256), SessionState: state.SessionState,
		LastClient: state.LastClient, UpdatedAt: state.UpdatedAt, Task: localStatusTaskFromState(state.Task),
		LatestTest: localStatusTest{Command: statusText(state.LatestTest.Command, 256), Status: statusText(state.LatestTest.Status, 64), Summary: statusText(state.LatestTest.Summary, 512), ExitCode: state.LatestTest.ExitCode, ObservedAt: statusText(state.LatestTest.ObservedAt, 64)},
	}
}

func statusText(value string, max int) string {
	value = config.Redact(strings.TrimSpace(value), nil)
	if len(value) <= max {
		return value
	}
	const suffix = "...[truncated]"
	if max <= len(suffix) {
		return value[:max]
	}
	return value[:max-len(suffix)] + suffix
}

func renderLocalStatus(snapshot localStatusSnapshot, jsonOutput bool) (string, error) {
	if jsonOutput {
		data, err := json.Marshal(snapshot)
		if err != nil {
			return "", err
		}
		return string(append(data, '\n')), nil
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Authority: Git working tree + SQLite task ledger (Tencent not queried)\nProject: %s\nProject ID: %s\nRoot: %s\n", snapshot.ProjectName, snapshot.ProjectID, snapshot.Root)
	if snapshot.Repository.Branch != "" || snapshot.Repository.GitHead != "" {
		fmt.Fprintf(&builder, "Repository: branch=%s head=%s changed_files=%d\n", snapshot.Repository.Branch, snapshot.Repository.GitHead, len(snapshot.Repository.ChangedFiles))
	} else if snapshot.RepositoryError != "" {
		fmt.Fprintf(&builder, "Repository: unavailable (%s)\n", snapshot.RepositoryError)
	} else {
		fmt.Fprintf(&builder, "Repository: %s\n", snapshot.Repository.StatusSummary)
	}
	fmt.Fprintf(&builder, "Local session: %s", snapshot.LocalState.SessionState)
	if snapshot.LocalState.SessionID != "" {
		fmt.Fprintf(&builder, " (%s)", snapshot.LocalState.SessionID)
	}
	builder.WriteByte('\n')
	if snapshot.ActiveTask != nil {
		fmt.Fprintf(&builder, "Active task: %s [%s] %s\n", snapshot.ActiveTask.TaskID, snapshot.ActiveTask.Status, snapshot.ActiveTask.CurrentStep)
	}
	fmt.Fprintf(&builder, "Unresolved tasks: %d\n", len(snapshot.UnresolvedTasks))
	for _, task := range snapshot.UnresolvedTasks {
		fmt.Fprintf(&builder, "  %s [%s] step=%s next=%s verify=%s/%s\n", task.TaskID, task.Status, task.CurrentStep, task.NextAction, task.LatestVerificationKind, task.LatestVerificationScope)
	}
	fmt.Fprintf(&builder, "Events: %d; queue pending=%d sending=%d dead-letter=%d\n", snapshot.EventCount, snapshot.Queue.Pending, snapshot.Queue.Sending, snapshot.Queue.DeadLetter)
	if snapshot.LocalState.Error != "" {
		fmt.Fprintf(&builder, "Local state warning: %s\n", snapshot.LocalState.Error)
	}
	if snapshot.TencentDeployment != nil {
		fmt.Fprintf(&builder, "Tencent deployment metadata: %v @ %v (local manifest only)\n", snapshot.TencentDeployment["resolved_commit"], snapshot.TencentDeployment["repository"])
	}
	return builder.String(), nil
}

func (a *App) timelineOutput(limit int, jsonOutput bool) (string, error) {
	resolved, err := project.Resolve("")
	if err != nil {
		return "", &cli.ExitError{Code: cli.ExitProjectNotInitialized, Err: errors.New("project is not initialized; run baron setup")}
	}
	if err := a.validateProjectBinding(resolved); err != nil {
		return "", err
	}
	store, err := storage.Open(filepath.Join(resolved.Root, ".baron", "runtime", "state.db"))
	if err != nil {
		return "", fmt.Errorf("open local Baron state: %w", err)
	}
	defer store.Close()
	events, err := store.ListTimeline(context.Background(), resolved.ProjectID, limit)
	if err != nil {
		return "", err
	}
	if events == nil {
		events = []storage.TimelineEvent{}
	}
	if jsonOutput {
		data, marshalErr := json.Marshal(map[string]any{"authority": "sqlite_event_ledger", "project_id": resolved.ProjectID, "events": events})
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(append(data, '\n')), nil
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Local timeline: %d events (SQLite; raw payload hidden)\n", len(events))
	for _, event := range events {
		fmt.Fprintf(&builder, "[%s] %s %s", event.OccurredAt.Format(time.RFC3339), event.Client, event.Type)
		if event.TaskID != "" {
			fmt.Fprintf(&builder, " task=%s", event.TaskID)
		}
		if event.Status != "" {
			fmt.Fprintf(&builder, " status=%s", event.Status)
		}
		if event.VerificationKind != "" {
			fmt.Fprintf(&builder, " verify=%s/%s", event.VerificationKind, event.VerificationScope)
		}
		if event.ExitCode != nil {
			fmt.Fprintf(&builder, " exit=%d", *event.ExitCode)
		}
		if event.Summary != "" {
			fmt.Fprintf(&builder, " - %s", event.Summary)
		}
		builder.WriteByte('\n')
	}
	return builder.String(), nil
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
	if errors.Is(err, credentials.ErrProviderUnavailable) {
		return &cli.ExitError{Code: cli.ExitTencentUnavailable, Err: err}
	}
	if errors.Is(err, credentials.ErrInvalidProviderCredential) {
		return &cli.ExitError{Code: cli.ExitAuthIncomplete, Err: err}
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
	transport := &http.Transport{}
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	}
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.MaxIdleConns = 16
	transport.MaxIdleConnsPerHost = 8
	return &App{HTTPClient: &http.Client{
		Timeout:   3 * time.Second,
		Transport: transport,
	}, CommandRunner: doctor.OSProbe{}}
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

func (a *App) permissionDirectory() (string, error) {
	path, err := a.globalPath()
	if err != nil {
		return "", err
	}
	global, err := config.LoadGlobalState(path)
	if err != nil {
		return "", err
	}
	if recorded := strings.TrimSpace(global.PermissionDirectory); recorded != "" {
		if err := permissions.ValidateDirectory(recorded); err != nil {
			return "", err
		}
		return filepath.Clean(recorded), nil
	}
	for _, candidate := range a.permissionPathCandidates() {
		if permissions.DirectoryOnPath(candidate) && permissions.DirectoryIsWritable(candidate) {
			return filepath.Clean(candidate), nil
		}
	}
	return permissions.DefaultDirectory(path)
}

func (a *App) permissionPathCandidates() []string {
	candidates := make([]string, 0, 1)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || !filepath.IsAbs(value) {
			return
		}
		value = filepath.Clean(value)
		for _, existing := range candidates {
			if sameAppPath(existing, value) {
				return
			}
		}
		candidates = append(candidates, value)
	}
	if executable := a.executablePathForPermissions(); executable != "" {
		add(filepath.Dir(executable))
	}
	for _, entry := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		add(entry)
	}
	return candidates
}

func (a *App) executablePathForPermissions() string {
	path := strings.TrimSpace(a.ExecutablePath)
	if path == "" {
		path, _ = release.CurrentExecutablePath()
	}
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return absolute
}

func sameAppPath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func (a *App) enablePermissions() (string, error) {
	directory, err := a.permissionDirectory()
	if err != nil {
		return "", err
	}
	global, globalPath, err := a.loadGlobal()
	if err != nil {
		return "", err
	}
	global.PermissionDirectory = directory
	if err := a.saveGlobal(globalPath, global); err != nil {
		return "", fmt.Errorf("record permission launcher directory: %w", err)
	}
	status, err := permissions.Enable(directory)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Explicit auto-accept launchers enabled for DSH and Codex at %s.\nWARNING: these launchers disable approval prompts and sandbox restrictions.\n%s", status.Directory, permissions.Instructions(status.Directory)), nil
}

func (a *App) disablePermissions() (string, error) {
	directory, err := a.permissionDirectory()
	if err != nil {
		return "", err
	}
	status, err := permissions.Disable(directory)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Explicit auto-accept launchers disabled at %s.", status.Directory), nil
}

func (a *App) permissionsStatus() (string, error) {
	directory, err := a.permissionDirectory()
	if err != nil {
		return "", err
	}
	status := permissions.Inspect(directory)
	return fmt.Sprintf("Auto-accept DSH: %t (%s)\nAuto-accept Codex: %t (%s)", status.DSHEnabled, status.DSHPath, status.CodexEnabled, status.CodexPath), nil
}

func (a *App) uninstallOptions(purgeShared bool) (baronuninstall.Options, error) {
	global, globalPath, err := a.loadGlobal()
	if err != nil {
		return baronuninstall.Options{}, err
	}
	environment := processEnvironment()
	dshHome, err := install.DSHHome(environment)
	if err != nil {
		return baronuninstall.Options{}, err
	}
	if global.DSHHomePath != "" {
		dshHome = filepath.Clean(global.DSHHomePath)
	} else if global.DSHProfilePatchPath != "" {
		dshHome = filepath.Dir(filepath.Dir(filepath.Dir(global.DSHProfilePatchPath)))
	}
	dshCredentialPath, err := install.DSHCredentialPath(environment)
	if err != nil {
		return baronuninstall.Options{}, err
	}
	if global.DSHHomePath != "" || global.DSHProfilePatchPath != "" {
		dshCredentialPath = filepath.Join(dshHome, ".credentials.yaml")
	}
	codexHome, err := install.CodexHome()
	if err != nil {
		return baronuninstall.Options{}, err
	}
	if global.CodexHomePath != "" {
		codexHome = filepath.Clean(global.CodexHomePath)
	} else if global.CodexHooksPath != "" {
		codexHome = filepath.Dir(global.CodexHooksPath)
	}
	codexHooksPath := global.CodexHooksPath
	if codexHooksPath == "" {
		codexHooksPath, err = install.CodexHooksPath()
		if err != nil {
			return baronuninstall.Options{}, err
		}
	}
	permissionDirectory, err := a.permissionDirectory()
	if err != nil {
		return baronuninstall.Options{}, err
	}
	patchPaths := make([]string, 0, 3)
	if global.DSHProfilePatchPath != "" {
		patchPaths = append(patchPaths, global.DSHProfilePatchPath)
	}
	patchHome := ""
	if global.DSHHomePath != "" || global.DSHProfilePatchPath != "" {
		patchHome = dshHome
	}
	for _, profile := range []string{"web", "headless"} {
		var patch string
		if patchHome != "" {
			patch = filepath.Join(patchHome, "profiles", profile, "cordis.patch.yml")
		} else if patchPath, patchErr := install.DSHProfilePatchPath(profile); patchErr == nil {
			patch = patchPath
		}
		if patch != "" && !containsStringValue(patchPaths, patch) {
			patchPaths = append(patchPaths, patch)
		}
	}
	projectRoots := make([]string, 0, len(global.ProjectRoots)+1)
	for _, root := range global.ProjectRoots {
		if !containsStringValue(projectRoots, root) {
			projectRoots = append(projectRoots, root)
		}
	}
	if current, resolveErr := project.Resolve(""); resolveErr == nil && !containsStringValue(projectRoots, current.Root) {
		projectRoots = append(projectRoots, current.Root)
	}
	executablePath := a.ExecutablePath
	if executablePath == "" {
		executablePath, err = release.CurrentExecutablePath()
		if err != nil {
			return baronuninstall.Options{}, err
		}
	}
	homeDir := ""
	sourceCheckouts := []string(nil)
	environmentFiles := []string(nil)
	if purgeShared {
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return baronuninstall.Options{}, fmt.Errorf("resolve user home for full uninstall: %w", err)
		}
		sourceCheckouts = baronuninstall.DiscoverBaronSourceCheckouts(homeDir)
		environmentFiles = baronuninstall.DefaultEnvironmentFiles(homeDir)
	}
	return baronuninstall.Options{
		GlobalPath:           globalPath,
		DSHConfigPath:        global.DSHConfigPath,
		DSHHome:              dshHome,
		DSHCredentialPath:    dshCredentialPath,
		DSHProfilePatchPaths: patchPaths,
		CodexHome:            codexHome,
		CodexHooksPath:       codexHooksPath,
		CodexAdapterPath:     global.CodexAdapterPath,
		PermissionsDirectory: permissionDirectory,
		TencentInstallPath:   global.TencentInstallPath,
		Receipts:             append([]string(nil), global.Receipts...),
		ProjectRoots:         projectRoots,
		ExecutablePath:       executablePath,
		PurgeShared:          purgeShared,
		PurgeAll:             purgeShared,
		HomeDir:              homeDir,
		SourceCheckouts:      sourceCheckouts,
		EnvironmentFiles:     environmentFiles,
		GOOS:                 runtime.GOOS,
		Runner:               a.commandRunner(),
	}, nil
}

func (a *App) uninstallPlan(purgeShared bool) (string, error) {
	options, err := a.uninstallOptions(purgeShared)
	if err != nil {
		return "", err
	}
	plan, err := baronuninstall.BuildPlan(options)
	if err != nil {
		return "", err
	}
	return plan.String(), nil
}

func (a *App) uninstall(purgeShared bool) (string, error) {
	options, err := a.uninstallOptions(purgeShared)
	if err != nil {
		return "", err
	}
	report, err := baronuninstall.Execute(context.Background(), options)
	if err != nil {
		return report.String(), err
	}
	return report.String(), nil
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
	global.ProjectRoots[result.ProjectID] = result.Root
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
	case "taskstarted", "task_started":
		return contracts.EventTaskStarted
	case "taskupdated", "task_updated":
		return contracts.EventTaskUpdated
	case "taskfailed", "task_failed":
		return contracts.EventTaskFailed
	case "taskblocked", "task_blocked":
		return contracts.EventTaskBlocked
	case "taskverified", "task_verified":
		return contracts.EventTaskVerified
	case "taskcompleted", "task_completed":
		return contracts.EventTaskCompleted
	case "taskinterrupted", "task_interrupted":
		return contracts.EventTaskInterrupted
	default:
		return contracts.EventType(key)
	}
}

func (a *App) DSHInit() error {
	return a.dshInitWithPlan(context.Background(), BootstrapPlan{}, nil)
}

func (a *App) dshInitWithPlan(ctx context.Context, plan BootstrapPlan, reporter install.ProgressReporter) error {
	if _, err := a.resolveDSHCredential(); err != nil {
		return err
	}
	global, path, err := a.loadGlobal()
	if err != nil {
		return err
	}
	if dshHome, homeErr := install.DSHHome(processEnvironment()); homeErr == nil {
		global.DSHHomePath = dshHome
	}
	runner := a.commandRunner()
	dshDependency := install.DependencyReport{}
	if state, ok := plan.State("dsh"); ok {
		dshDependency, err = install.EnsureNPMDependencyState(ctx, runner, install.NPMDependencySpec{Name: "DSH", Package: "@deepseek-ai/dsh", Command: "dsh"}, state, reporter)
	} else {
		dshDependency, err = install.InstallDSHLatestWithReport(ctx, runner, reporter)
	}
	if err != nil {
		return err
	}
	dshVersion := dshDependency.State.LocalVersion
	pluginReport, err := install.InstallDSHPluginsWithReport(ctx, runner, dshVersion, reporter)
	if err != nil {
		return err
	}
	dshChanged := dshDependency.State.Changed || pluginReport.Changed
	globalDir := filepath.Dir(path)
	adapterPath := filepath.Join(globalDir, "dsh-adapter")
	adapterChanged, err := install.InstallEmbeddedDSHAdapterWithChange(adapterPath)
	if err != nil {
		return err
	}
	dshChanged = dshChanged || adapterChanged
	profilePatchPath := ""
	for _, profile := range []string{"web", "headless"} {
		dump, err := runner.Run(ctx, "dsh", "--profile", profile, "--dump-config")
		if err != nil {
			return fmt.Errorf("inspect the Baron DSH adapter in the %s profile: %w", profile, err)
		}
		if !install.DSHProfileHasMarker(dump, "baron-dsh-adapter") {
			if _, err := runner.Run(ctx, "dsh", "plugin", "--profile", profile, "add", adapterPath); err != nil {
				return fmt.Errorf("install the Baron DSH adapter into the %s profile: %w", profile, err)
			}
			dshChanged = true
		}
		patchPath, patchErr := install.DSHProfilePatchPath(profile)
		if patchErr != nil {
			return patchErr
		}
		patchChanged, err := install.EnsureDSHProfilePatchWithChange(patchPath)
		if err != nil {
			return err
		}
		dshChanged = dshChanged || patchChanged
		if profile == "web" {
			profilePatchPath = patchPath
		}
	}
	global.DSHConfigPath = filepath.Join(globalDir, "dsh.json")
	global.DSHProfilePatchPath = profilePatchPath
	baseline, baselineChanged, err := installDSHWithChange(global.DSHConfigPath, dshVersion)
	if err != nil {
		return err
	}
	dshChanged = dshChanged || baselineChanged
	if err := install.VerifyDSHProfile(ctx, runner); err != nil {
		return err
	}
	if dshChanged {
		if err := install.ProbeDSHStartup(ctx, runner); err != nil {
			return err
		}
	}
	global.DSHComponents = map[string]bool{}
	for _, component := range baseline.Components {
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
	if _, err := install.WriteReceiptIfChanged(receiptPath, install.Receipt{Component: "deepseek-harness", Version: dshVersion, Source: "npm:@deepseek-ai/dsh"}); err != nil {
		return err
	}
	global.Receipts = appendReceipt(global.Receipts, receiptPath)
	return a.saveGlobal(path, global)
}

func (a *App) CodexInit() error {
	return a.codexInitWithPlan(context.Background(), BootstrapPlan{}, nil)
}

func (a *App) codexInitWithPlan(ctx context.Context, plan BootstrapPlan, reporter install.ProgressReporter) error {
	runner := a.commandRunner()
	var err error
	codexDependency := install.DependencyReport{}
	if state, ok := plan.State("codex"); ok {
		codexDependency, err = install.EnsureNPMDependencyState(ctx, runner, install.NPMDependencySpec{Name: "Codex", Package: "@openai/codex", Command: "codex"}, state, reporter)
	} else {
		codexDependency, err = install.InstallCodexLatestWithReport(ctx, runner, reporter)
	}
	if err != nil {
		return err
	}
	codexSource, codexVersion := codexDependency.Source, codexDependency.State.LocalVersion
	global, path, err := a.loadGlobal()
	if err != nil {
		return err
	}
	codexHome, err := install.CodexHome()
	if err != nil {
		return err
	}
	global.CodexHomePath = codexHome
	codexHooksPath, err := install.CodexHooksPath()
	if err != nil {
		return err
	}
	global.CodexHooksPath = codexHooksPath
	globalDir := filepath.Dir(path)
	global.CodexAdapterPath = filepath.Join(globalDir, "codex-adapter")
	if _, err := install.InstallEmbeddedCodexAdapterWithChange(global.CodexAdapterPath); err != nil {
		return err
	}
	if _, err := mergeCodexWithChange(global.CodexHooksPath); err != nil {
		return err
	}
	global.CodexHooksInstalled = true
	receiptPath := filepath.Join(globalDir, "receipts", "codex.json")
	if _, err := install.WriteReceiptIfChanged(receiptPath, install.Receipt{Component: "codex-cli", Version: codexVersion, Source: codexSource}); err != nil {
		return err
	}
	global.Receipts = appendReceipt(global.Receipts, receiptPath)
	adapterReceiptPath := filepath.Join(globalDir, "receipts", "codex-adapter.json")
	if _, err := install.WriteReceiptIfChanged(adapterReceiptPath, install.Receipt{Component: "codex-adapter", Version: install.EmbeddedCodexAdapterVersion, Source: "embedded:adapters/codex"}); err != nil {
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
		if err := a.validateProviderCredential(context.Background(), dshProviderBaseURL(values), key); err == nil {
			return key, nil
		} else if strings.TrimSpace(values["DEEPSEEK_API_KEY"]) != "" {
			// An inherited environment value wins over the official DSH store
			// for every child process. Do not pretend a replacement store value
			// repaired an invalid environment override.
			return "", credentialValidationFailure("DeepSeek", "baron deepseek-harness init", err)
		} else if !errors.Is(err, credentials.ErrInvalidProviderCredential) {
			return "", credentialValidationFailure("DeepSeek", "baron deepseek-harness init", err)
		}
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
			if err := a.validateProviderCredential(context.Background(), firstNonEmptyString(managed.MemoryLLMBaseURL, dshProviderBaseURL(values)), managed.MemoryLLMAPIKey); err == nil {
				if err := install.EnsureDSHProviderKey(values, managed.MemoryLLMAPIKey); err != nil {
					return "", err
				}
				return managed.MemoryLLMAPIKey, nil
			} else if !errors.Is(err, credentials.ErrInvalidProviderCredential) {
				return "", credentialValidationFailure("DeepSeek", "baron deepseek-harness init", err)
			}
		}
	}

	baseURL := dshProviderBaseURL(values)
	for attempt := 0; attempt < 3; attempt++ {
		key, promptErr := a.prompter().VisibleSecret("DeepSeek API key (DEEPSEEK_API_KEY)")
		if promptErr != nil {
			if errors.Is(promptErr, credentials.ErrEmptyValue) {
				continue
			}
			return "", credentialPromptFailure("DeepSeek", "DEEPSEEK_API_KEY", "baron deepseek-harness init", promptErr)
		}
		if validationErr := a.validateProviderCredential(context.Background(), baseURL, key); validationErr != nil {
			if errors.Is(validationErr, credentials.ErrInvalidProviderCredential) {
				continue
			}
			return "", credentialValidationFailure("DeepSeek", "baron deepseek-harness init", validationErr)
		}
		if err := install.EnsureDSHProviderKey(values, key); err != nil {
			return "", err
		}
		return key, nil
	}
	return "", credentialValidationFailure("DeepSeek", "baron deepseek-harness init", credentials.ErrInvalidProviderCredential)
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
		if validationErr := a.validateProviderCredential(context.Background(), resolved.MemoryLLMBaseURL, resolved.MemoryLLMAPIKey); validationErr == nil {
			return resolved, nil
		} else if !errors.Is(validationErr, credentials.ErrInvalidProviderCredential) {
			return install.TencentRuntimeConfig{}, credentialValidationFailure("Tencent", "baron tencent-memory init", validationErr)
		}
	}

	for attempt := 0; attempt < 3; attempt++ {
		key, promptErr := a.prompter().VisibleSecret("Tencent provider API key (DEEPSEEK_API_KEY)")
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
			if validationErr := a.validateProviderCredential(context.Background(), resolved.MemoryLLMBaseURL, resolved.MemoryLLMAPIKey); validationErr != nil {
				if errors.Is(validationErr, credentials.ErrInvalidProviderCredential) {
					continue
				}
				return install.TencentRuntimeConfig{}, credentialValidationFailure("Tencent", "baron tencent-memory init", validationErr)
			}
			return resolved, nil
		}
	}
	return install.TencentRuntimeConfig{}, credentialPromptFailure("Tencent", "DEEPSEEK_API_KEY", "baron tencent-memory init", credentials.ErrEmptyValue)
}

// SetCredential is the explicit provider-key rotation command. Validation is
// completed before either provider-owned store is changed.
func (a *App) SetCredential(provider string) error {
	if strings.ToLower(strings.TrimSpace(provider)) != "deepseek" {
		return fmt.Errorf("unsupported credential provider %q", provider)
	}
	values := processEnvironment()
	global, _, err := a.loadGlobal()
	if err != nil {
		return err
	}
	managed := install.TencentRuntimeConfig{}
	if strings.TrimSpace(global.TencentInstallPath) != "" {
		managed, err = install.LoadTencentRuntimeConfig(global.TencentInstallPath)
		if err != nil {
			return err
		}
	}
	baseURL := configuredProviderBaseURL(values, managed.MemoryLLMBaseURL)
	var key string
	for attempt := 0; attempt < 3; attempt++ {
		candidate, promptErr := a.prompter().VisibleSecret("DeepSeek API key")
		if promptErr != nil {
			if errors.Is(promptErr, credentials.ErrEmptyValue) {
				continue
			}
			return credentialPromptFailure("DeepSeek", "DEEPSEEK_API_KEY", deepseekCredentialCommand, promptErr)
		}
		if validationErr := a.validateProviderCredential(context.Background(), baseURL, candidate); validationErr != nil {
			if errors.Is(validationErr, credentials.ErrInvalidProviderCredential) {
				continue
			}
			return credentialValidationFailure("DeepSeek", deepseekCredentialCommand, validationErr)
		}
		key = candidate
		break
	}
	if key == "" {
		return credentialValidationFailure("DeepSeek", deepseekCredentialCommand, credentials.ErrInvalidProviderCredential)
	}
	if err := install.EnsureDSHProviderKey(values, key); err != nil {
		return err
	}
	if strings.TrimSpace(global.TencentInstallPath) != "" {
		if replaceErr := install.ReplaceTencentRuntimeAPIKey(filepath.Join(global.TencentInstallPath, "deploy", "global-images"), key); replaceErr != nil && !errors.Is(replaceErr, os.ErrNotExist) {
			return replaceErr
		}
	}
	return nil
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
	return &credentials.Prompter{In: a.Input, Out: a.PromptOutput, ReadSecret: a.ReadSecret, ReadLine: a.ReadLine, BeforeInput: a.prepareForInput}
}

func (a *App) validateProviderCredential(ctx context.Context, baseURL, key string) error {
	if a.ValidateProviderCredential != nil {
		return a.ValidateProviderCredential(ctx, baseURL, key)
	}
	return credentials.ValidateOpenAICompatible(ctx, a.HTTPClient, baseURL, key)
}

func dshProviderBaseURL(values map[string]string) string {
	return configuredProviderBaseURL(values, "")
}

func configuredProviderBaseURL(values map[string]string, fallback string) string {
	for _, name := range []string{"BARON_DSH_LLM_BASE_URL", "DEEPSEEK_BASE_URL", "OPENAI_BASE_URL"} {
		if value := strings.TrimSpace(values[name]); value != "" {
			return value
		}
	}
	if value := strings.TrimSpace(fallback); value != "" {
		return value
	}
	return install.DefaultTencentRuntimeConfig().MemoryLLMBaseURL
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

func credentialValidationFailure(component, command string, err error) error {
	if errors.Is(err, credentials.ErrInvalidProviderCredential) {
		return providerValidationError{message: fmt.Sprintf("%s provider credential was rejected; rerun %s or use %s", component, command, deepseekCredentialCommand), cause: err}
	}
	return providerValidationError{message: fmt.Sprintf("%s provider credential could not be validated; the provider or network is unavailable; rerun %s", component, command), cause: err}
}

type providerValidationError struct {
	message string
	cause   error
}

func (e providerValidationError) Error() string { return e.message }
func (e providerValidationError) Unwrap() error { return e.cause }

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

func containsStringValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
func installDSH(path, dshVersion string) (installReport, error) {
	report, _, err := installDSHWithChange(path, dshVersion)
	return installReport{Components: report.Components}, err
}

func installDSHWithChange(path, dshVersion string) (install.DSHReport, bool, error) {
	report, changed, err := install.EnsureDSHBaselineWithChange(path, install.DSHOptions{AdapterCommand: "baron hook dsh", Version: dshVersion})
	return report, changed, err
}

func mergeCodexWithChange(path string) (bool, error) {
	return install.MergeCodexHooksWithChange(path, "baron")
}

func mergeCodex(path string) error { return install.MergeCodexHooks(path, "baron") }

type installReport struct{ Components []string }
