package install

import (
	"context"
	"crypto/tls"
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

	"github.com/baron-shared-brain/baron/internal/managedruntime"
)

// InteractiveCommandRunner is implemented by the native runner so sudo can
// prompt in the user's terminal without Baron ever handling the password.
// Fixture runners intentionally do not need to implement it; they use the
// non-interactive sudo check instead.
type InteractiveCommandRunner interface {
	CommandRunner
	RunInteractive(context.Context, string, ...string) (string, error)
}

// FileDownloader keeps the package/bootstrap policy testable. The default
// implementation downloads official bootstrap inputs into a user-owned
// temporary file; it never executes downloaded content.
type FileDownloader interface {
	Download(context.Context, string, string) error
}

type DockerBootstrapOptions struct {
	GOOS          string
	GOARCH        string
	OSReleasePath string
	HTTPClient    *http.Client
	Apt           *AptSession
	Downloader    FileDownloader
	Progress      ProgressReporter
	AptKeyPath    string
	AptSourcePath string
	// Refresh checks the official stable repository candidates and updates stale
	// Docker Engine/Compose packages. A healthy daemon with matching candidates
	// remains untouched; the normal health/readiness path leaves this false so
	// diagnostics never mutate a user's host.
	Refresh bool
}

type DockerBootstrapReport struct {
	Ready          bool
	Distribution   string
	Backend        string
	WSLDistro      string
	BridgeVerified bool
	Version        string
	Architecture   string
	DockerPath     string
	UsedSudo       bool
	Installed      bool
	DaemonStarted  bool
	NeedsRelogin   bool
	Message        string
}

var dockerPackageNames = []string{"docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"}

type linuxDistribution struct {
	ID              string
	VersionID       string
	VersionCodename string
	UbuntuCodename  string
}

