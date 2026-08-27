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
// implementation downloads only the official Docker repository key into a
// user-owned temporary file; it never executes downloaded content.
type FileDownloader interface {
	Download(context.Context, string, string) error
}

type DockerBootstrapOptions struct {
	GOOS          string
	GOARCH        string
	OSReleasePath string
	Downloader    FileDownloader
	AptKeyPath    string
	AptSourcePath string
	// Refresh resolves the official stable repository and asks apt to install
	// the latest available Docker Engine/Compose packages even when the
	// daemon is already healthy. The normal health/readiness path leaves this
	// false so diagnostics never mutate a user's host.
	Refresh bool
}

type DockerBootstrapReport struct {
	Ready         bool
	Distribution  string
	Version       string
	Architecture  string
	DockerPath    string
	UsedSudo      bool
	Installed     bool
	DaemonStarted bool
	NeedsRelogin  bool
	Message       string
}

type linuxDistribution struct {
	ID              string
	VersionID       string
	VersionCodename string
	UbuntuCodename  string
}

// EnsureDocker validates the host, performs the sudo preflight before any
// network operation, and installs/repairs Docker through the official apt
// repository on Ubuntu/Debian only. It deliberately does not add the current
// user to the root-equivalent docker group.
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
	if err := preflightSudo(ctx, runner); err != nil {
		return DockerBootstrapReport{}, err
	}

	report := DockerBootstrapReport{
		Distribution: distro.ID,
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
				return report, nil
			}
			if err := installDockerPackages(ctx, runner, options, distro, codename); err != nil {
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
				if err := installDockerPackages(ctx, runner, options, distro, codename); err != nil {
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
			return report, nil
		}
	}

	if err := installDockerPackages(ctx, runner, options, distro, codename); err != nil {
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
	return report, nil
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

func installDockerPackages(ctx context.Context, runner CommandRunner, options DockerBootstrapOptions, distro linuxDistribution, codename string) error {
	if _, err := runSudo(ctx, runner, "apt-get", "update"); err != nil {
		return errors.New("Docker bootstrap could not refresh apt metadata; run sudo apt-get update and retry")
	}
	if _, err := runSudo(ctx, runner, "apt-get", "install", "-y", "ca-certificates"); err != nil {
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
		options.Downloader = httpFileDownloader{}
	}
	platform := distro.ID
	repository := "https://download.docker.com/linux/" + platform
	if err := options.Downloader.Download(ctx, repository+"/gpg", keyPath); err != nil {
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
	if _, err := runSudo(ctx, runner, "install", "-m", "0755", "-d", "/etc/apt/keyrings"); err != nil {
		return errors.New("create Docker apt keyring directory")
	}
	if !keyExisted {
		if _, err := runSudo(ctx, runner, "install", "-m", "0644", keyPath, aptKeyPath); err != nil {
			return errors.New("install the official Docker apt signing key")
		}
	}
	if !sourceExisted {
		if _, err := runSudo(ctx, runner, "install", "-m", "0644", sourcePath, aptSourcePath); err != nil {
			cleanup()
			return errors.New("install the official Docker apt repository definition")
		}
	}
	if _, err := runSudo(ctx, runner, "apt-get", "update"); err != nil {
		cleanup()
		return errors.New("Docker bootstrap could not refresh the official Docker apt repository")
	}
	packages := []string{"docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"}
	args := append([]string{"apt-get", "install", "-y"}, packages...)
	if _, err := runSudo(ctx, runner, args...); err != nil {
		cleanup()
		return errors.New("Docker Engine package installation failed; run sudo apt-get install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin and retry")
	}
	if _, err := runSudo(ctx, runner, "systemctl", "enable", "--now", "docker"); err != nil {
		cleanup()
		return errors.New("Docker packages installed but the service could not start; run sudo systemctl enable --now docker and retry")
	}
	return nil
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

type httpFileDownloader struct{}

func (httpFileDownloader) Download(ctx context.Context, rawURL, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Docker key download returned HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, io.LimitReader(response.Body, 2*1024*1024))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
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
