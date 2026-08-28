package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
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

func TestLoadingUIReportsLineLifecycleWhenOutputIsNotATerminal(t *testing.T) {
	var output bytes.Buffer
	ui := newProgressUI(&output, false)
	if err := ui.Run("Initializing DSH", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{"Initializing DSH...", "Initializing DSH complete."} {
		if !strings.Contains(got, want) {
			t.Fatalf("loading output missing %q:\n%s", want, got)
		}
	}
}

func TestLoadingUIUsesSpinnerAndReportsFailureOnInteractiveOutput(t *testing.T) {
	var output bytes.Buffer
	ui := newProgressUI(&output, true)
	wantErr := errors.New("operation failed")
	if err := ui.Run("Initializing Codex", func() error {
		time.Sleep(150 * time.Millisecond)
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("loading error=%v, want %v", err, wantErr)
	}
	got := output.String()
	if !strings.Contains(got, "Initializing Codex") || !strings.Contains(got, "failed.") || !strings.Contains(got, "\r") {
		t.Fatalf("interactive loading output is incomplete:\n%q", got)
	}
}

func TestLoadingUILeavesVisibleStartLineForFastInteractiveOperations(t *testing.T) {
	var output bytes.Buffer
	ui := newProgressUI(&output, true)
	if err := ui.Run("Updating Baron", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "[Baron] Updating Baron...\n") {
		t.Fatalf("fast interactive operation did not leave a visible start line:\n%q", got)
	}
}

func TestLoadingUIPreparesFreshLineForInteractiveInput(t *testing.T) {
	var output bytes.Buffer
	ui := newProgressUI(&output, true)
	if err := ui.Run("Running Baron install", func() error {
		ui.PrepareForInput()
		_, _ = fmt.Fprint(&output, "DeepSeek API key: ")
		time.Sleep(250 * time.Millisecond)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	if strings.Contains(got, "installDeepSeek API key") {
		t.Fatalf("interactive prompt was appended to the spinner line:\n%q", got)
	}
	if !strings.Contains(got, "\rDeepSeek API key: ") {
		t.Fatalf("interactive prompt did not start at a fresh cursor position:\n%q", got)
	}
	if gotCount := strings.Count(got, "DeepSeek API key: "); gotCount != 1 {
		t.Fatalf("spinner continued writing while input was active (%d prompt labels):\n%q", gotCount, got)
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
