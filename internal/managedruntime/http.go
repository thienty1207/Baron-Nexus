package managedruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultHTTPDownloadBytes int64 = 8 << 30

// HTTPDownloader streams a verified catalog asset to the manager's staging
// file. It never buffers a runtime archive in memory and only allows HTTP in
// explicitly injected test configurations.
type HTTPDownloader struct {
	HTTP          *http.Client
	AllowInsecure bool
	MaxBytes      int64
}

func (d HTTPDownloader) Download(ctx context.Context, asset Asset, destination io.Writer, reporter ProgressReporter) (DownloadReceipt, error) {
	if destination == nil {
		return DownloadReceipt{}, errors.New("managed runtime download destination is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(asset.URL))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return DownloadReceipt{}, errors.New("managed runtime asset URL is invalid")
	}
	if parsed.Scheme != "https" && !(d.AllowInsecure && parsed.Scheme == "http") {
		return DownloadReceipt{}, errors.New("managed runtime downloads require HTTPS")
	}
	maxBytes := d.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultHTTPDownloadBytes
	}
	client := d.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return DownloadReceipt{}, fmt.Errorf("create managed runtime download request: %w", err)
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "Baron-Nexus-managed-runtime")
	response, err := client.Do(request)
	if err != nil {
		return DownloadReceipt{}, fmt.Errorf("download managed runtime asset: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return DownloadReceipt{}, fmt.Errorf("download managed runtime asset returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		return DownloadReceipt{}, errors.New("managed runtime asset exceeds the size limit")
	}

	hash := sha256.New()
	reader := io.TeeReader(response.Body, hash)
	buffer := make([]byte, 64*1024)
	var written int64
	lastReport := int64(-1)
	for {
		read, readErr := reader.Read(buffer)
		if read > 0 {
			if written > maxBytes-int64(read) {
				return DownloadReceipt{}, errors.New("managed runtime asset exceeds the size limit")
			}
			count, writeErr := destination.Write(buffer[:read])
			if writeErr != nil {
				return DownloadReceipt{}, fmt.Errorf("write managed runtime asset: %w", writeErr)
			}
			if count != read {
				return DownloadReceipt{}, io.ErrShortWrite
			}
			written += int64(read)
			if reporter != nil && (response.ContentLength > 0 || written-lastReport >= 1<<20) {
				reporter.Download("managed runtime", written, response.ContentLength)
				lastReport = written
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return DownloadReceipt{}, fmt.Errorf("read managed runtime asset: %w", readErr)
		}
	}
	if reporter != nil && written != lastReport {
		reporter.Download("managed runtime", written, response.ContentLength)
	}
	return DownloadReceipt{Bytes: written, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}
