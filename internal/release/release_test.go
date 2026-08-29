package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/install"
)

func TestInstallLatestDownloadsVerifiesAndAtomicallyInstallsCandidate(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("fixture candidate is a Linux amd64 executable")
	}
	root := t.TempDir()
	candidate := []byte("#!/bin/sh\necho 'baron 0.1.0'\n")
	manifest := []byte(`{"project":"Baron Nexus","version":"0.1.0","artifacts":["baron-linux-amd64"]}`)
	sums := checksumLine(candidate, "baron-linux-amd64") + "\n" + checksumLine(manifest, "release-manifest.json") + "\n"
	server := releaseFixtureServer(t, candidate, manifest, []byte(sums))
	defer server.Close()

	target := filepath.Join(root, "bin", "baron")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old Baron binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := Client{
		HTTPClient:    server.Client(),
		APIBaseURL:    server.URL,
		Repository:    "owner/repo",
		GOOS:          "linux",
		GOARCH:        "amd64",
		AllowInsecure: true,
	}
	report, err := client.InstallLatest(context.Background(), target, "0.0.9", false)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Changed || report.Version != "0.1.0" {
		t.Fatalf("report=%#v", report)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(candidate) {
		t.Fatalf("installed candidate=%q", data)
	}
	if report.Rollback == "" {
		t.Fatal("successful update did not report a rollback artifact")
	}
	if _, err := os.Stat(report.Rollback); err != nil {
		t.Fatalf("rollback artifact missing: %v", err)
	}
}

func TestInstallLatestReportsReleaseDownloadProgress(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("fixture candidate is a Linux amd64 executable")
	}
	candidate := []byte("#!/bin/sh\necho 'baron 0.1.0'\n")
	manifest := []byte(`{"project":"Baron Nexus","version":"0.1.0","artifacts":["baron-linux-amd64"]}`)
	sums := checksumLine(candidate, "baron-linux-amd64") + "\n" + checksumLine(manifest, "release-manifest.json") + "\n"
	server := releaseFixtureServer(t, candidate, manifest, []byte(sums))
	defer server.Close()

	var output bytes.Buffer
	client := Client{
		HTTPClient: server.Client(), APIBaseURL: server.URL, Repository: "owner/repo",
		GOOS: "linux", GOARCH: "amd64", AllowInsecure: true,
		Progress: install.NewProgressReporter(&output),
	}
	if _, err := client.InstallLatest(context.Background(), filepath.Join(t.TempDir(), "baron"), "0.0.9", false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Checking latest Baron release", "release manifest", "Baron binary"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("release progress output missing %q:\n%s", want, output.String())
		}
	}
}

func TestInstallLatestIsIdempotentWhenCurrentVersionMatches(t *testing.T) {
	candidate := []byte("candidate")
	manifest := []byte(`{"project":"Baron Nexus","version":"0.1.0","artifacts":["baron-linux-amd64"]}`)
	sums := checksumLine(candidate, "baron-linux-amd64") + "\n" + checksumLine(manifest, "release-manifest.json") + "\n"
	server := releaseFixtureServer(t, candidate, manifest, []byte(sums))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "baron")
	if err := os.WriteFile(target, []byte("current Baron binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := Client{HTTPClient: server.Client(), APIBaseURL: server.URL, Repository: "owner/repo", GOOS: "linux", GOARCH: "amd64", AllowInsecure: true}
	report, err := client.InstallLatest(context.Background(), target, "0.1.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Changed || report.Version != "0.1.0" {
		t.Fatalf("expected no-op report, got %#v", report)
	}
}

func TestInstallLatestDoesNotSkipMissingTargetWhenVersionMatches(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("fixture candidate is a Linux amd64 executable")
	}
	candidate := []byte("#!/bin/sh\necho 'baron 0.1.0'\n")
	manifest := []byte(`{"project":"Baron Nexus","version":"0.1.0","artifacts":["baron-linux-amd64"]}`)
	sums := checksumLine(candidate, "baron-linux-amd64") + "\n" + checksumLine(manifest, "release-manifest.json") + "\n"
	server := releaseFixtureServer(t, candidate, manifest, []byte(sums))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "bin", "baron")
	client := Client{HTTPClient: server.Client(), APIBaseURL: server.URL, Repository: "owner/repo", GOOS: "linux", GOARCH: "amd64", AllowInsecure: true}
	report, err := client.InstallLatest(context.Background(), target, "0.1.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Changed {
		t.Fatalf("missing target was incorrectly treated as a no-op: %#v", report)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("missing target was not installed: %v", err)
	}
}

