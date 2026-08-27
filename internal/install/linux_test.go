package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type dockerBootstrapFixture struct {
	available                    map[string]bool
	outputs                      map[string]string
	errors                       map[string]error
	calls                        []string
	downloads                    []string
	installed                    bool
	aptUpdates                   int
	failOfficialRepositoryUpdate bool
}

func (f *dockerBootstrapFixture) LookPath(name string) (string, error) {
	if f.available[name] || (name == "docker" && f.installed) {
		return "/fake/" + name, nil
	}
	return "", errors.New("missing " + name)
}

func (f *dockerBootstrapFixture) Run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if call == "sudo -n apt-get update" {
		f.aptUpdates++
		if f.failOfficialRepositoryUpdate && f.aptUpdates == 2 {
			return "", errors.New("repository unavailable")
		}
	}
	if err, ok := f.errors[call]; ok {
		return "", err
	}
	if name == "sudo" && len(args) >= 5 && args[0] == "-n" && args[1] == "apt-get" && args[2] == "install" {
		f.installed = true
	}
	if call == "sudo -n docker info" && f.installed {
		return "ready", nil
	}
	return f.outputs[call], nil
}

func (f *dockerBootstrapFixture) Download(_ context.Context, rawURL, destination string) error {
	f.downloads = append(f.downloads, rawURL)
	return os.WriteFile(destination, []byte("docker-key"), 0o600)
}

func writeOSRelease(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHTTPFileDownloaderDoesNotTruncateReleaseArchive(t *testing.T) {
	payload := bytes.Repeat([]byte("uv"), 2*1024*1024)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := writer.Write(payload); err != nil {
			t.Errorf("write test payload: %v", err)
		}
	}))
	defer server.Close()

	destination := filepath.Join(t.TempDir(), "uv.tar.gz")
	if err := (httpFileDownloader{}).Download(context.Background(), server.URL, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("downloaded archive size=%d, want %d", len(got), len(payload))
	}
}

