package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/install"
	"github.com/baron-shared-brain/baron/internal/version"
)

const (
	DefaultRepository = "thienty1207/Baron-Nexus"
	DefaultAPIBaseURL = "https://api.github.com"
	maxMetadataBytes  = 2 << 20
	maxChecksumBytes  = 8 << 20
	maxBinaryBytes    = 128 << 20
)

// Client downloads and verifies Baron release assets. APIBaseURL and
// AllowInsecure are injectable for deterministic tests; production defaults
// remain the GitHub HTTPS API and release asset hosts.
type Client struct {
	HTTPClient    *http.Client
	APIBaseURL    string
	Repository    string
	GOOS          string
	GOARCH        string
	AllowInsecure bool
	Progress      install.ProgressReporter
}

type Report struct {
	Version  string
	Target   string
	Changed  bool
	Rollback string
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type releaseManifest struct {
	Project   string   `json:"project"`
	Version   string   `json:"version"`
	Artifacts []string `json:"artifacts"`
}

// InstallLatest installs the latest compatible release. When force is false,
// a matching currentVersion is an intentional no-op after the release tag is
// verified. The target is not mutated until manifest, checksum, and binary
// launch validation all succeed.
func (c Client) InstallLatest(ctx context.Context, target, currentVersion string, force bool) (Report, error) {
	c.step("Checking latest Baron release...")
	if target == "" {
		return Report{}, errors.New("Baron install target is required")
	}
	assetName, err := c.assetName()
	if err != nil {
		return Report{}, err
	}
	release, err := c.latest(ctx)
	if err != nil {
		return Report{}, err
	}
	tagVersion, err := normalizeReleaseVersion(release.TagName)
	if err != nil {
		return Report{}, err
	}
	if !force && strings.TrimSpace(currentVersion) == tagVersion {
		info, statErr := os.Stat(target)
		if statErr == nil {
			if !info.Mode().IsRegular() {
				return Report{}, fmt.Errorf("Baron install target is not a regular file: %s", target)
			}
			c.step(fmt.Sprintf("Baron %s is already up to date.", tagVersion))
			return Report{Version: tagVersion, Target: target, Changed: false}, nil
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return Report{}, fmt.Errorf("inspect Baron install target: %w", statErr)
		}
	}

	assets := make(map[string]string, len(release.Assets))
	for _, asset := range release.Assets {
		if asset.Name != "" && asset.BrowserDownloadURL != "" {
			assets[asset.Name] = asset.BrowserDownloadURL
		}
	}
	manifestURL, ok := assets["release-manifest.json"]
	if !ok {
		return Report{}, errors.New("Baron release is missing release-manifest.json")
	}
	sumsURL, ok := assets["SHA256SUMS"]
	if !ok {
		return Report{}, errors.New("Baron release is missing SHA256SUMS")
	}
	binaryURL, ok := assets[assetName]
	if !ok {
		return Report{}, fmt.Errorf("Baron release has no compatible asset %q", assetName)
	}

	manifestData, err := c.download(ctx, manifestURL, maxMetadataBytes, "Baron release manifest")
	if err != nil {
		return Report{}, fmt.Errorf("download Baron release manifest: %w", err)
	}
	var manifest releaseManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return Report{}, fmt.Errorf("decode Baron release manifest: %w", err)
	}
	manifestVersion, err := normalizeReleaseVersion(manifest.Version)
	if err != nil {
		return Report{}, fmt.Errorf("Baron release manifest: %w", err)
	}
	if manifestVersion != tagVersion {
		return Report{}, fmt.Errorf("Baron release manifest version %s does not match tag version %s", manifestVersion, tagVersion)
	}
	if !contains(manifest.Artifacts, assetName) {
		return Report{}, fmt.Errorf("Baron release manifest does not list %s", assetName)
	}

	sumsData, err := c.download(ctx, sumsURL, maxChecksumBytes, "Baron release checksums")
	if err != nil {
		return Report{}, fmt.Errorf("download Baron release checksums: %w", err)
	}
	expected, err := checksumFor(sumsData, assetName)
	if err != nil {
		return Report{}, err
	}
	binaryData, err := c.download(ctx, binaryURL, maxBinaryBytes, "Baron release binary")
	if err != nil {
		return Report{}, fmt.Errorf("download Baron release binary: %w", err)
	}
	actual := sha256.Sum256(binaryData)
	if hex.EncodeToString(actual[:]) != expected {
		return Report{}, fmt.Errorf("Baron release checksum mismatch for %s", assetName)
	}

	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Report{}, fmt.Errorf("create Baron install directory: %w", err)
	}
	temp, err := os.CreateTemp(parent, ".baron-release-*.exe")
	if err != nil {
		return Report{}, fmt.Errorf("stage Baron release binary: %w", err)
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o755); err != nil {
		_ = temp.Close()
		return Report{}, fmt.Errorf("set staged Baron binary permissions: %w", err)
	}
	if _, err := temp.Write(binaryData); err != nil {
		_ = temp.Close()
		return Report{}, fmt.Errorf("write staged Baron binary: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return Report{}, fmt.Errorf("sync staged Baron binary: %w", err)
	}
	if err := temp.Close(); err != nil {
		return Report{}, fmt.Errorf("close staged Baron binary: %w", err)
	}
	if err := validateBinary(ctx, tempPath, tagVersion); err != nil {
		return Report{}, fmt.Errorf("validate Baron release binary: %w", err)
	}
	c.step("Installing verified Baron binary...")
	if c.isWindows() {
		stagedPath := target + ".baron-update-" + tagVersion + ".exe"
		if err := os.Rename(tempPath, stagedPath); err != nil {
			return Report{}, fmt.Errorf("stage verified Baron Windows update: %w", err)
		}
		cleanup = false
		return Report{}, fmt.Errorf("Baron Windows update staged at %s; close Baron and rerun install.ps1 -BinarySource \"%s\" -Destination \"%s\" -AllowReplace", stagedPath, stagedPath, target)
	}

	backup, err := install.UpdateBinary(target, tempPath, func() error {
		return validateBinary(ctx, target, tagVersion)
	})
	if err != nil {
		return Report{}, err
	}
	cleanup = false
	_ = os.Remove(tempPath)
	c.step("Verified Baron binary installed.")
	return Report{Version: tagVersion, Target: target, Changed: true, Rollback: backup}, nil
}

