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
	"path/filepath"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/cli"
	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/continuity"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/doctor"
	"github.com/baron-shared-brain/baron/internal/hooks"
	"github.com/baron-shared-brain/baron/internal/install"
	"github.com/baron-shared-brain/baron/internal/memory/tencent"
	"github.com/baron-shared-brain/baron/internal/project"
	"github.com/baron-shared-brain/baron/internal/storage"
)

type App struct {
	GlobalPath         string
	HTTPClient         *http.Client
	ProjectProvisioner func(context.Context, string, string) (contracts.ProjectBinding, error)
	CommandRunner      install.CommandRunner
}

func (a *App) CLIOptions(out, errOut io.Writer) cli.Options {
	return cli.Options{
		Out: out, Err: errOut,
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
	if resolved, resolveErr := project.Resolve(""); resolveErr == nil {
		credentialPaths = append(credentialPaths, resolved.EnvPath)
	}
	return doctor.Check(context.Background(), doctor.Options{
		DSHComponents:      global.DSHComponents,
		CodexAuthenticated: codexAuthReady(),
		TencentEndpoint:    global.Identity.Endpoint,
		HubEndpoint:        global.Identity.HubEndpoint,
		ProxyEndpoint:      os.Getenv("BARON_TENCENT_PROXY_ENDPOINT"),
		CredentialPaths:    credentialPaths,
		HTTPClient:         a.HTTPClient,
	}), nil
}

func codexAuthReady() bool {
	if os.Getenv("BARON_CODEX_AUTH_READY") == "1" || os.Getenv("OPENAI_API_KEY") != "" {
		return true
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(configDir, "codex", "auth.json"))
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
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
	if jsonOutput {
		data, marshalErr := json.Marshal(result)
		return string(append(data, '\n')), marshalErr
	}
	return fmt.Sprintf("Project: %s\nProject ID: %s\nTeam: %s\nAgent: %s\n", resolved.Metadata.Name, resolved.ProjectID, resolved.Binding.TeamID, resolved.Binding.AgentID), nil
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
	if global.CodexHooksPath != "" {
		if err := install.MergeCodexHooks(global.CodexHooksPath, "baron"); err != nil {
			return err
		}
	}
	if resolved, resolveErr := project.Resolve(""); resolveErr == nil && global.Identity.Endpoint != "" {
		store, openErr := storage.Open(filepath.Join(resolved.Root, ".baron", "runtime", "state.db"))
		if openErr != nil {
			return openErr
		}
		defer store.Close()
		identity := resolved.Identity
		if identity.UserKey == "" {
			identity.UserKey = global.Identity.UserKey
		}
		isolation := resolved.IsolationContext()
		if isolation.UserID == "" {
			isolation.UserID = global.Identity.UserID
		}
		backend := tencent.NewClient(tencent.Config{Endpoint: global.Identity.Endpoint, HubEndpoint: global.Identity.HubEndpoint, UserKey: identity.UserKey, ServiceID: global.Identity.ServiceID, HTTPClient: a.HTTPClient})
		secrets := []string{identity.UserKey}
		if env, envErr := config.ReadEnvFile(resolved.EnvPath); envErr == nil {
			secrets = append(secrets, env["BARON_TENCENT_USER_KEY"])
		}
		syncer := continuity.NewSyncer(store, backend, isolation, secrets)
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
	case strings.Contains(message, "integrity") || strings.Contains(message, "symlink"):
		code = cli.ExitIntegrityFailure
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
	if identity.Endpoint != "" && isolation.TeamID != "" && isolation.AgentID != "" && isolation.UserID != "" {
		runtime.SetMemoryBackend(tencent.NewClient(tencent.Config{
			Endpoint: identity.Endpoint, HubEndpoint: identity.HubEndpoint, UserKey: identity.UserKey,
			ServiceID: identity.ServiceID, HTTPClient: a.HTTPClient,
		}), isolation)
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
	if _, err := a.commandRunner().Run(context.Background(), "dsh", "plugin", "--profile", "web", "add", adapterPath); err != nil {
		return errors.New("install the Baron DSH adapter into the web profile")
	}
	profilePatchPath, err := install.DSHProfilePatchPath("web")
	if err != nil {
		return err
	}
	if err := install.EnsureDSHProfilePatch(profilePatchPath); err != nil {
		return err
	}
	if err := install.VerifyDSHProfile(context.Background(), a.commandRunner()); err != nil {
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
	if err := install.InstallCodex(context.Background(), a.commandRunner(), "0.149.0"); err != nil {
		return err
	}
	global, path, err := a.loadGlobal()
	if err != nil {
		return err
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	global.CodexHooksPath = filepath.Join(configDir, "codex", "hooks.json")
	if err := mergeCodex(global.CodexHooksPath); err != nil {
		return err
	}
	global.CodexHooksInstalled = true
	receiptPath := filepath.Join(filepath.Dir(path), "receipts", "codex.json")
	if err := install.WriteReceipt(receiptPath, install.Receipt{Component: "codex-cli", Version: "0.149.0", Source: "npm:@openai/codex"}); err != nil {
		return err
	}
	global.Receipts = appendReceipt(global.Receipts, receiptPath)
	return a.saveGlobal(path, global)
}

func (a *App) TencentInit(ctx context.Context) error {
	runner := a.commandRunner()
	if _, err := runner.LookPath("docker"); err != nil {
		return errors.New("Docker CLI is required for Tencent Agent Memory initialization; install Docker first")
	}
	global, path, err := a.loadGlobal()
	if err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "docker", "info"); err != nil {
		return errors.New("Docker daemon is unavailable; start Docker and rerun baron tencent-memory init")
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
	serviceID := firstNonEmptyString(os.Getenv("BARON_TENCENT_SERVICE_ID"), "default")
	deploymentRoot := os.Getenv("BARON_TENCENT_MEMORY_DIR")
	if deploymentRoot == "" {
		deploymentRoot = filepath.Join(filepath.Dir(path), "tencent-memory")
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
		return nil
	}
	if healthErr := health(); healthErr != nil {
		if os.Getenv("BARON_TENCENT_SKIP_DEPLOY") == "1" {
			return healthErr
		}
		if err := install.EnsureTencentDeployment(ctx, runner, install.TencentDeploymentOptions{Root: deploymentRoot}); err != nil {
			return fmt.Errorf("Tencent services are unavailable and automatic deployment failed: %w", err)
		}
		if adminKey == "" {
			adminKey, err = install.TencentAdminKey(deploymentRoot)
			if err != nil {
				return errors.New("Tencent deployment did not produce a readable admin key; inspect the managed deployment")
			}
			client = tencent.NewClient(tencent.Config{Endpoint: endpoint, HubEndpoint: hubEndpoint, AdminKey: adminKey, ServiceID: serviceID, HTTPClient: a.HTTPClient})
		}
		if err := health(); err != nil {
			return err
		}
	}
	identity, err := client.EnsureIdentity(ctx, contracts.IdentitySpec{UserName: "baron", TeamName: "baron-projects"})
	if err != nil {
		return err
	}
	identityClient := tencent.NewClient(tencent.Config{Endpoint: endpoint, HubEndpoint: hubEndpoint, UserKey: identity.UserKey, ServiceID: identity.ServiceID, HTTPClient: a.HTTPClient})
	if err := identityClient.VerifyAuth(ctx); err != nil {
		return err
	}
	global.Identity = identity
	global.TencentInstallPath = deploymentRoot
	receiptPath := filepath.Join(filepath.Dir(path), "receipts", "tencent-memory.json")
	if err := install.WriteReceipt(receiptPath, install.Receipt{Component: "tencent-memory", Version: "v3", Source: "TencentDB Agent Memory local deployment"}); err != nil {
		return err
	}
	global.Receipts = appendReceipt(global.Receipts, receiptPath)
	return a.saveGlobal(path, global)
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

// The imports are kept behind these tiny adapters so app wiring remains easy
// to replace with fixture runners in tests.
func installDSH(path string) (installReport, error) {
	report, err := install.EnsureDSHBaseline(path, install.DSHOptions{AdapterCommand: "baron hook dsh"})
	return installReport{Components: report.Components}, err
}

func mergeCodex(path string) error { return install.MergeCodexHooks(path, "baron") }

type installReport struct{ Components []string }