// EnsureDocker validates the host, performs the sudo preflight before any
// network operation, and installs/repairs Docker through the official apt
// repository on Ubuntu/Debian. On Windows it verifies either an already
// managed Docker Desktop daemon or a verified Ubuntu WSL2 Docker engine;
// Baron does not mutate Windows UI components.
func EnsureDocker(ctx context.Context, runner CommandRunner, options DockerBootstrapOptions) (DockerBootstrapReport, error) {
	if runner == nil {
		return DockerBootstrapReport{}, errors.New("Docker bootstrap runner is not configured")
	}
	if options.GOOS == "" {
		options.GOOS = runtime.GOOS
	}
	if options.GOARCH == "" {
		options.GOARCH = runtime.GOARCH
	}
	options.GOOS = strings.ToLower(strings.TrimSpace(options.GOOS))
	if options.GOOS == "windows" {
		return verifyWindowsDocker(ctx, runner, options)
	}
	if options.GOOS != "linux" {
		return DockerBootstrapReport{}, errors.New("automatic Docker installation is supported only on Ubuntu/Debian Linux; on Windows install Docker Desktop, WSL2, and Ubuntu manually, then rerun baron tencent-memory init")
	}
	if !supportedDockerArchitecture(options.GOARCH) {
		return DockerBootstrapReport{}, fmt.Errorf("unsupported Linux architecture %q for automatic Docker installation; install Docker manually", options.GOARCH)
	}
	if options.OSReleasePath == "" {
		options.OSReleasePath = "/etc/os-release"
	}
	distro, err := readLinuxDistribution(options.OSReleasePath)
	if err != nil {
		return DockerBootstrapReport{}, fmt.Errorf("read Linux distribution identity: %w", err)
	}
	if distro.ID != "ubuntu" && distro.ID != "debian" {
		return DockerBootstrapReport{}, fmt.Errorf("automatic Docker installation supports Ubuntu/Debian only; detected %q", distro.ID)
	}
	codename := distro.VersionCodename
	if distro.ID == "ubuntu" && distro.UbuntuCodename != "" {
		codename = distro.UbuntuCodename
	}
	if strings.TrimSpace(codename) == "" {
		return DockerBootstrapReport{}, fmt.Errorf("could not determine %s release codename; install Docker manually and rerun", distro.ID)
	}
	reportStep(options.Progress, "Requesting sudo authorization for Docker bootstrap...")
	if err := preflightSudo(ctx, runner); err != nil {
		return DockerBootstrapReport{}, err
	}
	reportStep(options.Progress, "sudo authorization accepted")

	report := DockerBootstrapReport{
		Distribution: distro.ID,
		Backend:      "linux",
		Version:      firstNonEmptyLinux(distro.VersionID, codename),
		Architecture: options.GOARCH,
	}
	dockerPath, dockerErr := runner.LookPath("docker")
	if dockerErr == nil {
		report.DockerPath = dockerPath
		if _, err := runner.Run(ctx, "docker", "info"); err == nil {
			if !options.Refresh {
				report.Ready = true
				report.Message = "Docker Engine and daemon are ready."
				reportStep(options.Progress, "Docker Engine is already ready.")
				return report, nil
			}
			if err := refreshAptMetadata(ctx, runner, options.Progress, options.Apt, "Docker"); err != nil {
				return report, errors.New("Docker bootstrap could not refresh apt metadata; run sudo apt-get update and retry")
			}
			known, current, stale := dockerPackagesCurrent(ctx, runner)
			if !known {
				return report, errors.New("latest Docker package state is unknown; verify apt metadata and retry")
			}
			if current {
				report.Ready = true
				report.Message = "Docker Engine and daemon are already latest."
				reportStep(options.Progress, "Docker Engine and packages are already latest.")
				return report, nil
			}
			if err := installDockerPackages(ctx, runner, options, distro, codename, stale); err != nil {
				return report, err
			}
			report.Ready = true
			report.Installed = true
			report.UsedSudo = true
			report.DaemonStarted = true
			report.Message = "Docker Engine and daemon were refreshed from the official stable repository."
			return report, nil
		}
		if _, err := runSudo(ctx, runner, "systemctl", "enable", "--now", "docker"); err == nil {
			report.UsedSudo = true
			report.DaemonStarted = true
		}
		if _, err := runSudo(ctx, runner, "docker", "info"); err == nil {
			report.UsedSudo = true
			if options.Refresh {
				if err := refreshAptMetadata(ctx, runner, options.Progress, options.Apt, "Docker"); err != nil {
					return report, errors.New("Docker bootstrap could not refresh apt metadata; run sudo apt-get update and retry")
				}
				known, current, stale := dockerPackagesCurrent(ctx, runner)
				if !known {
					return report, errors.New("latest Docker package state is unknown; verify apt metadata and retry")
				}
				if current {
					report.Ready = true
					report.NeedsRelogin = true
					report.Message = dockerPermissionMessage(report.DaemonStarted)
					reportStep(options.Progress, "Docker Engine and packages are already latest.")
					return report, nil
				}
				if err := installDockerPackages(ctx, runner, options, distro, codename, stale); err != nil {
					return report, err
				}
				report.Installed = true
				report.DaemonStarted = true
				report.Ready = true
				report.NeedsRelogin = true
				report.Message = "Docker Engine and daemon were refreshed from the official stable repository."
				return report, nil
			}
			report.Ready = true
			report.NeedsRelogin = true
			report.Message = dockerPermissionMessage(report.DaemonStarted)
			reportStep(options.Progress, "Docker Engine is ready through sudo.")
			return report, nil
		}
	}

	if err := installDockerPackages(ctx, runner, options, distro, codename, nil); err != nil {
		return report, err
	}
	report.Installed = true
	report.UsedSudo = true
	report.DaemonStarted = true
	if dockerPath, err := runner.LookPath("docker"); err == nil {
		report.DockerPath = dockerPath
	}
	if _, err := runSudo(ctx, runner, "docker", "info"); err != nil {
		return report, errors.New("Docker was installed but the daemon is not reachable; run sudo systemctl status docker and rerun baron tencent-memory init")
	}
	report.Ready = true
	report.NeedsRelogin = true
	report.Message = dockerPermissionMessage(true)
	reportStep(options.Progress, "Docker Engine is ready through sudo.")
	return report, nil
}