func TestHTTPFileDownloaderReportsDownloadProgress(t *testing.T) {
	payload := bytes.Repeat([]byte("uv"), 2*1024*1024)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		if _, err := writer.Write(payload); err != nil {
			t.Errorf("write test payload: %v", err)
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	destination := filepath.Join(t.TempDir(), "uv.tar.gz")
	downloader := httpFileDownloader{Progress: NewProgressReporter(&output)}
	if err := downloader.DownloadWithProgress(context.Background(), server.URL+"/uv.tar.gz", destination, "uv archive"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "uv archive") || !strings.Contains(output.String(), "100%") {
		t.Fatalf("download progress was not reported:\n%s", output.String())
	}
}

func TestEnsureDockerRejectsUnsupportedDistributionBeforeNetwork(t *testing.T) {
	fixture := &dockerBootstrapFixture{available: map[string]bool{"sudo": true}}
	_, err := EnsureDocker(context.Background(), fixture, DockerBootstrapOptions{
		GOOS:          "linux",
		GOARCH:        "amd64",
		OSReleasePath: writeOSRelease(t, "ID=fedora\nVERSION_ID=42\n"),
	})
	if err == nil || !strings.Contains(err.Error(), "Ubuntu/Debian") {
		t.Fatalf("unsupported distribution was not rejected: %v", err)
	}
	if len(fixture.downloads) != 0 || strings.Contains(strings.Join(fixture.calls, "\n"), "apt-get") {
		t.Fatalf("unsupported host touched network/package path: downloads=%#v calls=%#v", fixture.downloads, fixture.calls)
	}
}

func TestEnsureDockerRequiresSudoBeforeNetwork(t *testing.T) {
	fixture := &dockerBootstrapFixture{available: map[string]bool{"curl": true}}
	_, err := EnsureDocker(context.Background(), fixture, DockerBootstrapOptions{
		GOOS:          "linux",
		GOARCH:        "amd64",
		OSReleasePath: writeOSRelease(t, "ID=ubuntu\nVERSION_ID=26.04\nUBUNTU_CODENAME=resolute\n"),
	})
	if err == nil || !strings.Contains(err.Error(), "sudo -v") {
		t.Fatalf("missing sudo did not produce exact repair guidance: %v", err)
	}
	if len(fixture.downloads) != 0 || strings.Contains(strings.Join(fixture.calls, "\n"), "apt-get") {
		t.Fatalf("missing sudo touched network/package path: downloads=%#v calls=%#v", fixture.downloads, fixture.calls)
	}
}

func TestEnsureDockerUsesOfficialAptPathWithoutDockerGroupMutation(t *testing.T) {
	fixture := &dockerBootstrapFixture{
		available: map[string]bool{"sudo": true, "docker": true},
		outputs:   map[string]string{"sudo -n true": "", "sudo -n docker info": "ready"},
	}
	report, err := EnsureDocker(context.Background(), fixture, DockerBootstrapOptions{
		GOOS:          "linux",
		GOARCH:        "amd64",
		OSReleasePath: writeOSRelease(t, "ID=ubuntu\nVERSION_ID=26.04\nUBUNTU_CODENAME=resolute\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || report.UsedSudo {
		t.Fatalf("unexpected ready report: %#v", report)
	}
	if len(fixture.downloads) != 0 {
		t.Fatalf("already-ready Docker downloaded packages: %#v", fixture.downloads)
	}
	for _, call := range fixture.calls {
		if strings.Contains(call, "usermod") || strings.Contains(call, "groupadd") {
			t.Fatalf("bootstrap changed root-equivalent group membership: %s", call)
		}
	}
}

func TestEnsureDockerRefreshesHealthyDaemonWhenRequested(t *testing.T) {
	fixture := &dockerBootstrapFixture{
		available: map[string]bool{"sudo": true, "docker": true, "systemctl": true},
		outputs: map[string]string{
			"docker info":         "ready",
			"sudo -n true":        "",
			"sudo -n docker info": "ready",
		},
	}
	report, err := EnsureDocker(context.Background(), fixture, DockerBootstrapOptions{
		GOOS:          "linux",
		GOARCH:        "amd64",
		OSReleasePath: writeOSRelease(t, "ID=ubuntu\nVERSION_ID=26.04\nUBUNTU_CODENAME=resolute\n"),
		Downloader:    fixture,
		Refresh:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || !report.Installed || !report.UsedSudo {
		t.Fatalf("healthy daemon was not refreshed: %#v", report)
	}
	if len(fixture.downloads) != 1 || fixture.downloads[0] != "https://download.docker.com/linux/ubuntu/gpg" {
		t.Fatalf("latest Docker repository was not resolved: %#v", fixture.downloads)
	}
	joined := strings.Join(fixture.calls, "\n")
	if !strings.Contains(joined, "sudo -n apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin") {
		t.Fatalf("healthy Docker refresh did not install latest official package set: %#v", fixture.calls)
	}
}

func TestEnsureDockerRepairsStoppedDaemonWithSudo(t *testing.T) {
	fixture := &dockerBootstrapFixture{
		available: map[string]bool{"sudo": true, "docker": true, "systemctl": true},
		outputs: map[string]string{
			"sudo -n true":                          "",
			"sudo -n systemctl enable --now docker": "",
			"sudo -n docker info":                   "ready",
		},
		errors: map[string]error{"docker info": errors.New("permission denied")},
	}
	report, err := EnsureDocker(context.Background(), fixture, DockerBootstrapOptions{
		GOOS:          "linux",
		GOARCH:        "amd64",
		OSReleasePath: writeOSRelease(t, "ID=debian\nVERSION_ID=13\nVERSION_CODENAME=trixie\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || !report.UsedSudo || !report.DaemonStarted {
		t.Fatalf("stopped daemon was not repaired: %#v", report)
	}
	joined := strings.Join(fixture.calls, "\n")
	if !strings.Contains(joined, "sudo -n systemctl enable --now docker") {
		t.Fatalf("systemctl repair was not attempted: %#v", fixture.calls)
	}
}

func TestEnsureDockerInstallsOfficialPackagesAfterSudoPreflight(t *testing.T) {
	fixture := &dockerBootstrapFixture{
		available: map[string]bool{"sudo": true},
		outputs: map[string]string{
			"sudo -n true": "",
		},
	}
	report, err := EnsureDocker(context.Background(), fixture, DockerBootstrapOptions{
		GOOS:          "linux",
		GOARCH:        "amd64",
		OSReleasePath: writeOSRelease(t, "ID=ubuntu\nVERSION_ID=26.04\nUBUNTU_CODENAME=resolute\n"),
		Downloader:    fixture,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || !report.Installed || !report.UsedSudo {
		t.Fatalf("official install path did not report readiness: %#v", report)
	}
	if len(fixture.calls) == 0 || fixture.calls[0] != "sudo -n true" {
		t.Fatalf("sudo preflight was not first: %#v", fixture.calls)
	}
	if len(fixture.downloads) != 1 || fixture.downloads[0] != "https://download.docker.com/linux/ubuntu/gpg" {
		t.Fatalf("unexpected Docker key download: %#v", fixture.downloads)
	}
	joined := strings.Join(fixture.calls, "\n")
	for _, packageName := range []string{"docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"} {
		if !strings.Contains(joined, packageName) {
			t.Fatalf("package %s missing from install plan: %#v", packageName, fixture.calls)
		}
	}
	if strings.Contains(joined, "usermod") || strings.Contains(joined, "groupadd") {
		t.Fatalf("install path mutated root-equivalent group membership: %#v", fixture.calls)
	}
}

func TestEnsureDockerWindowsReturnsManualGuidanceBeforeHostInspection(t *testing.T) {
	fixture := &dockerBootstrapFixture{}
	_, err := EnsureDocker(context.Background(), fixture, DockerBootstrapOptions{GOOS: "windows", GOARCH: "amd64"})
	if err == nil || !strings.Contains(err.Error(), "Docker Desktop") || !strings.Contains(err.Error(), "WSL2") {
		t.Fatalf("Windows guidance was not explicit: %v", err)
	}
	if len(fixture.calls) != 0 || len(fixture.downloads) != 0 {
		t.Fatalf("Windows path touched host/network: calls=%#v downloads=%#v", fixture.calls, fixture.downloads)
	}
}

func TestEnsureDockerCleansOnlyNewAptStateAfterRepositoryFailure(t *testing.T) {
	fixture := &dockerBootstrapFixture{
		available:                    map[string]bool{"sudo": true},
		failOfficialRepositoryUpdate: true,
	}
	keyPath := filepath.Join(t.TempDir(), "docker.asc")
	sourcePath := filepath.Join(t.TempDir(), "docker.sources")
	_, err := EnsureDocker(context.Background(), fixture, DockerBootstrapOptions{
		GOOS:          "linux",
		GOARCH:        "amd64",
		OSReleasePath: writeOSRelease(t, "ID=ubuntu\nVERSION_ID=26.04\nUBUNTU_CODENAME=resolute\n"),
		Downloader:    fixture,
		AptKeyPath:    keyPath,
		AptSourcePath: sourcePath,
	})
	if err == nil || !strings.Contains(err.Error(), "official Docker apt repository") {
		t.Fatalf("repository failure was not actionable: %v", err)
	}
	joined := strings.Join(fixture.calls, "\n")
	if !strings.Contains(joined, "sudo -n rm -f "+keyPath) || !strings.Contains(joined, "sudo -n rm -f "+sourcePath) {
		t.Fatalf("Baron-owned apt partial state was not cleaned: %#v", fixture.calls)
	}
	fixture.failOfficialRepositoryUpdate = false
	report, retryErr := EnsureDocker(context.Background(), fixture, DockerBootstrapOptions{
		GOOS: "linux", GOARCH: "amd64", OSReleasePath: writeOSRelease(t, "ID=ubuntu\nVERSION_ID=26.04\nUBUNTU_CODENAME=resolute\n"),
		Downloader: fixture, AptKeyPath: keyPath, AptSourcePath: sourcePath,
	})
	if retryErr != nil || !report.Ready {
		t.Fatalf("bootstrap was not resumable after partial failure: report=%#v err=%v", report, retryErr)
	}
}