func (c Client) latest(ctx context.Context) (githubRelease, error) {
	base := c.APIBaseURL
	if base == "" {
		base = DefaultAPIBaseURL
	}
	repository := c.RepositoryOrDefault()
	if err := validateRepository(repository); err != nil {
		return githubRelease{}, err
	}
	endpoint := strings.TrimRight(base, "/") + "/repos/" + repository + "/releases/latest"
	data, err := c.download(ctx, endpoint, maxMetadataBytes, "latest Baron release metadata")
	if err != nil {
		return githubRelease{}, fmt.Errorf("read latest Baron release: %w", err)
	}
	var release githubRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return githubRelease{}, fmt.Errorf("decode latest Baron release: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return githubRelease{}, errors.New("latest Baron release has no tag")
	}
	return release, nil
}

func (c Client) download(ctx context.Context, rawURL string, maxBytes int64, label string) ([]byte, error) {
	c.step("Downloading " + label + "...")
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("Baron release URL is invalid")
	}
	if !c.AllowInsecure && parsed.Scheme != "https" {
		return nil, errors.New("Baron release downloads require HTTPS")
	}
	if !c.AllowInsecure && !allowedGitHubHost(parsed.Hostname()) {
		return nil, fmt.Errorf("Baron release URL host %q is not an allowed GitHub host", parsed.Hostname())
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Baron-Nexus/"+version.Value)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Baron release download returned HTTP %d", resp.StatusCode)
	}
	reader := install.NewProgressReader(resp.Body, c.Progress, label, resp.ContentLength)
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("Baron release response exceeds the safety limit")
	}
	c.step(label + " downloaded.")
	return data, nil
}

func (c Client) step(label string) {
	if c.Progress != nil {
		c.Progress.Step(label)
	}
}

func (c Client) assetName() (string, error) {
	goos, goarch := c.platform()
	if goarch != "amd64" || (goos != "linux" && goos != "windows") {
		return "", fmt.Errorf("unsupported Baron release platform %s/%s", goos, goarch)
	}
	if goos == "windows" {
		return "baron-windows-amd64.exe", nil
	}
	return "baron-linux-amd64", nil
}

func (c Client) isWindows() bool {
	goos, _ := c.platform()
	return goos == "windows"
}

func (c Client) platform() (string, string) {
	goos := c.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := c.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

func (c Client) RepositoryOrDefault() string {
	if strings.TrimSpace(c.Repository) == "" {
		return DefaultRepository
	}
	return strings.TrimSpace(c.Repository)
}

func validateRepository(repository string) error {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(repository, "\\?#") || parts[0] == "." || parts[1] == "." || parts[0] == ".." || parts[1] == ".." {
		return fmt.Errorf("invalid Baron release repository %q", repository)
	}
	return nil
}

func allowedGitHubHost(host string) bool {
	switch strings.ToLower(host) {
	case "api.github.com", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return true
	default:
		return false
	}
}

func normalizeReleaseVersion(value string) (string, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "v"))
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", fmt.Errorf("invalid Baron release version %q", value)
	}
	for _, part := range parts {
		for _, char := range part {
			if char < '0' || char > '9' {
				return "", fmt.Errorf("invalid Baron release version %q", value)
			}
		}
	}
	return value, nil
}

func checksumFor(data []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != assetName {
			continue
		}
		checksum := strings.ToLower(fields[0])
		if len(checksum) != sha256.Size*2 {
			return "", fmt.Errorf("invalid SHA-256 entry for %s", assetName)
		}
		if _, err := hex.DecodeString(checksum); err != nil {
			return "", fmt.Errorf("invalid SHA-256 entry for %s", assetName)
		}
		return checksum, nil
	}
	return "", fmt.Errorf("SHA256SUMS has no entry for %s", assetName)
}

func validateBinary(ctx context.Context, path, expectedVersion string) error {
	output, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return fmt.Errorf("candidate did not launch: %w", err)
	}
	want := "baron " + expectedVersion
	if strings.TrimSpace(string(output)) != want {
		return fmt.Errorf("candidate reported %q, expected %q", strings.TrimSpace(string(output)), want)
	}
	return nil
}

// CurrentExecutablePath resolves the running Baron binary, while allowing the
// installer path override used by package tests and explicit user installs.
func CurrentExecutablePath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("BARON_INSTALL_PATH")); override != "" {
		return filepath.Abs(override)
	}
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve Baron executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Abs(path)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
