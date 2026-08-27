package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

type latestUVDownloadFixture struct {
	archive           []byte
	digest            string
	metadataTag       string
	archiveDownloads  int
	checksumDownloads int
	urls              []string
	runner            *commandFixture
	alwaysMismatch    bool
}

func (f *latestUVDownloadFixture) Download(_ context.Context, rawURL, destination string) error {
	f.urls = append(f.urls, rawURL)
	var data []byte
	switch {
	case rawURL == "https://api.github.com/repos/astral-sh/uv/releases/latest":
		data = []byte(`{"tag_name":"` + f.metadataTag + `"}`)
	case strings.HasSuffix(rawURL, ".sha256"):
		f.checksumDownloads++
		digest := f.digest
		if f.alwaysMismatch || f.checksumDownloads == 1 {
			digest = strings.Repeat("0", sha256.Size*2)
		}
		data = []byte(digest + "  uv.tar.gz\n")
	default:
		f.archiveDownloads++
		data = f.archive
		if f.runner != nil && f.archiveDownloads >= 2 {
			f.runner.available["uv"] = true
			f.runner.available["uvx"] = true
		}
	}
	return os.WriteFile(destination, data, 0o600)
}

func TestEnsureUVResolvesOneLatestReleaseAndRetriesTheSameChecksumPair(t *testing.T) {
	archive := uvTestArchive(t)
	digest := sha256.Sum256(archive)
	runner := &commandFixture{available: map[string]bool{}}
	downloader := &latestUVDownloadFixture{
		archive:     archive,
		digest:      hex.EncodeToString(digest[:]),
		metadataTag: "0.12.6",
		runner:      runner,
	}
	if _, err := ensureUV(context.Background(), runner, downloader, "amd64", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if downloader.archiveDownloads != 2 || downloader.checksumDownloads != 2 {
		t.Fatalf("uv downloads archive=%d checksum=%d, want one complete retry", downloader.archiveDownloads, downloader.checksumDownloads)
	}
	for _, rawURL := range downloader.urls[1:] {
		if !strings.Contains(rawURL, "/releases/download/0.12.6/") {
			t.Fatalf("uv download did not use resolved release tag: %s", rawURL)
		}
	}
}

func TestEnsureUVChecksumFailureReportsNonSecretDigests(t *testing.T) {
	archive := uvTestArchive(t)
	actual := sha256.Sum256(archive)
	expected := strings.Repeat("0", sha256.Size*2)
	runner := &commandFixture{available: map[string]bool{}}
	downloader := &latestUVDownloadFixture{
		archive:        archive,
		digest:         expected,
		metadataTag:    "0.12.6",
		runner:         runner,
		alwaysMismatch: true,
	}
	_, err := ensureUV(context.Background(), runner, downloader, "amd64", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), expected) || !strings.Contains(err.Error(), hex.EncodeToString(actual[:])) {
		t.Fatalf("checksum error=%v, want expected and actual digests", err)
	}
	if strings.Contains(err.Error(), string(archive)) {
		t.Fatal("checksum error unexpectedly included archive data")
	}
}

type nodeIndexDownloadFixture struct {
	data []byte
}

func (f nodeIndexDownloadFixture) Download(_ context.Context, _ string, destination string) error {
	return os.WriteFile(destination, f.data, 0o600)
}

func TestResolveLatestNodeMajorUsesOfficialReleaseIndex(t *testing.T) {
	major, err := resolveLatestNodeMajor(context.Background(), nodeIndexDownloadFixture{
		data: []byte(`[{"version":"v26.8.1","date":"2026-08-26","lts":false},{"version":"v24.20.0","date":"2026-08-26","lts":"Krypton"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if major != "26" {
		t.Fatalf("latest Node major=%q, want 26", major)
	}
}

func TestResolveLatestNodeMajorRejectsMalformedReleaseIndex(t *testing.T) {
	_, err := resolveLatestNodeMajor(context.Background(), nodeIndexDownloadFixture{data: []byte(`[{"version":"not-a-version"}]`)})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "node release") {
		t.Fatalf("malformed Node index error=%v", err)
	}
}

type latestHostDownloadFixture struct {
	archive []byte
	digest  string
	urls    []string
	runner  *commandFixture
}

func (f *latestHostDownloadFixture) Download(_ context.Context, rawURL, destination string) error {
	f.urls = append(f.urls, rawURL)
	var data []byte
	switch {
	case rawURL == "https://nodejs.org/dist/index.json":
		data = []byte(`[{"version":"v26.8.1","date":"2026-08-26","lts":false}]`)
	case rawURL == "https://api.github.com/repos/astral-sh/uv/releases/latest":
		data = []byte(`{"tag_name":"0.12.6"}`)
	case strings.HasSuffix(rawURL, ".sha256"):
		data = []byte(f.digest + "  uv.tar.gz\n")
	case strings.Contains(rawURL, "/releases/download/"):
		data = f.archive
		if f.runner != nil {
			f.runner.available["uv"] = true
			f.runner.available["uvx"] = true
		}
	default:
		data = []byte("upstream signing key")
	}
	return os.WriteFile(destination, data, 0o600)
}

func TestEnsureHostToolchainRefreshesExistingDependenciesToLatest(t *testing.T) {
	osRelease := t.TempDir() + "/os-release"
	if err := os.WriteFile(osRelease, []byte("ID=ubuntu\nVERSION_ID=26.04\nVERSION_CODENAME=questing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := uvTestArchive(t)
	digest := sha256.Sum256(archive)
	runner := &commandFixture{available: map[string]bool{"sudo": true, "node": true, "npm": true, "npx": true, "pnpm": true}}
	runner.outputs = map[string]string{"node --version": "v26.8.1\n"}
	downloader := &latestHostDownloadFixture{archive: archive, digest: hex.EncodeToString(digest[:]), runner: runner}
	if _, err := EnsureHostToolchain(context.Background(), runner, HostToolchainOptions{GOOS: "linux", GOARCH: "amd64", OSReleasePath: osRelease, Home: t.TempDir(), Downloader: downloader}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, "\n")
	for _, want := range []string{"sudo -n apt-get install -y nodejs", "sudo -n npm install --global pnpm@latest"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("latest host refresh missing %q in calls:\n%s", want, joined)
		}
	}
	if !strings.Contains(strings.Join(downloader.urls, "\n"), "/releases/download/0.12.6/") {
		t.Fatalf("latest uv release tag missing from downloads: %#v", downloader.urls)
	}
}
