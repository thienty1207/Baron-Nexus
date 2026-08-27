package install

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestTerminalProgressReportsStepsAndDownloadProgress(t *testing.T) {
	var output bytes.Buffer
	reporter := NewProgressReporter(&output)

	reporter.Step("sudo authorization accepted")
	reporter.Download("uv archive", 12*1024*1024, 20*1024*1024)

	got := output.String()
	for _, want := range []string{
		"[Baron] sudo authorization accepted",
		"uv archive",
		"12.0 MiB/20.0 MiB",
		"60%",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("progress output missing %q:\n%s", want, got)
		}
	}
}

func TestProgressReaderDoesNotDuplicateFinalUpdate(t *testing.T) {
	var output bytes.Buffer
	reporter := NewProgressReporter(&output)
	reader := NewProgressReader(&finalChunkReader{data: bytes.Repeat([]byte("x"), 2*1024*1024)}, reporter, "archive", -1)
	if _, err := io.Copy(io.Discard, reader); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(output.String(), "archive:"); got != 1 {
		t.Fatalf("final progress updates=%d, want 1:\n%s", got, output.String())
	}
}

type finalChunkReader struct {
	data []byte
	done bool
}

func (r *finalChunkReader) Read(buffer []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(buffer, r.data), io.EOF
}
