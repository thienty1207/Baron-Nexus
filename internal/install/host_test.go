package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type sudoReauthFixture struct {
	calls          []string
	interactive    int
	failFirstSudo  bool
	failedSudoCall bool
	available      map[string]bool
	outputs        map[string]string
	onRun          func(name string, args ...string)
}

func (f *sudoReauthFixture) LookPath(name string) (string, error) {
	if f.available != nil && !f.available[name] {
		return "", errors.New("command missing")
	}
	return "/fake/" + name, nil
}

func (f *sudoReauthFixture) Run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.onRun != nil {
		f.onRun(name, args...)
	}
	if f.failFirstSudo && !f.failedSudoCall && name == "sudo" && len(args) > 0 && args[0] == "-n" {
		f.failedSudoCall = true
		return "", errors.New("sudo: a password is required")
	}
	if output, ok := f.outputs[call]; ok {
		return output, nil
	}
	return "ok", nil
}

func (f *sudoReauthFixture) RunInteractive(_ context.Context, name string, args ...string) (string, error) {
	f.interactive++
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return "", nil
}

type hostDownloadFixture struct {
	archive       []byte
	checksum      string
	markAvailable func()
}

func (f hostDownloadFixture) Download(_ context.Context, rawURL, destination string) error {
	var data []byte
	switch {
	case rawURL == nodeReleaseIndexURL:
		data = []byte(`[{"version":"v26.8.1","date":"2026-08-26","lts":false}]`)
	case rawURL == uvReleaseAPIURL:
		data = []byte(`{"tag_name":"0.12.6"}`)
	case strings.HasSuffix(rawURL, ".sha256"):
		data = []byte(f.checksum + "  uv.tar.gz\n")
	case strings.Contains(rawURL, uvReleaseDownloadURL+"/"):
		data = f.archive
	default:
		data = []byte("nodesource-key")
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		return err
	}
	if f.markAvailable != nil && strings.Contains(rawURL, uvReleaseDownloadURL+"/") {
		f.markAvailable()
	}
	return nil
}

func uvTestArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, item := range []struct {
		name string
		data []byte
	}{{name: "uv-x86_64-unknown-linux-gnu/uv", data: []byte("uv-binary")}, {name: "uv-x86_64-unknown-linux-gnu/uvx", data: []byte("uvx-binary")}} {
		if err := tarWriter.WriteHeader(&tar.Header{Name: item.name, Mode: 0o755, Size: int64(len(item.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(item.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestRunSudoReauthenticatesOnceAfterAuthorizationFailure(t *testing.T) {
	fixture := &sudoReauthFixture{failFirstSudo: true}
	if _, err := runSudo(context.Background(), fixture, "docker", "info"); err != nil {
		t.Fatal(err)
	}
	if fixture.interactive != 1 {
		t.Fatalf("interactive sudo calls=%d, want 1; calls=%v", fixture.interactive, fixture.calls)
	}
	if got := strings.Join(fixture.calls, "\n"); !strings.Contains(got, "sudo -n docker info") || !strings.Contains(got, "sudo -v") {
		t.Fatalf("reauth sequence missing: %v", fixture.calls)
	}
}

func TestEnsureHostToolchainRejectsUnsupportedDistributionBeforeDownload(t *testing.T) {
	osRelease := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(osRelease, []byte("ID=arch\nVERSION_ID=1\nVERSION_CODENAME=rolling\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := &sudoReauthFixture{}
	_, err := EnsureHostToolchain(context.Background(), fixture, HostToolchainOptions{GOOS: "linux", GOARCH: "amd64", OSReleasePath: osRelease})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "ubuntu/debian") {
		t.Fatalf("unsupported distribution error=%v", err)
	}
	if fixture.interactive != 0 || len(fixture.calls) != 0 {
		t.Fatalf("unsupported distribution triggered host work: %#v", fixture.calls)
	}
}

func TestEnsureHostToolchainUsesSudoPreflightBeforePackageWork(t *testing.T) {
	osRelease := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(osRelease, []byte("ID=ubuntu\nVERSION_ID=26.04\nVERSION_CODENAME=questing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := &sudoReauthFixture{}
	fixture.available = map[string]bool{"sudo": true, "node": true, "npm": true, "npx": true, "pnpm": true, "uv": true, "uvx": true}
	fixture.outputs = map[string]string{"node --version": "v24.19.0\n"}
	archive := uvTestArchive(t)
	digest := sha256.Sum256(archive)
	_, err := EnsureHostToolchain(context.Background(), fixture, HostToolchainOptions{
		GOOS:          "linux",
		GOARCH:        "amd64",
		OSReleasePath: osRelease,
		Home:          t.TempDir(),
		Downloader:    hostDownloadFixture{archive: archive, checksum: hex.EncodeToString(digest[:])},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.calls) < 2 || fixture.calls[0] != "sudo -v" || fixture.calls[1] != "sudo -n true" {
		t.Fatalf("host work did not start after sudo preflight: %#v", fixture.calls)
	}
}

func TestEnsureHostToolchainBootstrapsMissingNodeAndPnpm(t *testing.T) {
	osRelease := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(osRelease, []byte("ID=debian\nVERSION_ID=13\nVERSION_CODENAME=trixie\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := &sudoReauthFixture{available: map[string]bool{"sudo": true, "uv": true, "uvx": true}}
	fixture.outputs = map[string]string{"node --version": "v24.19.0\n"}
	fixture.onRun = func(name string, args ...string) {
		call := name + " " + strings.Join(args, " ")
		if call == "sudo -n apt-get install -y nodejs" {
			fixture.available["node"] = true
			fixture.available["npm"] = true
			fixture.available["npx"] = true
		}
		if call == "sudo -n npm install --global pnpm@latest" {
			fixture.available["pnpm"] = true
		}
	}
	report, err := EnsureHostToolchain(context.Background(), fixture, HostToolchainOptions{
		GOOS:          "linux",
		GOARCH:        "amd64",
		OSReleasePath: osRelease,
		Home:          t.TempDir(),
		Downloader: func() FileDownloader {
			archive := uvTestArchive(t)
			digest := sha256.Sum256(archive)
			return hostDownloadFixture{archive: archive, checksum: hex.EncodeToString(digest[:])}
		}(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || !strings.Contains(strings.Join(report.Installed, ","), "node/npm/npx") || !strings.Contains(strings.Join(report.Installed, ","), "pnpm") {
		t.Fatalf("host report=%#v, want Node and pnpm bootstrap", report)
	}
	calls := strings.Join(fixture.calls, "\n")
	for _, want := range []string{"sudo -v", "sudo -n true", "sudo -n apt-get update", "sudo -n apt-get install -y nodejs", "sudo -n npm install --global pnpm@latest"} {
		if !strings.Contains(calls, want) {
			t.Fatalf("host bootstrap missing %q in calls:\n%s", want, calls)
		}
	}
}

func TestEnsureHostToolchainInstallsUVAndUVXOnlyAfterChecksumVerification(t *testing.T) {
	osRelease := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(osRelease, []byte("ID=ubuntu\nVERSION_ID=26.04\nVERSION_CODENAME=questing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	fixture := &sudoReauthFixture{available: map[string]bool{"sudo": true, "node": true, "npm": true, "npx": true, "pnpm": true}}
	fixture.outputs = map[string]string{"node --version": "v24.19.0\n"}
	archive := uvTestArchive(t)
	digest := sha256.Sum256(archive)
	download := hostDownloadFixture{archive: archive, checksum: hex.EncodeToString(digest[:]), markAvailable: func() {
		fixture.available["uv"] = true
		fixture.available["uvx"] = true
	}}
	if _, err := EnsureHostToolchain(context.Background(), fixture, HostToolchainOptions{GOOS: "linux", GOARCH: "amd64", OSReleasePath: osRelease, Home: home, Downloader: download}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"uv", "uvx"} {
		data, err := os.ReadFile(filepath.Join(home, ".local", "bin", name))
		if err != nil {
			t.Fatalf("read installed %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("installed %s is empty", name)
		}
	}
}

func TestEnsureHostToolchainRejectsUVChecksumMismatch(t *testing.T) {
	osRelease := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(osRelease, []byte("ID=debian\nVERSION_ID=13\nVERSION_CODENAME=trixie\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := &sudoReauthFixture{available: map[string]bool{"sudo": true, "node": true, "npm": true, "npx": true, "pnpm": true}}
	fixture.outputs = map[string]string{"node --version": "v24.19.0\n"}
	archive := uvTestArchive(t)
	download := hostDownloadFixture{archive: archive, checksum: strings.Repeat("0", sha256.Size*2)}
	_, err := EnsureHostToolchain(context.Background(), fixture, HostToolchainOptions{GOOS: "linux", GOARCH: "amd64", OSReleasePath: osRelease, Home: t.TempDir(), Downloader: download})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "checksum") {
		t.Fatalf("checksum failure=%v", err)
	}
	if strings.Contains(err.Error(), fmt.Sprintf("%x", archive)) {
		t.Fatal("checksum error unexpectedly included archive data")
	}
}