func verifyWindowsDocker(ctx context.Context, runner CommandRunner, options DockerBootstrapOptions) (DockerBootstrapReport, error) {
	report := DockerBootstrapReport{Distribution: "windows", Backend: "unavailable", Architecture: options.GOARCH}
	dockerPath, dockerErr := runner.LookPath("docker")
	if dockerErr == nil {
		report.DockerPath = dockerPath
		reportStep(options.Progress, "Verifying Docker Desktop...")
		if _, runErr := runner.Run(ctx, "docker", "info"); runErr == nil {
			report.Ready = true
			report.Backend = "docker"
			report.Message = "Docker Desktop daemon is ready."
			reportStep(options.Progress, "Docker Desktop is ready.")
			return report, nil
		}
	}

	// Docker Desktop is optional when Docker Engine is exposed by the Ubuntu
	// WSL2 distro. Inspecting the distro and its own daemon is required; host
	// Docker availability alone is not sufficient evidence for this bridge.
	if _, wslErr := runner.LookPath("wsl"); wslErr == nil {
		reportStep(options.Progress, "Verifying Docker Engine through Ubuntu WSL2...")
		platform, platformErr := managedruntime.DetectPlatformFor(ctx, platformCommandRunner{runner: runner}, "windows", options.GOARCH)
		if platformErr == nil && platform.BridgeVerified {
			report.Ready = true
			report.Backend = "wsl2"
			report.WSLDistro = platform.WSLDistro
			report.BridgeVerified = true
			report.Message = "Docker Engine is ready through the verified Ubuntu WSL2 bridge."
			reportStep(options.Progress, "Ubuntu WSL2 Docker bridge is ready.")
			return report, nil
		}
	}
	if dockerErr != nil {
		return report, errors.New("Docker Desktop or a verified Ubuntu WSL2 Docker engine is required on Windows; install and start one, then rerun Baron")
	}
	return report, errors.New("Docker Desktop is installed but its daemon is not reachable, and no verified Ubuntu WSL2 Docker engine was found; start Docker Desktop or the WSL2 backend, then rerun Baron")
}

type platformCommandRunner struct {
	runner CommandRunner
}

func (r platformCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := r.runner.Run(ctx, name, args...)
	return []byte(output), err
}

func preflightSudo(ctx context.Context, runner CommandRunner) error {
	if _, err := runner.LookPath("sudo"); err != nil {
		return errors.New("sudo is required before Baron can install Docker; run sudo -v or ask an administrator, then rerun baron tencent-memory init")
	}
	var err error
	if interactive, ok := runner.(InteractiveCommandRunner); ok {
		_, err = interactive.RunInteractive(ctx, "sudo", "-v")
	} else {
		_, err = runner.Run(ctx, "sudo", "-n", "true")
	}
	if err != nil {
		return errors.New("sudo authorization is required before any Docker/Tencent download; run sudo -v in this terminal, then rerun baron tencent-memory init")
	}
	if _, err := runner.Run(ctx, "sudo", "-n", "true"); err != nil {
		return errors.New("sudo authorization could not be verified; run sudo -v in this terminal, then rerun baron tencent-memory init")
	}
	return nil
}

