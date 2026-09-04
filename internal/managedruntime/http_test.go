package managedruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type httpProgressFixture struct {
	steps     []string
	downloads int
}

func (p *httpProgressFixture) Step(label string)             { p.steps = append(p.steps, label) }
func (p *httpProgressFixture) Download(string, int64, int64) { p.downloads++ }

func TestHTTPDownloaderStreamsHashesAndReportsProgress(t *testing.T) {
	payload := []byte(strings.Repeat("managed-runtime", 1024))
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	var destination bytes.Buffer
	progress := &httpProgressFixture{}
	receipt, err := (HTTPDownloader{HTTP: server.Client(), AllowInsecure: true}).Download(
		context.Background(),
		Asset{URL: server.URL + "/runtime.bin", SHA256: hex.EncodeToString(sum[:]), Platform: "windows", Architecture: "amd64"},
		&destination, progress,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(destination.Bytes(), payload) || receipt.Bytes != int64(len(payload)) || receipt.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("download destination=%d receipt=%#v", destination.Len(), receipt)
	}
	if progress.downloads == 0 {
		t.Fatal("downloader did not report progress")
	}
}

func TestHTTPDownloaderRejectsInsecureURLByDefault(t *testing.T) {
	_, err := (HTTPDownloader{}).Download(context.Background(), Asset{URL: "http://example.invalid/runtime.bin"}, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("insecure download error=%v", err)
	}
}
