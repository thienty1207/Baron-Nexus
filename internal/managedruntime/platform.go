package managedruntime

import (
	"context"
	"encoding/binary"
	"errors"
	"runtime"
	"strings"
	"unicode/utf16"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type PlatformReport struct {
	OS             string
	Architecture   string
	Backend        string
	WSL2           bool
	WSLDistro      string
	Docker         bool
	BridgeVerified bool
	NativeStrix    bool
	Message        string
}

func DetectPlatform(ctx context.Context, runner CommandRunner) (PlatformReport, error) {
	return detectPlatformFor(ctx, runner, runtime.GOOS, runtime.GOARCH)
}

// DetectPlatformFor is the injectable form used by host bootstrap code that
// needs to inspect a target platform without pretending the current process
// runs on that platform. Production callers should normally use DetectPlatform.
func DetectPlatformFor(ctx context.Context, runner CommandRunner, goos, architecture string) (PlatformReport, error) {
	return detectPlatformFor(ctx, runner, goos, architecture)
}

func detectPlatformFor(ctx context.Context, runner CommandRunner, goos, architecture string) (PlatformReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return PlatformReport{}, err
	}
	if runner == nil {
		return PlatformReport{}, errors.New("platform command runner is not configured")
	}
	goos = strings.TrimSpace(strings.ToLower(goos))
	if goos == "" {
		goos = runtime.GOOS
	}
	architecture = strings.TrimSpace(strings.ToLower(architecture))
	if architecture == "" {
		architecture = runtime.GOARCH
	}
	report := PlatformReport{OS: goos, Architecture: architecture, Backend: "unavailable"}
	if _, err := runner.Run(ctx, "docker", "info"); err == nil {
		report.Docker = true
	}
	switch goos {
	case "windows":
		_, statusErr := runner.Run(ctx, "wsl", "--status")
		if statusErr == nil {
			if output, err := runner.Run(ctx, "wsl", "--list", "--verbose"); err == nil {
				report.WSLDistro = findUbuntuWSL2Distro(string(output))
				report.WSL2 = report.WSLDistro != ""
			}
		}
		if report.WSL2 {
			if _, err := runner.Run(ctx, "wsl", "--distribution", report.WSLDistro, "--", "docker", "info"); err == nil {
				report.BridgeVerified = true
				report.Docker = true
			}
		}
		switch {
		case report.BridgeVerified:
			report.Backend = "wsl2"
		case report.Docker:
			report.Backend = "docker"
		case report.WSL2:
			report.Backend = "wsl2-unverified"
		}
		report.NativeStrix = false
		switch {
		case report.BridgeVerified:
			report.Message = "Verified Ubuntu WSL2 + Docker bridge is available; native Windows Strix remains disabled."
		case report.WSL2:
			report.Message = "Ubuntu WSL2 is present, but Docker inside that distro could not be verified; the Strix bridge is not ready."
		case report.Docker:
			report.Message = "Docker is reachable, but a verified Ubuntu WSL2 + Docker bridge is required for Strix on Windows."
		}
	case "linux":
		report.Backend = "linux"
		report.NativeStrix = true
		report.BridgeVerified = report.Docker
		if report.Docker {
			report.Message = "Linux native Strix runtime is available; Docker is also reachable."
		} else {
			report.Message = "Linux native Strix runtime is available; Docker backend is unavailable."
		}
	default:
		report.Backend = "unsupported"
		report.Message = "This operating system is outside the managed Strix support boundary."
	}
	if report.Backend == "unavailable" {
		report.Message = "No managed Strix backend is ready; install WSL2 or Docker and rerun Baron."
	}
	return report, nil
}

func findUbuntuWSL2Distro(output string) string {
	output = normalizeWSLText(output)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[len(fields)-1] != "2" {
			continue
		}
		name := fields[0]
		if name == "*" && len(fields) >= 4 {
			name = fields[1]
		}
		name = strings.TrimPrefix(name, "*")
		name = strings.TrimSpace(name)
		if strings.HasPrefix(strings.ToLower(name), "ubuntu") {
			return name
		}
	}
	return ""
}

func normalizeWSLText(output string) string {
	data := []byte(output)
	if len(data) >= 2 && len(data)%2 == 0 {
		offset := 0
		if data[0] == 0xff && data[1] == 0xfe {
			offset = 2
		}
		if len(data)-offset >= 2 {
			units := make([]uint16, 0, (len(data)-offset)/2)
			zeroHighBytes := 0
			for index := offset; index+1 < len(data); index += 2 {
				if data[index+1] == 0 {
					zeroHighBytes++
				}
				units = append(units, binary.LittleEndian.Uint16(data[index:index+2]))
			}
			if len(units) > 0 && zeroHighBytes*2 >= len(units) {
				return strings.TrimPrefix(string(utf16.Decode(units)), "\ufeff")
			}
		}
	}
	return strings.ReplaceAll(strings.TrimPrefix(output, "\ufeff"), "\x00", "")
}
