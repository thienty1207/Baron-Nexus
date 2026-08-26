package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
)

const (
	nodeSourceRepository = "https://deb.nodesource.com/node_22.x"
	nodeSourceKeyURL     = "https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key"
	uvReleaseBaseURL     = "https://github.com/astral-sh/uv/releases/latest/download"
)

// HostToolchainOptions controls the Ubuntu/Debian dependency bootstrap. The
// downloader is injectable so tests can verify checksum and ordering without
// contacting an external release service.
type HostToolchainOptions struct {
	GOOS          string
	GOARCH        string
	OSReleasePath string
	Home          string
	Downloader    FileDownloader
}

// HostToolchainReport describes the host prerequisites Baron verified or
// installed. It intentionally contains no command output or credential.
type HostToolchainReport struct {
	Ready        bool
	Distribution string
	Version      string
	Architecture string
	Installed    []string
	Message      string
}

// EnsureHostToolchain installs or verifies Node/npm/npx, pnpm, and uv/uvx on
// Ubuntu/Debian. Sudo is authenticated before the first package or release
// download. Windows remains on the documented manual Docker/WSL boundary.
func EnsureHostToolchain(ctx context.Context, runner CommandRunner, options HostToolchainOptions) (HostToolchainReport, error) {
	if runner == nil {
		return HostToolchainReport{}, errors.New("host bootstrap runner is not configured")
	}
	if options.GOOS == "" {
		options.GOOS = runtime.GOOS
	}
	if options.GOARCH == "" {
		options.GOARCH = runtime.GOARCH
	}
	if options.GOOS != "linux" {
		return HostToolchainReport{}, errors.New("automatic host dependency installation is supported only on Ubuntu/Debian Linux; on Windows install Docker Desktop, WSL2, Ubuntu, Node/npm, pnpm, and uv manually")
	}
	if !supportedDockerArchitecture(options.GOARCH) {
		return HostToolchainReport{}, fmt.Errorf("unsupported Linux architecture %q for automatic host dependency installation", options.GOARCH)
	}
	if options.OSReleasePath == "" {
		options.OSReleasePath = "/etc/os-release"
	}
	distro, err := readLinuxDistribution(options.OSReleasePath)
	if err != nil {
		return HostToolchainReport{}, fmt.Errorf("read Linux distribution identity: %w", err)
	}
	if distro.ID != "ubuntu" && distro.ID != "debian" {
		return HostToolchainReport{}, fmt.Errorf("automatic host dependency installation supports Ubuntu/Debian only; detected %q", distro.ID)
	}
	if err := preflightSudo(ctx, runner); err != nil {
		return HostToolchainReport{}, err
	}
	if options.Downloader == nil {
		options.Downloader = httpFileDownloader{}
	}
	home := strings.TrimSpace(options.Home)
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return HostToolchainReport{}, errors.New("resolve the user home directory for uv installation")
		}
	}
	uvBinDir := filepath.Join(home, ".local", "bin")
	// A fresh installer commonly places uv in ~/.local/bin. Make it visible to
	// the current Baron process and its child DSH commands immediately.
	if path := os.Getenv("PATH"); !strings.Contains(string(os.PathListSeparator)+path+string(os.PathListSeparator), string(os.PathListSeparator)+uvBinDir+string(os.PathListSeparator)) {
		_ = os.Setenv("PATH", uvBinDir+string(os.PathListSeparator)+path)
	}

	report := HostToolchainReport{
		Distribution: distro.ID,
		Version:      firstNonEmptyLinux(distro.VersionID, distro.VersionCodename),
		Architecture: options.GOARCH,
		Installed:    []string{},
	}
	if installed, err := ensureNodeToolchain(ctx, runner, options, distro); err != nil {
		return report, err
	} else if installed {
		report.Installed = append(report.Installed, "node/npm/npx")
	}
	if installed, err := ensurePnpm(ctx, runner); err != nil {
		return report, err
	} else if installed {
		report.Installed = append(report.Installed, "pnpm")
	}
	if installed, err := ensureUV(ctx, runner, options.Downloader, options.GOARCH, uvBinDir); err != nil {
		return report, err
	} else if installed {
		report.Installed = append(report.Installed, "uv/uvx")
	}
	report.Ready = true
	report.Message = "Node/npm/npx, pnpm, and uv/uvx are ready."
	return report, nil
}

// runSudo executes a non-interactive sudo command. If its first attempt fails,
// the native runner is allowed one fresh sudo -v prompt and one retry. The
// password is read by sudo itself and never crosses this boundary.
func runSudo(ctx context.Context, runner CommandRunner, args ...string) (string, error) {
	commandArgs := append([]string{"-n"}, args...)
	output, err := runner.Run(ctx, "sudo", commandArgs...)
	if err == nil {
		return output, nil
	}
	interactive, ok := runner.(InteractiveCommandRunner)
	if !ok {
		return output, err
	}
	if _, authErr := interactive.RunInteractive(ctx, "sudo", "-v"); authErr != nil {
		return "", errors.New("sudo authorization failed; rerun sudo -v in this terminal, then retry Baron")
	}
	output, err = runner.Run(ctx, "sudo", commandArgs...)
	if err != nil {
		return "", errors.New("sudo command failed after reauthentication; verify the requested operation and retry Baron")
	}
	return output, nil
}

