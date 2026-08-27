package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	nodeReleaseIndexURL  = "https://nodejs.org/dist/index.json"
	nodeSourceRepository = "https://deb.nodesource.com/node_"
	nodeSourceKeyURL     = "https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key"
	uvReleaseAPIURL      = "https://api.github.com/repos/astral-sh/uv/releases/latest"
	uvReleaseDownloadURL = "https://github.com/astral-sh/uv/releases/download"
	uvDownloadAttempts   = 2
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
	Progress      ProgressReporter
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

// EnsureHostToolchain resolves and refreshes Node/npm/npx, pnpm, and uv/uvx on
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
	reportStep(options.Progress, "Preparing Ubuntu/Debian host dependencies...")
	reportStep(options.Progress, "Requesting sudo authorization for host dependencies...")
	if err := preflightSudo(ctx, runner); err != nil {
		return HostToolchainReport{}, err
	}
	reportStep(options.Progress, "sudo authorization accepted")
	if options.Downloader == nil {
		options.Downloader = httpFileDownloader{Progress: options.Progress}
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
	reportStep(options.Progress, "Preparing Node.js/npm/npx...")
	if installed, err := ensureNodeToolchain(ctx, runner, options, distro); err != nil {
		return report, err
	} else if installed {
		report.Installed = append(report.Installed, "node/npm/npx")
		reportStep(options.Progress, "Node.js/npm/npx ready.")
	}
	if installed, err := ensurePnpm(ctx, runner, options.Progress); err != nil {
		return report, err
	} else if installed {
		report.Installed = append(report.Installed, "pnpm")
		reportStep(options.Progress, "pnpm ready.")
	}
	if installed, err := ensureUV(ctx, runner, options.Downloader, options.GOARCH, uvBinDir, options.Progress); err != nil {
		return report, err
	} else if installed {
		report.Installed = append(report.Installed, "uv/uvx")
		reportStep(options.Progress, "uv/uvx ready.")
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

// RunSudo exposes the existing password boundary to cleanup operations. The
// runner, not Baron, owns the sudo prompt and Baron never receives the secret.
func RunSudo(ctx context.Context, runner CommandRunner, args ...string) (string, error) {
	return runSudo(ctx, runner, args...)
}

func runSudoProgress(ctx context.Context, runner CommandRunner, reporter ProgressReporter, label string, args ...string) (string, error) {
	reportStep(reporter, label+"...")
	output, err := runSudo(ctx, runner, args...)
	if err != nil {
		reportStep(reporter, label+" failed.")
		return output, err
	}
	reportStep(reporter, label+" complete.")
	return output, nil
}

type nodeReleaseMetadata struct {
	Version string `json:"version"`
}

func resolveLatestNodeMajor(ctx context.Context, downloader FileDownloader, reporters ...ProgressReporter) (string, error) {
	reporter := firstProgressReporter(reporters...)
	if downloader == nil {
		return "", errors.New("Node release downloader is not configured")
	}
	tempRoot, err := os.MkdirTemp("", "baron-node-release-")
	if err != nil {
		return "", fmt.Errorf("create Node release metadata workspace: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	metadataPath := filepath.Join(tempRoot, "index.json")
	if err := downloadFile(ctx, downloader, reporter, "latest Node.js release metadata", nodeReleaseIndexURL, metadataPath); err != nil {
		return "", errors.New("resolve the latest Node release")
	}
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return "", errors.New("read the latest Node release metadata")
	}
	var releases []nodeReleaseMetadata
	if err := json.Unmarshal(data, &releases); err != nil {
		return "", errors.New("decode the latest Node release metadata")
	}
	for _, release := range releases {
		version := strings.TrimPrefix(strings.TrimSpace(release.Version), "v")
		parts := strings.Split(version, ".")
		if len(parts) != 3 || parts[0] == "" {
			continue
		}
		major, err := strconv.Atoi(parts[0])
		if err != nil || major < 24 {
			continue
		}
		return strconv.Itoa(major), nil
	}
	return "", errors.New("latest Node release metadata contains no supported release")
}

func ensureNodeToolchain(ctx context.Context, runner CommandRunner, options HostToolchainOptions, distro linuxDistribution) (bool, error) {
	latestMajor, err := resolveLatestNodeMajor(ctx, options.Downloader, options.Progress)
	if err != nil {
		return false, err
	}
	if _, err := runSudoProgress(ctx, runner, options.Progress, "Refreshing apt metadata for Node.js", "apt-get", "update"); err != nil {
		return false, errors.New("Node bootstrap could not refresh apt metadata; retry after sudo authorization")
	}
	if _, err := runSudoProgress(ctx, runner, options.Progress, "Installing Node.js bootstrap prerequisites", "apt-get", "install", "-y", "ca-certificates", "gnupg"); err != nil {
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
	if err := downloadFile(ctx, options.Downloader, options.Progress, "NodeSource signing key", nodeSourceKeyURL, keyPath); err != nil {
		return false, errors.New("download the official NodeSource signing key after sudo preflight")
	}
	if _, err := runSudoProgress(ctx, runner, options.Progress, "Verifying the NodeSource signing key", "gpg", "--batch", "--yes", "--dearmor", "--output", keyringPath, keyPath); err != nil {
		return false, errors.New("verify the official NodeSource signing key")
	}
	sources := "Types: deb\nURIs: " + nodeSourceRepository + latestMajor + ".x\nSuites: nodistro\nComponents: main\nArchitectures: " + nodeRepositoryArchitecture(options.GOARCH) + "\nSigned-By: /etc/apt/keyrings/nodesource.gpg\n\n"
	if err := os.WriteFile(sourcePath, []byte(sources), 0o600); err != nil {
		return false, fmt.Errorf("write NodeSource repository definition: %w", err)
	}
	if _, err := runSudoProgress(ctx, runner, options.Progress, "Preparing the NodeSource apt keyring", "install", "-m", "0755", "-d", "/etc/apt/keyrings"); err != nil {
		return false, errors.New("create the NodeSource apt keyring directory")
	}
	if _, err := runSudoProgress(ctx, runner, options.Progress, "Installing the NodeSource signing key", "install", "-m", "0644", keyringPath, "/etc/apt/keyrings/nodesource.gpg"); err != nil {
		return false, errors.New("install the official NodeSource signing key")
	}
	if _, err := runSudoProgress(ctx, runner, options.Progress, "Installing the NodeSource apt repository definition", "install", "-m", "0644", sourcePath, "/etc/apt/sources.list.d/nodesource.sources"); err != nil {
		return false, errors.New("install the official NodeSource apt repository definition")
	}
	if _, err := runSudoProgress(ctx, runner, options.Progress, "Refreshing the NodeSource apt repository", "apt-get", "update"); err != nil {
		return false, errors.New("Node bootstrap could not refresh the NodeSource apt repository")
	}
	if _, err := runSudoProgress(ctx, runner, options.Progress, "Installing Node.js", "apt-get", "install", "-y", "nodejs"); err != nil {
		return false, fmt.Errorf("install the latest supported Node major on %s: %w", distro.ID, err)
	}
	if output, versionErr := runner.Run(ctx, "node", "--version"); versionErr != nil || !supportedHostNodeVersion(output) {
		return false, fmt.Errorf("Node was installed but is below the supported 22.19+ or 24+ version (latest major %s)", latestMajor)
	}
	if !commandAvailable(runner, "npm") || !commandAvailable(runner, "npx") {
		if _, err := runSudoProgress(ctx, runner, options.Progress, "Installing npm and npx", "apt-get", "install", "-y", "npm"); err != nil {
			return false, errors.New("Node was installed but npm/npx could not be installed")
		}
	}
	if !commandAvailable(runner, "npm") || !commandAvailable(runner, "npx") {
		return false, errors.New("Node bootstrap completed without usable npm and npx")
	}
	return true, nil
}

func ensurePnpm(ctx context.Context, runner CommandRunner, reporters ...ProgressReporter) (bool, error) {
	reporter := firstProgressReporter(reporters...)
	if !commandAvailable(runner, "npm") {
		return false, errors.New("pnpm bootstrap requires npm")
	}
	if _, err := runSudoProgress(ctx, runner, reporter, "Installing pnpm through npm", "npm", "install", "--global", "pnpm@latest"); err != nil {
		return false, errors.New("install the latest pnpm through npm")
	}
	if !commandAvailable(runner, "pnpm") {
		return false, errors.New("pnpm was installed but is not on PATH")
	}
	return true, nil
}

func ensureUV(ctx context.Context, runner CommandRunner, downloader FileDownloader, goarch, binDir string, reporters ...ProgressReporter) (bool, error) {
	reporter := firstProgressReporter(reporters...)
	asset, ok := uvLinuxAsset(goarch)
	if !ok {
		return false, fmt.Errorf("uv bootstrap does not support Linux architecture %q", goarch)
	}
	tempRoot, err := os.MkdirTemp("", "baron-uv-bootstrap-")
	if err != nil {
		return false, fmt.Errorf("create user-owned uv bootstrap workspace: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	tag, err := resolveLatestUVTag(ctx, downloader, filepath.Join(tempRoot, "release.json"), reporter)
	if err != nil {
		return false, err
	}
	var archivePath string
	for attempt := 1; attempt <= uvDownloadAttempts; attempt++ {
		archivePath = filepath.Join(tempRoot, fmt.Sprintf("%s.%d", asset, attempt))
		checksumPath := archivePath + ".sha256"
		baseURL := uvReleaseDownloadURL + "/" + tag
		if err := downloadFile(ctx, downloader, reporter, "uv archive (attempt "+strconv.Itoa(attempt)+")", baseURL+"/"+asset, archivePath); err != nil {
			return false, errors.New("download the latest uv release after sudo preflight")
		}
		if err := downloadFile(ctx, downloader, reporter, "uv archive checksum", baseURL+"/"+asset+".sha256", checksumPath); err != nil {
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
		if err == nil && actual == expected {
			break
		}
		if attempt == uvDownloadAttempts {
			if err != nil {
				return false, fmt.Errorf("refusing to install uv: release checksum verification failed: calculate archive checksum: %w", err)
			}
			return false, fmt.Errorf("refusing to install uv: release checksum verification failed (expected %s, got %s)", expected, actual)
		}
	}
	binaries, err := readUVArchive(archivePath)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return false, fmt.Errorf("create uv install directory: %w", err)
	}
	reportStep(reporter, "Installing verified uv and uvx...")
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

type uvReleaseMetadata struct {
	TagName string `json:"tag_name"`
}

func resolveLatestUVTag(ctx context.Context, downloader FileDownloader, destination string, reporters ...ProgressReporter) (string, error) {
	reporter := firstProgressReporter(reporters...)
	if downloader == nil {
		return "", errors.New("uv release downloader is not configured")
	}
	if err := downloadFile(ctx, downloader, reporter, "latest uv release metadata", uvReleaseAPIURL, destination); err != nil {
		return "", errors.New("resolve the latest uv release")
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		return "", errors.New("read the latest uv release metadata")
	}
	var metadata uvReleaseMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", errors.New("decode the latest uv release metadata")
	}
	tag := strings.TrimSpace(metadata.TagName)
	if !isReleaseTag(tag) {
		return "", fmt.Errorf("latest uv release returned invalid tag %q", tag)
	}
	return tag, nil
}

func isReleaseTag(value string) bool {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return false
			}
		}
	}
	return true
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
