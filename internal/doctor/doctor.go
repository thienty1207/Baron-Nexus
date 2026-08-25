package doctor

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
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/install"
)

type Status string

const (
	StatusReady       Status = "ready"
	StatusMissing     Status = "missing"
	StatusUnavailable Status = "unavailable"
	StatusIncomplete  Status = "incomplete"
	StatusWarning     Status = "warning"
	StatusSkipped     Status = "skipped"
)

type CheckResult struct {
	Name       string `json:"name"`
	Status     Status `json:"status"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

type Report struct {
	Ready    bool          `json:"ready"`
	ExitCode int           `json:"exit_code"`
	Checks   []CheckResult `json:"checks"`
}

func (r Report) ByName(name string) CheckResult {
	for _, check := range r.Checks {
		if check.Name == name {
			return check
		}
	}
	return CheckResult{Name: name, Status: StatusSkipped, Message: "check not configured"}
}

func (r Report) Human() string {
	var builder strings.Builder
	for _, check := range r.Checks {
		prefix := "[OK]"
		if check.Status != StatusReady {
			prefix = "[ERROR]"
			if check.Status == StatusWarning || check.Status == StatusSkipped {
				prefix = "[WARN]"
			}
		}
		fmt.Fprintf(&builder, "%s %s: %s", prefix, check.Name, check.Message)
		if check.Suggestion != "" {
			fmt.Fprintf(&builder, " Run: %s", check.Suggestion)
		}
		builder.WriteByte('\n')
	}
	if r.Ready {
		builder.WriteString("All required components are ready.\n")
	}
	return builder.String()
}

func (r Report) WriteJSON(w io.Writer) error {
	return json.NewEncoder(w).Encode(r)
}

type CommandProbe interface {
	LookPath(name string) (string, error)
	Run(context.Context, string, ...string) (string, error)
}

type OSProbe struct{}

func (OSProbe) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (OSProbe) Run(ctx context.Context, name string, args ...string) (string, error) {
	return stringOutput(exec.CommandContext(ctx, name, args...))
}

// RunInteractive is used only for safe OS authentication preflights such as
// `sudo -v`. The password is read by sudo from the user's terminal; Baron
// never receives or persists it.
func (OSProbe) RunInteractive(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = os.Stdin
	var output bytes.Buffer
	command.Stdout = io.MultiWriter(os.Stdout, &output)
	command.Stderr = io.MultiWriter(os.Stderr, &output)
	err := command.Run()
	return output.String(), err
}

type Options struct {
	Probe               CommandProbe
	DSHComponents       map[string]bool
	DSHProviderChecked  bool
	DSHProviderReady    bool
	CodexAuthenticated  bool
	CodexProjectTrusted bool
	TencentReady        bool
	TencentEndpoint     string
	HubEndpoint         string
	KnowledgeEndpoint   string
	ProxyEndpoint       string
	CredentialPaths     []string
	CodexHooksPath      string
	SurfaceChecks       []CheckResult
	HTTPClient          *http.Client
	LinuxBootstrap      bool
}

func Check(ctx context.Context, options Options) Report {
	if options.Probe == nil {
		options.Probe = OSProbe{}
	}
	report := Report{}
	add := func(result CheckResult) { report.Checks = append(report.Checks, result) }

	_, dockerErr := options.Probe.LookPath("docker")
	if dockerErr != nil {
		add(CheckResult{Name: "docker-cli", Status: StatusMissing, Message: "Docker CLI is not installed.", Suggestion: "install Docker, then rerun baron test"})
	} else {
		add(CheckResult{Name: "docker-cli", Status: StatusReady, Message: "Docker CLI is installed."})
		if _, err := options.Probe.Run(ctx, "docker", "info"); err != nil {
			if _, sudoErr := options.Probe.LookPath("sudo"); sudoErr == nil {
				if _, sudoDockerErr := options.Probe.Run(ctx, "sudo", "-n", "docker", "info"); sudoDockerErr == nil {
					add(CheckResult{Name: "docker-daemon", Status: StatusReady, Message: "Docker daemon is reachable through sudo; Baron will manage it without changing docker-group membership."})
				} else {
					add(CheckResult{Name: "docker-daemon", Status: StatusUnavailable, Message: "Docker daemon is unavailable.", Suggestion: "start Docker, then rerun baron test"})
				}
			} else {
				add(CheckResult{Name: "docker-daemon", Status: StatusUnavailable, Message: "Docker daemon is unavailable.", Suggestion: "start Docker, then rerun baron test"})
			}
		} else {
			add(CheckResult{Name: "docker-daemon", Status: StatusReady, Message: "Docker daemon is reachable."})
		}
	}

	if options.LinuxBootstrap {
		if _, err := options.Probe.LookPath("apt-get"); err != nil {
			add(CheckResult{Name: "docker-package-manager", Status: StatusMissing, Message: "apt-get is not available for the Ubuntu/Debian Docker bootstrap.", Suggestion: "install apt-get or use a supported Ubuntu/Debian release"})
		} else {
			add(CheckResult{Name: "docker-package-manager", Status: StatusReady, Message: "apt-get is available for the Ubuntu/Debian Docker bootstrap."})
		}
		if _, err := options.Probe.LookPath("sudo"); err != nil {
			add(CheckResult{Name: "sudo", Status: StatusMissing, Message: "sudo is not installed; Baron cannot perform the Linux Docker bootstrap.", Suggestion: "install sudo or ask an administrator to run baron tencent-memory init"})
		} else if _, err := options.Probe.Run(ctx, "sudo", "-n", "true"); err != nil {
			add(CheckResult{Name: "sudo", Status: StatusIncomplete, Message: "sudo authorization is not cached for this terminal.", Suggestion: "run sudo -v in this terminal, then rerun baron tencent-memory init"})
		} else {
			add(CheckResult{Name: "sudo", Status: StatusReady, Message: "sudo authorization is available for system bootstrap."})
		}
	}

	for _, command := range []string{"node", "npm", "npx", "pnpm", "uv", "uvx"} {
		if _, err := options.Probe.LookPath(command); err != nil {
			add(CheckResult{Name: command, Status: StatusMissing, Message: command + " is not installed.", Suggestion: "install " + command + " before the related Baron initializer"})
		} else if command == "node" {
			version, versionErr := options.Probe.Run(ctx, "node", "--version")
			if versionErr != nil {
				add(CheckResult{Name: command, Status: StatusUnavailable, Message: "Node is installed but did not report a version.", Suggestion: "install Node 22.19+ or 24+, then rerun baron test"})
			} else if !supportedNodeVersion(version) {
				add(CheckResult{Name: command, Status: StatusIncomplete, Message: "Node version " + bounded(strings.TrimSpace(version), 40) + " is outside the pinned DSH support range.", Suggestion: "install Node 22.19+ or 24+, then rerun baron test"})
			} else {
				add(CheckResult{Name: command, Status: StatusReady, Message: "Node is available (" + bounded(strings.TrimSpace(version), 40) + ")."})
			}
		} else {
			add(CheckResult{Name: command, Status: StatusReady, Message: command + " is available."})
		}
	}

	if _, err := options.Probe.LookPath("dsh"); err != nil {
		add(CheckResult{Name: "dsh", Status: StatusMissing, Message: "DeepSeek Harness is not installed.", Suggestion: "baron deepseek-harness init"})
	} else {
		version, versionErr := options.Probe.Run(ctx, "dsh", "--version")
		if versionErr != nil {
			add(CheckResult{Name: "dsh", Status: StatusUnavailable, Message: "DeepSeek Harness is installed but did not report a version.", Suggestion: "baron deepseek-harness init"})
		} else {
			add(CheckResult{Name: "dsh", Status: StatusReady, Message: "DeepSeek Harness is available (" + bounded(strings.TrimSpace(version), 80) + ")."})
		}
	}
	componentNames := []string{"duckduckgo-search", "superpowers-dsh", "dsh-reverse-skill", "baron-dsh-adapter"}
	for _, name := range componentNames {
		if options.DSHComponents != nil && options.DSHComponents[name] {
			add(CheckResult{Name: name, Status: StatusReady, Message: name + " is registered."})
		} else {
			add(CheckResult{Name: name, Status: StatusIncomplete, Message: name + " is not verified.", Suggestion: "baron deepseek-harness init"})
		}
	}
	if options.DSHProviderChecked {
		if options.DSHProviderReady {
			add(CheckResult{Name: "dsh-credentials", Status: StatusReady, Message: "DSH provider credential is configured through the supported credential path."})
		} else {
			add(CheckResult{Name: "dsh-credentials", Status: StatusIncomplete, Message: "DSH provider credential is not configured.", Suggestion: "baron deepseek-harness init (or set DEEPSEEK_API_KEY)"})
		}
	}

	if _, err := options.Probe.LookPath("codex"); err != nil {
		add(CheckResult{Name: "codex", Status: StatusMissing, Message: "Codex CLI is not installed.", Suggestion: "baron codex-cli init"})
	} else {
		version, versionErr := options.Probe.Run(ctx, "codex", "--version")
		if versionErr != nil {
			add(CheckResult{Name: "codex", Status: StatusUnavailable, Message: "Codex CLI did not report a supported version.", Suggestion: "baron codex-cli init"})
		} else {
			add(CheckResult{Name: "codex", Status: StatusReady, Message: "Codex CLI is available (" + bounded(strings.TrimSpace(version), 80) + ")."})
		}
		if options.CodexAuthenticated {
			add(CheckResult{Name: "codex-auth", Status: StatusReady, Message: "Codex authentication readiness is confirmed."})
		} else {
			add(CheckResult{Name: "codex-auth", Status: StatusIncomplete, Message: "Codex authentication is incomplete.", Suggestion: "run codex and complete ChatGPT sign-in"})
		}
		if options.CodexHooksPath != "" {
			inspection, inspectErr := install.InspectCodexHooks(options.CodexHooksPath, "baron")
			switch {
			case inspectErr != nil:
				add(CheckResult{Name: "codex-hooks", Status: StatusWarning, Message: "Codex hook configuration could not be inspected; no hook trust decision is inferred."})
			case inspection.State == install.CodexHooksMissing:
				add(CheckResult{Name: "codex-hooks", Status: StatusIncomplete, Message: "Baron Codex hooks are not installed.", Suggestion: "baron codex-cli init"})
			case inspection.State == install.CodexHooksIncomplete:
				add(CheckResult{Name: "codex-hooks", Status: StatusIncomplete, Message: "Baron Codex hook configuration is incomplete.", Suggestion: "baron codex-cli init"})
			case options.CodexProjectTrusted:
				add(CheckResult{Name: "codex-hooks", Status: StatusReady, Message: "Baron Codex hooks are configured and this project is trusted by Codex."})
			default:
				add(CheckResult{Name: "codex-hooks", Status: StatusWarning, Message: "Baron Codex hooks are configured, but interactive project trust/enablement cannot be proved from hooks.json alone.", Suggestion: install.CodexHookApprovalInstruction})
			}
		}
	}

	if options.TencentReady {
		add(CheckResult{Name: "tencent-memory", Status: StatusReady, Message: "Tencent Agent Memory identity and services are ready."})
	} else if options.TencentEndpoint != "" {
		if healthCheck(ctx, options.HTTPClient, options.TencentEndpoint) {
			add(CheckResult{Name: "tencent-memory", Status: StatusReady, Message: "Tencent MemoryCore is healthy."})
		} else {
			add(CheckResult{Name: "tencent-memory", Status: StatusUnavailable, Message: "Tencent MemoryCore is unavailable.", Suggestion: "baron tencent-memory init"})
		}
	} else {
		add(CheckResult{Name: "tencent-memory", Status: StatusIncomplete, Message: "Tencent Agent Memory is not initialized.", Suggestion: "baron tencent-memory init"})
	}
	if options.HubEndpoint != "" {
		if healthCheck(ctx, options.HTTPClient, options.HubEndpoint) {
			add(CheckResult{Name: "tencent-hub", Status: StatusReady, Message: "Tencent MemoryHub is healthy."})
		} else {
			add(CheckResult{Name: "tencent-hub", Status: StatusUnavailable, Message: "Tencent MemoryHub is unavailable.", Suggestion: "baron tencent-memory init"})
		}
	}
	if options.KnowledgeEndpoint != "" {
		if healthCheck(ctx, options.HTTPClient, options.KnowledgeEndpoint) {
			add(CheckResult{Name: "tencent-knowledge", Status: StatusReady, Message: "Tencent Knowledge Service is healthy."})
		} else {
			add(CheckResult{Name: "tencent-knowledge", Status: StatusUnavailable, Message: "Tencent Knowledge Service is unavailable.", Suggestion: "baron tencent-memory init"})
		}
	}
	if options.ProxyEndpoint != "" {
		if healthCheck(ctx, options.HTTPClient, options.ProxyEndpoint) {
			add(CheckResult{Name: "tencent-proxy", Status: StatusReady, Message: "Tencent proxy is healthy."})
		} else {
			add(CheckResult{Name: "tencent-proxy", Status: StatusUnavailable, Message: "Tencent proxy is unavailable.", Suggestion: "baron tencent-memory init"})
		}
	}
	for _, check := range options.SurfaceChecks {
		add(check)
	}
	for _, path := range options.CredentialPaths {
		private, permissionErr := config.IsPrivateFile(path)
		if errors.Is(permissionErr, os.ErrNotExist) {
			continue
		}
		if permissionErr != nil {
			add(CheckResult{Name: "permissions:" + path, Status: StatusWarning, Message: "Could not verify credential file permissions."})
			continue
		}
		if !private {
			add(CheckResult{Name: "permissions:" + path, Status: StatusWarning, Message: "Credential file is readable by group or other users.", Suggestion: "chmod 600 " + path})
		}
	}

	report.Ready = true
	for _, check := range report.Checks {
		if check.Status != StatusReady {
			report.Ready = false
			if report.ExitCode == 0 {
				switch check.Status {
				case StatusUnavailable:
					report.ExitCode = 12
				case StatusIncomplete:
					report.ExitCode = 11
				default:
					report.ExitCode = 10
				}
			}
		}
	}
	return report
}

func healthCheck(ctx context.Context, client *http.Client, endpoint string) bool {
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/health", nil)
	if err != nil {
		return false
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 300
}

func stringOutput(command interface{ Output() ([]byte, error) }) (string, error) {
	output, err := command.Output()
	return string(output), err
}

func bounded(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(value) <= max {
		return value
	}
	const suffix = "..."
	if max <= len(suffix) {
		return value[:max]
	}
	return value[:max-len(suffix)] + suffix
}

var nodeVersionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)(?:\.(\d+))`)

func supportedNodeVersion(value string) bool {
	match := nodeVersionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) < 3 {
		return false
	}
	major, majorErr := strconv.Atoi(match[1])
	minor, minorErr := strconv.Atoi(match[2])
	if majorErr != nil || minorErr != nil {
		return false
	}
	return (major == 22 && minor >= 19) || major >= 24
}

func SortedChecks(report Report) Report {
	sort.SliceStable(report.Checks, func(i, j int) bool { return report.Checks[i].Name < report.Checks[j].Name })
	return report
}