func ensureNodeToolchain(ctx context.Context, runner CommandRunner, options HostToolchainOptions, distro linuxDistribution) (bool, error) {
	nodeReady := false
	if _, err := runner.LookPath("node"); err == nil {
		if output, versionErr := runner.Run(ctx, "node", "--version"); versionErr == nil && supportedHostNodeVersion(output) {
			nodeReady = true
		}
	}
	npmReady := commandAvailable(runner, "npm")
	npxReady := commandAvailable(runner, "npx")
	if nodeReady && npmReady && npxReady {
		return false, nil
	}
	if _, err := runSudo(ctx, runner, "apt-get", "update"); err != nil {
		return false, errors.New("Node bootstrap could not refresh apt metadata; retry after sudo authorization")
	}
	if _, err := runSudo(ctx, runner, "apt-get", "install", "-y", "ca-certificates", "gnupg"); err != nil {
		return false, errors.New("Node bootstrap could not install its package prerequisites")
	}
	tempRoot, err := os.MkdirTemp("", "baron-node-bootstrap-")
	if err != nil {
		return false, fmt.Errorf("create user-owned Node bootstrap workspace: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	keyPath := filepath.Join(tempRoot, "nodesource.asc")
	keyringPath := filepath.Join(tempRoot, "nodesource.gpg")
	sourcePath := filepath.Join(tempRoot, "nodesource.sources")
	if err := options.Downloader.Download(ctx, nodeSourceKeyURL, keyPath); err != nil {
		return false, errors.New("download the official NodeSource signing key after sudo preflight")
	}
	if _, err := runSudo(ctx, runner, "gpg", "--batch", "--yes", "--dearmor", "--output", keyringPath, keyPath); err != nil {
		return false, errors.New("verify the official NodeSource signing key")
	}
	sources := "Types: deb\nURIs: " + nodeSourceRepository + "\nSuites: nodistro\nComponents: main\nArchitectures: " + nodeRepositoryArchitecture(options.GOARCH) + "\nSigned-By: /etc/apt/keyrings/nodesource.gpg\n\n"
	if err := os.WriteFile(sourcePath, []byte(sources), 0o600); err != nil {
		return false, fmt.Errorf("write NodeSource repository definition: %w", err)
	}
	if _, err := runSudo(ctx, runner, "install", "-m", "0755", "-d", "/etc/apt/keyrings"); err != nil {
		return false, errors.New("create the NodeSource apt keyring directory")
	}
	if _, err := runSudo(ctx, runner, "install", "-m", "0644", keyringPath, "/etc/apt/keyrings/nodesource.gpg"); err != nil {
		return false, errors.New("install the official NodeSource signing key")
	}
	if _, err := runSudo(ctx, runner, "install", "-m", "0644", sourcePath, "/etc/apt/sources.list.d/nodesource.sources"); err != nil {
		return false, errors.New("install the official NodeSource apt repository definition")
	}
	if _, err := runSudo(ctx, runner, "apt-get", "update"); err != nil {
		return false, errors.New("Node bootstrap could not refresh the NodeSource apt repository")
	}
	if _, err := runSudo(ctx, runner, "apt-get", "install", "-y", "nodejs"); err != nil {
		return false, fmt.Errorf("install Node 22 on %s: %w", distro.ID, err)
	}
	if output, versionErr := runner.Run(ctx, "node", "--version"); versionErr != nil || !supportedHostNodeVersion(output) {
		return false, errors.New("Node was installed but is below the supported 22.19+ or 24+ version")
	}
	if !commandAvailable(runner, "npm") || !commandAvailable(runner, "npx") {
		if _, err := runSudo(ctx, runner, "apt-get", "install", "-y", "npm"); err != nil {
			return false, errors.New("Node was installed but npm/npx could not be installed")
		}
	}
	if !commandAvailable(runner, "npm") || !commandAvailable(runner, "npx") {
		return false, errors.New("Node bootstrap completed without usable npm and npx")
	}
	return true, nil
}

func ensurePnpm(ctx context.Context, runner CommandRunner) (bool, error) {
	if commandAvailable(runner, "pnpm") {
		return false, nil
	}
	if !commandAvailable(runner, "npm") {
		return false, errors.New("pnpm bootstrap requires npm")
	}
	if _, err := runSudo(ctx, runner, "npm", "install", "--global", "pnpm@latest"); err != nil {
		return false, errors.New("install the latest pnpm through npm")
	}
	if !commandAvailable(runner, "pnpm") {
		return false, errors.New("pnpm was installed but is not on PATH")
	}
	return true, nil
}

func ensureUV(ctx context.Context, runner CommandRunner, downloader FileDownloader, goarch, binDir string) (bool, error) {
	if commandAvailable(runner, "uv") && commandAvailable(runner, "uvx") {
		return false, nil
	}
	asset, ok := uvLinuxAsset(goarch)
	if !ok {
		return false, fmt.Errorf("uv bootstrap does not support Linux architecture %q", goarch)
	}
	tempRoot, err := os.MkdirTemp("", "baron-uv-bootstrap-")
	if err != nil {
		return false, fmt.Errorf("create user-owned uv bootstrap workspace: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	archivePath := filepath.Join(tempRoot, asset)
	checksumPath := archivePath + ".sha256"
	if err := downloader.Download(ctx, uvReleaseBaseURL+"/"+asset, archivePath); err != nil {
		return false, errors.New("download the latest uv release after sudo preflight")
	}
	if err := downloader.Download(ctx, uvReleaseBaseURL+"/"+asset+".sha256", checksumPath); err != nil {
		return false, errors.New("download the uv release checksum")
	}
	checksumData, err := os.ReadFile(checksumPath)
	if err != nil {
		return false, errors.New("read the uv release checksum")
	}
	expected, err := firstChecksum(string(checksumData))
	if err != nil {
		return false, errors.New("uv release checksum is malformed")
	}
	actual, err := fileSHA256(archivePath)
	if err != nil || actual != expected {
		return false, errors.New("refusing to install uv: release checksum verification failed")
	}
	binaries, err := readUVArchive(archivePath)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return false, fmt.Errorf("create uv install directory: %w", err)
	}
	for _, name := range []string{"uv", "uvx"} {
		data, exists := binaries[name]
		if !exists {
			return false, fmt.Errorf("uv release archive did not contain %s", name)
		}
		if err := config.AtomicWriteFile(filepath.Join(binDir, name), data, 0o755); err != nil {
			return false, fmt.Errorf("install %s: %w", name, err)
		}
	}
	if !commandAvailable(runner, "uv") || !commandAvailable(runner, "uvx") {
		return false, errors.New("uv/uvx were installed but are not on PATH; export PATH=\"$HOME/.local/bin:$PATH\" and retry")
	}
	return true, nil
}

func commandAvailable(runner CommandRunner, name string) bool {
	_, err := runner.LookPath(name)
	return err == nil
}

var hostNodeVersionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)`)

func supportedHostNodeVersion(value string) bool {
	match := hostNodeVersionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 3 {
		return false
	}
	major, majorErr := strconv.Atoi(match[1])
	minor, minorErr := strconv.Atoi(match[2])
	if majorErr != nil || minorErr != nil {
		return false
	}
	return (major == 22 && minor >= 19) || major >= 24
}

func nodeRepositoryArchitecture(goarch string) string {
	switch goarch {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	case "arm":
		return "armhf"
	case "ppc64le":
		return "ppc64el"
	default:
		return goarch
	}
}

func uvLinuxAsset(goarch string) (string, bool) {
	architecture := map[string]string{
		"amd64":   "x86_64",
		"arm64":   "aarch64",
		"ppc64le": "powerpc64le",
		"s390x":   "s390x",
		"386":     "i686",
	}[goarch]
	if architecture == "" {
		return "", false
	}
	return "uv-" + architecture + "-unknown-linux-gnu.tar.gz", true
}

func firstChecksum(content string) (string, error) {
	for _, field := range strings.Fields(content) {
		if len(field) == sha256.Size*2 {
			decoded, err := hex.DecodeString(field)
			if err == nil && len(decoded) == sha256.Size {
				return strings.ToLower(field), nil
			}
		}
	}
	return "", errors.New("sha256 checksum not found")
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, 128*1024*1024)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readUVArchive(path string) (map[string][]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open uv release archive: %w", err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(io.LimitReader(file, 128*1024*1024))
	if err != nil {
		return nil, errors.New("decode uv release archive")
	}
	defer gzipReader.Close()
	archive := tar.NewReader(gzipReader)
	binaries := map[string][]byte{}
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("read uv release archive")
		}
		if header.Typeflag != tar.TypeReg || (header.Name != "uv" && !strings.HasSuffix(header.Name, "/uv") && header.Name != "uvx" && !strings.HasSuffix(header.Name, "/uvx")) {
			continue
		}
		name := filepath.Base(header.Name)
		if header.Size <= 0 || header.Size > 64*1024*1024 {
			return nil, errors.New("uv release archive contains an invalid binary size")
		}
		data, err := io.ReadAll(io.LimitReader(archive, header.Size+1))
		if err != nil || int64(len(data)) != header.Size {
			return nil, errors.New("read uv release binary")
		}
		binaries[name] = data
	}
	return binaries, nil
}