func TestInstallLatestEqualVersionOnlyFetchesReleaseMetadata(t *testing.T) {
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counts[r.URL.Path]++
		if r.URL.Path != "/repos/owner/repo/releases/latest" {
			http.Error(w, "unexpected binary work", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.1.0", "assets": []map[string]string{
			{"name": "baron-linux-amd64", "browser_download_url": serverURL(r, "/download/baron-linux-amd64")},
			{"name": "release-manifest.json", "browser_download_url": serverURL(r, "/download/release-manifest.json")},
			{"name": "SHA256SUMS", "browser_download_url": serverURL(r, "/download/SHA256SUMS")},
		}})
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "baron")
	if err := os.WriteFile(target, []byte("current Baron binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := Client{HTTPClient: server.Client(), APIBaseURL: server.URL, Repository: "owner/repo", GOOS: "linux", GOARCH: "amd64", AllowInsecure: true}
	report, err := client.InstallLatest(context.Background(), target, "0.1.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Changed || counts["/repos/owner/repo/releases/latest"] != 1 {
		t.Fatalf("unexpected no-op report/counts: %#v %#v", report, counts)
	}
	for path, count := range counts {
		if path != "/repos/owner/repo/releases/latest" && count != 0 {
			t.Fatalf("equal release fetched mutation asset %s %d times", path, count)
		}
	}
}

func serverURL(r *http.Request, path string) string {
	return "http://" + r.Host + path
}

func TestInstallLatestDoesNotDowngradeNewerCurrentVersion(t *testing.T) {
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counts[r.URL.Path]++
		if r.URL.Path != "/repos/owner/repo/releases/latest" {
			http.Error(w, "unexpected downgrade work", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.1.14", "assets": []map[string]string{
			{"name": "baron-linux-amd64", "browser_download_url": serverURL(r, "/download/baron-linux-amd64")},
			{"name": "release-manifest.json", "browser_download_url": serverURL(r, "/download/release-manifest.json")},
			{"name": "SHA256SUMS", "browser_download_url": serverURL(r, "/download/SHA256SUMS")},
		}})
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "baron")
	if err := os.WriteFile(target, []byte("current Baron 0.1.15"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := Client{HTTPClient: server.Client(), APIBaseURL: server.URL, Repository: "owner/repo", GOOS: "linux", GOARCH: "amd64", AllowInsecure: true}
	report, err := client.InstallLatest(context.Background(), target, "0.1.15", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Changed || report.Version != "0.1.15" || counts["/repos/owner/repo/releases/latest"] != 1 {
		t.Fatalf("unexpected newer-current report/counts: %#v %#v", report, counts)
	}
	for path, count := range counts {
		if path != "/repos/owner/repo/releases/latest" && count != 0 {
			t.Fatalf("newer current version fetched downgrade asset %s %d times", path, count)
		}
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "current Baron 0.1.15" {
		t.Fatalf("current binary changed during downgrade guard: %q", data)
	}
}

func TestInstallLatestRejectsChecksumMismatchBeforeMutation(t *testing.T) {
	manifest := []byte(`{"project":"Baron Nexus","version":"0.1.0","artifacts":["baron-linux-amd64"]}`)
	server := releaseFixtureServer(t, []byte("candidate"), manifest, []byte(strings.Repeat("0", 64)+"  baron-linux-amd64\n"+checksumLine(manifest, "release-manifest.json")+"\n"))
	defer server.Close()
	root := t.TempDir()
	target := filepath.Join(root, "baron")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := Client{HTTPClient: server.Client(), APIBaseURL: server.URL, Repository: "owner/repo", GOOS: "linux", GOARCH: "amd64", AllowInsecure: true}
	if _, err := client.InstallLatest(context.Background(), target, "0.0.9", false); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "old" {
		t.Fatalf("target mutated after checksum failure: %q", data)
	}
}

func TestInstallLatestRejectsManifestTagMismatch(t *testing.T) {
	candidate := []byte("candidate")
	manifest := []byte(`{"project":"Baron Nexus","version":"0.0.8","artifacts":["baron-linux-amd64"]}`)
	sums := checksumLine(candidate, "baron-linux-amd64") + "\n" + checksumLine(manifest, "release-manifest.json") + "\n"
	server := releaseFixtureServer(t, candidate, manifest, []byte(sums))
	defer server.Close()
	client := Client{HTTPClient: server.Client(), APIBaseURL: server.URL, Repository: "owner/repo", GOOS: "linux", GOARCH: "amd64", AllowInsecure: true}
	if _, err := client.InstallLatest(context.Background(), filepath.Join(t.TempDir(), "baron"), "0.0.9", false); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("expected manifest version error, got %v", err)
	}
}

func TestInstallLatestRejectsUnsupportedPlatform(t *testing.T) {
	client := Client{HTTPClient: http.DefaultClient, APIBaseURL: "https://api.github.com", Repository: "owner/repo", GOOS: "darwin", GOARCH: "arm64"}
	if _, err := client.InstallLatest(context.Background(), filepath.Join(t.TempDir(), "baron"), "0.0.9", false); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported platform error, got %v", err)
	}
}

func TestInstallLatestRejectsMissingCompatibleAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v0.1.0",
			"assets": []map[string]string{
				{"name": "release-manifest.json", "browser_download_url": "http://" + r.Host + "/manifest"},
				{"name": "SHA256SUMS", "browser_download_url": "http://" + r.Host + "/sums"},
			},
		})
	}))
	defer server.Close()
	client := Client{HTTPClient: server.Client(), APIBaseURL: server.URL, Repository: "owner/repo", GOOS: "linux", GOARCH: "amd64", AllowInsecure: true}
	if _, err := client.InstallLatest(context.Background(), filepath.Join(t.TempDir(), "baron"), "0.0.9", false); err == nil || !strings.Contains(err.Error(), "compatible asset") {
		t.Fatalf("expected missing asset error, got %v", err)
	}
}

