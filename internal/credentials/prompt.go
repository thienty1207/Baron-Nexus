package credentials

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrNonInteractive means a credential was required but the process was not
// attached to a terminal. Callers should surface the required environment
// variable instead of waiting for input that can never arrive.
var ErrNonInteractive = errors.New("credential prompt requires an interactive terminal")

// ErrEmptyValue means the user submitted an empty required credential.
var ErrEmptyValue = errors.New("credential value cannot be empty")

// SecretReader is injectable so credential orchestration can be tested
// without ever placing a real secret in a test process or its output.
type SecretReader func(io.Reader) ([]byte, error)

// LineReader is injectable so ordinary prompt handling can be tested without
// depending on a terminal device.
type LineReader func(io.Reader) (string, error)

// Prompter owns all interactive credential input. Prompts are written to Out
// (normally stderr), keeping machine-readable command output on stdout clean.
// ReadSecret and ReadLine are test seams; production callers should leave them
// nil so terminal capability and hidden input are enforced here.
type Prompter struct {
	In         io.Reader
	Out        io.Writer
	ReadSecret SecretReader
	ReadLine   LineReader

	lineReader *bufio.Reader
}

// Secret prompts for a hidden value and trims surrounding whitespace. The
// value is never written by this package.
func (p *Prompter) Secret(label string) (string, error) {
	in, out := p.io()
	if err := p.writePrompt(out, label, ""); err != nil {
		return "", err
	}

	var raw []byte
	var err error
	if p.ReadSecret != nil {
		raw, err = p.ReadSecret(in)
	} else {
		file, ok := terminalFile(in)
		if !ok {
			return "", ErrNonInteractive
		}
		raw, err = term.ReadPassword(int(file.Fd()))
		if writeErr := writeNewline(out); err == nil {
			err = writeErr
		}
	}
	if err != nil {
		return "", fmt.Errorf("read credential: %w", err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", ErrEmptyValue
	}
	return value, nil
}

// Value prompts for a visible non-secret value. An empty response accepts the
// supplied default when one is present.
func (p *Prompter) Value(label, defaultValue string) (string, error) {
	in, out := p.io()
	if err := p.writePrompt(out, label, defaultValue); err != nil {
		return "", err
	}

	var raw string
	var err error
	if p.ReadLine != nil {
		raw, err = p.ReadLine(in)
	} else {
		if _, ok := terminalFile(in); !ok {
			return "", ErrNonInteractive
		}
		if p.lineReader == nil {
			p.lineReader = bufio.NewReader(in)
		}
		raw, err = p.lineReader.ReadString('\n')
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read value: %w", err)
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		value = strings.TrimSpace(defaultValue)
	}
	if value == "" {
		return "", ErrEmptyValue
	}
	return value, nil
}

func (p *Prompter) io() (io.Reader, io.Writer) {
	in := p.In
	if in == nil {
		in = os.Stdin
	}
	out := p.Out
	if out == nil {
		out = os.Stderr
	}
	return in, out
}

func (p *Prompter) writePrompt(out io.Writer, label, defaultValue string) error {
	if defaultValue != "" {
		// Value defaults are intentionally visible; Secret never calls this
		// branch, so a credential cannot be echoed by prompt formatting.
		_, err := fmt.Fprintf(out, "%s [%s]: ", label, defaultValue)
		return err
	}
	_, err := fmt.Fprintf(out, "%s: ", label)
	return err
}

func writeNewline(out io.Writer) error {
	_, err := io.WriteString(out, "\n")
	return err
}

func terminalFile(reader io.Reader) (*os.File, bool) {
	file, ok := reader.(*os.File)
	if !ok || file == nil {
		return nil, false
	}
	fd := int(file.Fd())
	return file, term.IsTerminal(fd)
}