func installDockerPackages(ctx context.Context, runner CommandRunner, options DockerBootstrapOptions, distro linuxDistribution, codename string, packages []string) error {
	if err := refreshAptMetadata(ctx, runner, options.Progress, options.Apt, "Docker"); err != nil {
		return errors.New("Docker bootstrap could not refresh apt metadata; run sudo apt-get update and retry")
	}
	if _, err := runSudoProgress(ctx, runner, options.Progress, "Installing Docker package prerequisites", "apt-get", "install", "-y", "ca-certificates"); err != nil {
		return errors.New("Docker bootstrap could not install ca-certificates; run sudo apt-get install ca-certificates and retry")
	}

	tempRoot, err := os.MkdirTemp("", "baron-docker-bootstrap-")
	if err != nil {
		return fmt.Errorf("create user-owned Docker bootstrap workspace: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	keyPath := filepath.Join(tempRoot, "docker.asc")
	sourcePath := filepath.Join(tempRoot, "docker.sources")
	aptKeyPath := firstNonEmptyLinux(options.AptKeyPath, "/etc/apt/keyrings/docker.asc")
	aptSourcePath := firstNonEmptyLinux(options.AptSourcePath, "/etc/apt/sources.list.d/docker.sources")
	keyExisted := pathExists(aptKeyPath)
	sourceExisted := pathExists(aptSourcePath)
	cleanup := func() {
		if !keyExisted {
			_, _ = runSudo(ctx, runner, "rm", "-f", aptKeyPath)
		}
		if !sourceExisted {
			_, _ = runSudo(ctx, runner, "rm", "-f", aptSourcePath)
		}
	}
	if options.Downloader == nil {
		options.Downloader = httpFileDownloader{Progress: options.Progress, Client: configuredHTTPClient(options.HTTPClient)}
	}
	platform := distro.ID
	repository := "https://download.docker.com/linux/" + platform
	if err := downloadFile(ctx, options.Downloader, options.Progress, "Docker signing key", repository+"/gpg", keyPath); err != nil {
		return errors.New("download the official Docker apt signing key after sudo preflight")
	}
	architecture := dockerRepositoryArchitecture(options.GOARCH)
	sources := strings.Join([]string{
		"Types: deb",
		"URIs: " + repository,
		"Suites: " + codename,
		"Components: stable",
		"Architectures: " + architecture,
		"Signed-By: /etc/apt/keyrings/docker.asc",
		"",
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(sources), 0o600); err != nil {
		return fmt.Errorf("write user-owned Docker apt source: %w", err)
	}
	if _, err := runSudoProgress(ctx, runner, options.Progress, "Preparing Docker apt keyring", "install", "-m", "0755", "-d", "/etc/apt/keyrings"); err != nil {
		return errors.New("create Docker apt keyring directory")
	}
	if !keyExisted {
		if _, err := runSudoProgress(ctx, runner, options.Progress, "Installing the Docker signing key", "install", "-m", "0644", keyPath, aptKeyPath); err != nil {
			return errors.New("install the official Docker apt signing key")
		}
	}
	if !sourceExisted {
		if _, err := runSudoProgress(ctx, runner, options.Progress, "Installing the Docker apt repository definition", "install", "-m", "0644", sourcePath, aptSourcePath); err != nil {
			cleanup()
			return errors.New("install the official Docker apt repository definition")
		}
	}
	if options.Apt != nil {
		options.Apt.invalidate()
	}
	if err := refreshAptMetadata(ctx, runner, options.Progress, options.Apt, "the Docker repository"); err != nil {
		cleanup()
		return errors.New("Docker bootstrap could not refresh the official Docker apt repository")
	}
	if len(packages) == 0 {
		packages = dockerPackageNames
	}
	args := append([]string{"apt-get", "install", "-y"}, packages...)
	if _, err := runSudoProgress(ctx, runner, options.Progress, "Installing Docker Engine and Compose", args...); err != nil {
		cleanup()
		return errors.New("Docker Engine package installation failed; run sudo apt-get install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin and retry")
	}
	if _, err := runSudoProgress(ctx, runner, options.Progress, "Starting Docker Engine", "systemctl", "enable", "--now", "docker"); err != nil {
		cleanup()
		return errors.New("Docker packages installed but the service could not start; run sudo systemctl enable --now docker and retry")
	}
	return nil
}

func dockerPackagesCurrent(ctx context.Context, runner CommandRunner) (known, current bool, stale []string) {
	if !commandAvailable(runner, "dpkg-query") || !commandAvailable(runner, "apt-cache") {
		return false, false, nil
	}
	stale = []string{}
	for _, packageName := range dockerPackageNames {
		installedOutput, err := runner.Run(ctx, "dpkg-query", "-W", "-f=${Version}", packageName)
		if err != nil {
			stale = append(stale, packageName)
			continue
		}
		installed := strings.TrimSpace(installedOutput)
		if installed == "" {
			stale = append(stale, packageName)
			continue
		}
		candidateOutput, err := runner.Run(ctx, "apt-cache", "policy", packageName)
		if err != nil {
			return false, false, nil
		}
		candidate := aptCandidateVersion(candidateOutput)
		if candidate == "" || !strings.Contains(strings.ToLower(candidateOutput), "download.docker.com") {
			stale = append(stale, packageName)
			continue
		}
		if installed != candidate {
			stale = append(stale, packageName)
		}
	}
	return true, len(stale) == 0, stale
}

func aptCandidateVersion(output string) string {
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Candidate") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func readLinuxDistribution(path string) (linuxDistribution, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return linuxDistribution{}, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return linuxDistribution{
		ID:              strings.ToLower(values["ID"]),
		VersionID:       values["VERSION_ID"],
		VersionCodename: values["VERSION_CODENAME"],
		UbuntuCodename:  values["UBUNTU_CODENAME"],
	}, nil
}

func supportedDockerArchitecture(value string) bool {
	switch value {
	case "amd64", "arm64", "arm", "ppc64le", "s390x":
		return true
	default:
		return false
	}
}

func dockerRepositoryArchitecture(value string) string {
	if value == "arm" {
		return "armhf"
	}
	if value == "ppc64le" {
		return "ppc64el"
	}
	return value
}

func dockerPermissionMessage(started bool) string {
	if started {
		return "Docker is ready through sudo. Baron does not add the user to the root-equivalent docker group automatically; run sudo usermod -aG docker $USER and log out/in only if you explicitly want unprivileged Docker CLI access."
	}
	return "Docker is ready through sudo."
}

func firstNonEmptyLinux(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "unknown"
}

const maxHTTPDownloadBytes = 128 * 1024 * 1024

func configuredHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		return &http.Client{
			Timeout:   30 * time.Second,
			Transport: secureHTTPTransport(),
		}
	}
	if base.Transport == nil {
		clone := *base
		if clone.Timeout == 0 {
			clone.Timeout = 30 * time.Second
		}
		clone.Transport = secureHTTPTransport()
		return &clone
	}
	transport, ok := base.Transport.(*http.Transport)
	if !ok {
		return base
	}
	clone := *base
	if clone.Timeout == 0 {
		clone.Timeout = 30 * time.Second
	}
	if base.Timeout != 0 && transport.TLSClientConfig != nil && transport.TLSClientConfig.MinVersion >= tls.VersionTLS12 {
		return base
	}
	secureTransport := transport.Clone()
	tlsConfig := &tls.Config{}
	if transport.TLSClientConfig != nil {
		tlsConfig = transport.TLSClientConfig.Clone()
	}
	tlsConfig.MinVersion = tls.VersionTLS12
	secureTransport.TLSClientConfig = tlsConfig
	clone.Transport = secureTransport
	return &clone
}