func TestInstallLatestRejectsOversizedReleaseMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/owner/repo/releases/latest" {
			_, _ = w.Write([]byte(strings.Repeat("x", int(maxMetadataBytes)+1)))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := Client{HTTPClient: server.Client(), APIBaseURL: server.URL, Repository: "owner/repo", GOOS: "linux", GOARCH: "amd64", AllowInsecure: true}
	if _, err := client.InstallLatest(context.Background(), filepath.Join(t.TempDir(), "baron"), "0.0.9", false); err == nil || !strings.Contains(err.Error(), "safety limit") {
		t.Fatalf("expected bounded metadata error, got %v", err)
	}
}

func TestInstallLatestRejectsCandidateVersionBeforeMutation(t *testing.T) {
	candidate := []byte("#!/bin/sh\necho 'baron 0.0.9'\n")
	manifest := []byte(`{"project":"Baron Nexus","version":"0.1.0","artifacts":["baron-linux-amd64"]}`)
	sums := checksumLine(candidate, "baron-linux-amd64") + "\n" + checksumLine(manifest, "release-manifest.json") + "\n"
	server := releaseFixtureServer(t, candidate, manifest, []byte(sums))
	defer server.Close()
	root := t.TempDir()
	target := filepath.Join(root, "baron")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := Client{HTTPClient: server.Client(), APIBaseURL: server.URL, Repository: "owner/repo", GOOS: "linux", GOARCH: "amd64", AllowInsecure: true}
	if _, err := client.InstallLatest(context.Background(), target, "0.0.9", false); err == nil || !strings.Contains(err.Error(), "candidate") {
		t.Fatalf("expected candidate validation error, got %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old" {
		t.Fatalf("target mutated after candidate validation failure: %q", data)
	}
}

func releaseFixtureServer(t *testing.T, candidate, manifest, sums []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v0.1.0",
				"assets": []map[string]string{
					{"name": "baron-linux-amd64", "browser_download_url": "" + "http://" + r.Host + "/download/baron-linux-amd64"},
					{"name": "release-manifest.json", "browser_download_url": "http://" + r.Host + "/download/release-manifest.json"},
					{"name": "SHA256SUMS", "browser_download_url": "http://" + r.Host + "/download/SHA256SUMS"},
				},
			})
		case "/download/baron-linux-amd64":
			_, _ = w.Write(candidate)
		case "/download/release-manifest.json":
			_, _ = w.Write(manifest)
		case "/download/SHA256SUMS":
			_, _ = w.Write(sums)
		default:
			http.NotFound(w, r)
		}
	}))
}

func checksumLine(data []byte, name string) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%s  %s", hex.EncodeToString(sum[:]), name)
}
