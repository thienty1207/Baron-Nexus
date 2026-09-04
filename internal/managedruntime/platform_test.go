package managedruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type platformRunnerFixture struct {
	available map[string]bool
	outputs   map[string][]byte
}

func (f platformRunnerFixture) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := strings.TrimSpace(strings.Join(append([]string{name}, args...), " "))
	if !f.available[name] {
		return nil, errors.New("command unavailable")
	}
	output, ok := f.outputs[key]
	if !ok {
		return nil, errors.New("command failed")
	}
	return output, nil
}

func TestDetectPlatformReportsWindowsWSL2BridgeWithoutNativeStrix(t *testing.T) {
	report, err := detectPlatformFor(context.Background(), platformRunnerFixture{
		available: map[string]bool{"wsl": true, "docker": true},
		outputs: map[string][]byte{
			"wsl --status":         []byte("Default Version: 2\n"),
			"wsl --list --verbose": []byte("  NAME      STATE           VERSION\n* Ubuntu    Running         2\n"),
			"docker info":          []byte("server ready\n"),
			"wsl --distribution Ubuntu -- docker info": []byte("server ready inside WSL\n"),
		},
	}, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if report.OS != "windows" || !report.WSL2 || report.WSLDistro != "Ubuntu" || !report.Docker || !report.BridgeVerified || report.NativeStrix || report.Backend != "wsl2" {
		t.Fatalf("unexpected Windows bridge report: %#v", report)
	}
}

func TestDetectPlatformDoesNotTrustDefaultWSLVersionWithoutUbuntuDistro(t *testing.T) {
	report, err := detectPlatformFor(context.Background(), platformRunnerFixture{
		available: map[string]bool{"wsl": true, "docker": true},
		outputs: map[string][]byte{
			"wsl --status":         []byte("Default Version: 2\n"),
			"wsl --list --verbose": []byte("  NAME      STATE           VERSION\n  Debian    Stopped         2\n"),
			"docker info":          []byte("host docker ready\n"),
			"wsl --distribution Debian -- docker info": []byte("server ready inside WSL\n"),
		},
	}, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if report.WSL2 || report.BridgeVerified || report.Backend == "wsl2" {
		t.Fatalf("non-Ubuntu WSL distro was reported as verified bridge: %#v", report)
	}
}

func TestDetectPlatformDoesNotTrustHostDockerForWSLBridge(t *testing.T) {
	report, err := detectPlatformFor(context.Background(), platformRunnerFixture{
		available: map[string]bool{"wsl": true, "docker": true},
		outputs: map[string][]byte{
			"wsl --status":         []byte("Default Version: 2\n"),
			"wsl --list --verbose": []byte("  NAME      STATE           VERSION\n* Ubuntu    Running         2\n"),
			"docker info":          []byte("host docker ready\n"),
		},
	}, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if report.BridgeVerified || report.Backend == "wsl2" {
		t.Fatalf("host Docker was incorrectly treated as WSL bridge: %#v", report)
	}
}

func TestFindUbuntuWSL2DistroParsesWindowsUnicodePipeOutput(t *testing.T) {
	output := " \x00 \x00N\x00A\x00M\x00E\x00       \x00S\x00T\x00A\x00T\x00E\x00           \x00V\x00E\x00R\x00S\x00I\x00O\x00N\x00\r\x00\n\x00*\x00 Ubuntu   \x00Running        \x002\x00\r\x00\n\x00"
	if got := findUbuntuWSL2Distro(output); got != "Ubuntu" {
		t.Fatalf("parsed WSL distro=%q, want Ubuntu", got)
	}
}

func TestDetectPlatformReportsUnavailableBackendTruthfully(t *testing.T) {
	report, err := detectPlatformFor(context.Background(), platformRunnerFixture{available: map[string]bool{}, outputs: map[string][]byte{}}, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if report.Docker || report.WSL2 || report.NativeStrix || report.Backend != "unavailable" {
		t.Fatalf("unavailable platform was reported as ready: %#v", report)
	}
}
