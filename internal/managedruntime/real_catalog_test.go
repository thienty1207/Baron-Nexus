package managedruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRealCatalogStagesBundle is opt-in because it downloads the complete
// release bundle. It is the release maintainer's live archive/package smoke,
// not a network dependency for ordinary unit-test runs.
func TestRealCatalogStagesBundle(t *testing.T) {
	if os.Getenv("BARON_REAL_CATALOG_TEST") != "1" {
		t.Skip("set BARON_REAL_CATALOG_TEST=1 to download and stage the real bundle")
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "configs", "managed-runtime-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalogURL := "https://catalog.local/managed-runtime-catalog.json"
	resolver := Resolver{
		Metadata: &catalogFixtureClient{responses: map[string][]byte{catalogURL: data}},
		Platform: runtime.GOOS, Architecture: runtime.GOARCH,
		TestedMatrix: CompatibilityMatrix{MinPythonMajor: 3, MinPythonMinor: 12, MaxPythonMajor: 3, MaxPythonMinor: 14},
		CatalogURLs:  make(map[ComponentID]string, len(RequiredBundleComponents())),
	}
	for _, component := range RequiredBundleComponents() {
		resolver.CatalogURLs[component] = catalogURL
	}
	plan, err := resolver.Resolve(context.Background(), ResolverInput{
		Platform: runtime.GOOS, Architecture: runtime.GOARCH,
		Components: RequiredBundleComponents(), CompatibilityVersion: "real-catalog-test",
		RequireCompleteBundle: true,
	})
	if err != nil {
		t.Fatalf("resolve real bundle catalog: %v", err)
	}
	paths, err := ResolvePaths(filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	assetDir := os.Getenv("BARON_REAL_CATALOG_ASSETS_DIR")
	if strings.TrimSpace(assetDir) == "" {
		t.Fatal("BARON_REAL_CATALOG_ASSETS_DIR must point to the predownloaded, hash-verified assets")
	}
	downloader, err := newRealCatalogDownloader(assetDir)
	if err != nil {
		t.Fatal(err)
	}
	manager := Manager{
		Paths: paths, Downloader: downloader,
		Probe: NativeProbe{}, Installer: NativeComponentInstaller{}, EnableLaunchers: true, MaxDownloadBytes: 8 << 30,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	report, err := manager.Apply(ctx, plan)
	if err != nil {
		t.Fatalf("stage real managed runtime bundle: %v", err)
	}
	if len(report.Receipts) != len(RequiredBundleComponents()) {
		t.Fatalf("staged receipts=%d, want %d", len(report.Receipts), len(RequiredBundleComponents()))
	}
	if err := manager.Verify(ctx, report.Generation); err != nil {
		t.Fatalf("verify real managed runtime bundle: %v", err)
	}
}

type realCatalogDownloader struct {
	byHash map[string]string
}

func newRealCatalogDownloader(directory string) (realCatalogDownloader, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return realCatalogDownloader{}, fmt.Errorf("read real catalog asset directory: %w", err)
	}
	result := realCatalogDownloader{byHash: make(map[string]string, len(entries))}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		hash, _, hashErr := fileSHA256(path, managerDefaultMaxDownloadBytes)
		if hashErr != nil {
			continue
		}
		result.byHash[strings.ToLower(hash)] = path
	}
	if len(result.byHash) == 0 {
		return realCatalogDownloader{}, errors.New("real catalog asset directory contains no readable files")
	}
	return result, nil
}

func (d realCatalogDownloader) Download(ctx context.Context, asset Asset, destination io.Writer, reporter ProgressReporter) (DownloadReceipt, error) {
	if err := ctx.Err(); err != nil {
		return DownloadReceipt{}, err
	}
	path, ok := d.byHash[strings.ToLower(asset.SHA256)]
	if !ok {
		return DownloadReceipt{}, fmt.Errorf("verified local asset is missing for %s", asset.SHA256)
	}
	file, err := os.Open(path)
	if err != nil {
		return DownloadReceipt{}, err
	}
	defer file.Close()
	bytes, err := io.Copy(destination, file)
	if err != nil {
		return DownloadReceipt{}, err
	}
	if reporter != nil {
		reporter.Download("managed runtime", bytes, bytes)
	}
	return DownloadReceipt{Bytes: bytes, SHA256: strings.ToLower(asset.SHA256)}, nil
}

var _ Downloader = realCatalogDownloader{}