func secureHTTPTransport() *http.Transport {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, MaxIdleConns: 16, MaxIdleConnsPerHost: 8}
	}
	clone := transport.Clone()
	clone.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	clone.MaxIdleConns = 16
	clone.MaxIdleConnsPerHost = 8
	return clone
}

type httpFileDownloader struct {
	Progress ProgressReporter
	Client   *http.Client
}

type labeledFileDownloader interface {
	DownloadWithProgress(context.Context, string, string, string) error
}

func reportStep(reporter ProgressReporter, label string) {
	if reporter != nil {
		reporter.Step(label)
	}
}

func downloadFile(ctx context.Context, downloader FileDownloader, reporter ProgressReporter, label, rawURL, destination string) error {
	reportStep(reporter, "Downloading "+label+"...")
	var err error
	if labeled, ok := downloader.(labeledFileDownloader); ok {
		err = labeled.DownloadWithProgress(ctx, rawURL, destination, label)
	} else {
		err = downloader.Download(ctx, rawURL, destination)
	}
	if err != nil {
		reportStep(reporter, "Downloading "+label+" failed.")
		return err
	}
	reportStep(reporter, label+" downloaded.")
	return nil
}

func (d httpFileDownloader) Download(ctx context.Context, rawURL, destination string) error {
	return d.download(ctx, rawURL, destination, "download")
}

func (d httpFileDownloader) DownloadWithProgress(ctx context.Context, rawURL, destination, label string) error {
	return d.download(ctx, rawURL, destination, label)
}

func (d httpFileDownloader) download(ctx context.Context, rawURL, destination, label string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	client := d.Client
	if client == nil {
		client = configuredHTTPClient(nil)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Baron download returned HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	reader := NewProgressReader(response.Body, d.Progress, label, response.ContentLength)
	written, copyErr := io.Copy(file, io.LimitReader(reader, maxHTTPDownloadBytes+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if written > maxHTTPDownloadBytes {
		return fmt.Errorf("Baron download exceeds the %d-byte limit", maxHTTPDownloadBytes)
	}
	return closeErr
}

// NativeCommandRunner is the production runner used by the app. It is kept in
// this package so the bootstrap can be tested without coupling to Cobra.
type NativeCommandRunner struct{}

func (NativeCommandRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (NativeCommandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.Output()
	return string(output), err
}
