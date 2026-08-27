package install

import (
	"fmt"
	"io"
	"strings"
	"sync"
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

type terminalProgress struct {
	writer io.Writer
	mu     sync.Mutex
}

// NewProgressReporter creates the plain-text reporter used by CLI commands.
// A nil writer disables progress without changing the install behavior.
func NewProgressReporter(writer io.Writer) ProgressReporter {
	if writer == nil {
		return nil
	}
	return &terminalProgress{writer: writer}
}

func firstProgressReporter(reporters ...ProgressReporter) ProgressReporter {
	if len(reporters) == 0 {
		return nil
	}
	return reporters[0]
}

func (p *terminalProgress) Step(label string) {
	label = strings.TrimSpace(label)
	if label == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _ = fmt.Fprintf(p.writer, "[Baron] %s\n", label)
}

func (p *terminalProgress) Download(label string, current, total int64) {
	label = strings.TrimSpace(label)
	if label == "" {
		return
	}
	if current < 0 {
		current = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if total > 0 {
		if current > total {
			current = total
		}
		percent := current * 100 / total
		_, _ = fmt.Fprintf(p.writer, "[Baron]   %s: %s/%s (%d%%)\n", label, formatProgressBytes(current), formatProgressBytes(total), percent)
		return
	}
	_, _ = fmt.Fprintf(p.writer, "[Baron]   %s: %s downloaded\n", label, formatProgressBytes(current))
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
