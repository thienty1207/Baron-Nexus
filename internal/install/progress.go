package install

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

const (
	progressByteStep    int64 = 1 << 20
	progressPercentStep int64 = 5
)

// ProgressReporter receives human-readable bootstrap and download updates.
// Implementations must not include credentials or command output in events.
type ProgressReporter interface {
	Step(label string)
	Download(label string, current, total int64)
}

type JobProgress struct {
	JobID   string
	Phase   string
	Current int64
	Total   int64
	Detail  string
}

func RenderPentestProgress(reporter ProgressReporter, progress JobProgress) {
	if reporter == nil {
		return
	}
	jobID := strings.TrimSpace(progress.JobID)
	phase := strings.TrimSpace(progress.Phase)
	if jobID == "" {
		jobID = "unknown"
	}
	if phase == "" {
		phase = "working"
	}
	label := "Pentest " + jobID + ": " + phase
	if detail := strings.TrimSpace(progress.Detail); detail != "" {
		label += " - " + detail
	}
	if progress.Total > 0 {
		reporter.Download(label, progress.Current, progress.Total)
		return
	}
	reporter.Step(label)
}

type ProgressUI struct {
	writer       io.Writer
	mu           sync.Mutex
	interactive  bool
	spinner      bool
	spinnerText  string
	spinnerFrame int
}

// NewProgressUI creates the shared progress and operation renderer. A nil
// writer disables progress without changing the install behavior.
func NewProgressUI(writer io.Writer) *ProgressUI {
	if writer == nil {
		return nil
	}
	return newProgressUI(writer, writerIsTerminal(writer))
}

// NewProgressReporter keeps the existing reporter API for package callers.
// The returned UI also supports Run for lifecycle loading messages.
func NewProgressReporter(writer io.Writer) ProgressReporter {
	if writer == nil {
		return nil
	}
	return NewProgressUI(writer)
}

func newProgressUI(writer io.Writer, interactive bool) *ProgressUI {
	if writer == nil {
		return nil
	}
	return &ProgressUI{writer: writer, interactive: interactive}
}

func writerIsTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

func firstProgressReporter(reporters ...ProgressReporter) ProgressReporter {
	if len(reporters) == 0 {
		return nil
	}
	return reporters[0]
}

func (p *ProgressUI) Step(label string) {
	label = strings.TrimSpace(label)
	if label == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearSpinnerLocked()
	_, _ = fmt.Fprintf(p.writer, "[Baron] %s\n", label)
	p.renderSpinnerLocked()
}

func (p *ProgressUI) Download(label string, current, total int64) {
	label = strings.TrimSpace(label)
	if label == "" {
		return
	}
	if current < 0 {
		current = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearSpinnerLocked()
	if total > 0 {
		if current > total {
			current = total
		}
		percent := current * 100 / total
		_, _ = fmt.Fprintf(p.writer, "[Baron]   %s: %s/%s (%d%%)\n", label, formatProgressBytes(current), formatProgressBytes(total), percent)
		p.renderSpinnerLocked()
		return
	}
	_, _ = fmt.Fprintf(p.writer, "[Baron]   %s: %s downloaded\n", label, formatProgressBytes(current))
	p.renderSpinnerLocked()
}

// Run renders one operation lifecycle. Interactive terminals get a lightweight
// ASCII spinner; redirected output gets stable line-oriented messages.
func (p *ProgressUI) Run(label string, action func() error) error {
	if p == nil || action == nil {
		if action == nil {
			return nil
		}
		return action()
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "Baron operation"
	}
	if !p.interactive {
		p.Step(label + "...")
		err := action()
		if err != nil {
			p.Step(label + " failed.")
			return err
		}
		p.Step(label + " complete.")
		return nil
	}

	// Keep a durable start line so fast operations are visible even when the
	// spinner is immediately replaced by its completion line.
	frames := []string{"-", "\\", "|", "/"}
	p.mu.Lock()
	p.spinner = true
	p.spinnerText = fmt.Sprintf("[Baron] %s %s", frames[0], label)
	p.spinnerFrame = 0
	_, _ = fmt.Fprintf(p.writer, "[Baron] %s...\n", label)
	_, _ = fmt.Fprintf(p.writer, "\r%s", p.spinnerText)
	p.mu.Unlock()
	// Run the operation separately so the spinner remains responsive while
	// package managers or network calls block.
	done := make(chan error, 1)
	go func() { done <- action() }()
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			p.mu.Lock()
			p.clearSpinnerLocked()
			p.spinner = false
			if err != nil {
				_, _ = fmt.Fprintf(p.writer, "\r[Baron] ! %s failed.\n", label)
			} else {
				_, _ = fmt.Fprintf(p.writer, "\r[Baron] + %s complete.\n", label)
			}
			p.mu.Unlock()
			return err
		case <-ticker.C:
			p.mu.Lock()
			if p.spinner {
				p.spinnerFrame = (p.spinnerFrame + 1) % len(frames)
				p.spinnerText = fmt.Sprintf("[Baron] %s %s", frames[p.spinnerFrame], label)
				_, _ = fmt.Fprintf(p.writer, "\r%s", p.spinnerText)
			}
			p.mu.Unlock()
		}
	}
}

// PrepareForInput clears the live spinner and leaves the cursor at the start
// of its line before an interactive prompt writes to the terminal.
func (p *ProgressUI) PrepareForInput() {
	if p == nil || !p.interactive {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.spinner || p.spinnerText == "" {
		return
	}
	p.clearSpinnerLocked()
	p.spinner = false
}

func (p *ProgressUI) clearSpinnerLocked() {
	if !p.spinner || p.spinnerText == "" {
		return
	}
	_, _ = fmt.Fprintf(p.writer, "\r%s\r", strings.Repeat(" ", len(p.spinnerText)))
}

func (p *ProgressUI) renderSpinnerLocked() {
	if !p.spinner || p.spinnerText == "" {
		return
	}
	_, _ = fmt.Fprintf(p.writer, "\r%s", p.spinnerText)
}

func formatProgressBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB"}
	scaled := float64(value)
	for _, unit := range units {
		scaled /= 1024
		if scaled < 1024 || unit == units[len(units)-1] {
			return fmt.Sprintf("%.1f %s", scaled, unit)
		}
	}
	return fmt.Sprintf("%d B", value)
}

// NewProgressReader wraps a download stream and emits bounded progress
// updates. It is exported so release verification can use the same renderer.
func NewProgressReader(source io.Reader, reporter ProgressReporter, label string, total int64) io.Reader {
	if source == nil || reporter == nil {
		return source
	}
	return &progressReader{
		source:      source,
		reporter:    reporter,
		label:       label,
		total:       total,
		lastBytes:   0,
		lastPercent: -progressPercentStep,
	}
}

type progressReader struct {
	source      io.Reader
	reporter    ProgressReporter
	label       string
	total       int64
	current     int64
	lastBytes   int64
	lastPercent int64
}

func (r *progressReader) Read(buffer []byte) (int, error) {
	read, err := r.source.Read(buffer)
	if read > 0 {
		r.current += int64(read)
		r.report(false)
	}
	if err == io.EOF {
		r.report(true)
	}
	return read, err
}

func (r *progressReader) report(force bool) {
	if r.total > 0 {
		percent := r.current * 100 / r.total
		if r.current >= r.total {
			percent = 100
		}
		if percent == r.lastPercent {
			return
		}
		if !force && percent < r.lastPercent+progressPercentStep {
			return
		}
		r.reporter.Download(r.label, r.current, r.total)
		r.lastPercent = percent
		return
	}
	if r.current == r.lastBytes {
		return
	}
	if !force && r.current-r.lastBytes < progressByteStep {
		return
	}
	r.reporter.Download(r.label, r.current, r.total)
	r.lastBytes = r.current
}
