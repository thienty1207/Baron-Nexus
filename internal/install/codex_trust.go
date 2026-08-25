package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// CodexHookApprovalInstruction is intentionally explicit. Codex does not
// expose a stable, non-interactive API that proves a project hook was
// approved, so Baron must tell the user exactly what remains to be confirmed.
const CodexHookApprovalInstruction = "open Codex CLI in this project once; if Codex asks to trust or enable project hooks, approve the Baron project hooks, then rerun `baron test`"

type CodexHookTrustState string

const (
	CodexHooksMissing        CodexHookTrustState = "missing"
	CodexHooksIncomplete     CodexHookTrustState = "incomplete"
	CodexHooksApprovalNeeded CodexHookTrustState = "approval_required"
)

type CodexHookInspection struct {
	Path             string
	State            CodexHookTrustState
	ConfiguredEvents int
	MissingEvents    []string
	ApprovalRequired bool
}

// CodexProjectTrusted reports whether Codex has persistently trusted the
// supplied project in its user configuration. This is deliberately separate
// from hooks.json inspection: the hook file proves Baron entries are present,
// while config.toml records the user's project trust decision.
func CodexProjectTrusted(projectRoot string) bool {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if projectRoot == "." || projectRoot == "" {
		return false
	}
	for _, path := range codexConfigPaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var config struct {
			Projects map[string]struct {
				TrustLevel string `toml:"trust_level"`
			} `toml:"projects"`
		}
		if err := toml.Unmarshal(data, &config); err != nil {
			continue
		}
		if project, ok := config.Projects[projectRoot]; ok && strings.EqualFold(strings.TrimSpace(project.TrustLevel), "trusted") {
			return true
		}
	}
	return false
}

func codexConfigPaths() []string {
	paths := make([]string, 0, 3)
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		paths = append(paths, filepath.Join(codexHome, "config.toml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".codex", "config.toml"))
	}
	if configDir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(configDir, "codex", "config.toml"))
	}
	return uniquePaths(paths)
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	return result
}

// InspectCodexHooks verifies the Baron-owned hook entries without claiming
// that a JSON config file proves an interactive Codex trust decision. It is
// safe to call from read-only doctor/test commands.
func InspectCodexHooks(path, binary string) (CodexHookInspection, error) {
	if strings.TrimSpace(path) == "" {
		return CodexHookInspection{State: CodexHooksMissing}, errors.New("Codex hooks path is required")
	}
	if binary == "" {
		binary = "baron"
	}
	inspection := CodexHookInspection{Path: path}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		inspection.State = CodexHooksMissing
		inspection.MissingEvents = append([]string(nil), codexEvents...)
		return inspection, nil
	}
	if err != nil {
		return inspection, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return inspection, errors.New("Codex hooks path is not a safe regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return inspection, err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return inspection, fmt.Errorf("decode Codex hooks: %w", err)
	}
	hooks, _ := root["hooks"].(map[string]any)
	for _, event := range codexEvents {
		if codexHookEventConfigured(hooks[event], binary+" hook codex "+event) {
			inspection.ConfiguredEvents++
			continue
		}
		inspection.MissingEvents = append(inspection.MissingEvents, event)
	}
	if len(inspection.MissingEvents) > 0 {
		inspection.State = CodexHooksIncomplete
		return inspection, nil
	}
	inspection.State = CodexHooksApprovalNeeded
	inspection.ApprovalRequired = true
	return inspection, nil
}

func codexHookEventConfigured(raw any, command string) bool {
	items, _ := raw.([]any)
	for _, item := range items {
		entry, _ := item.(map[string]any)
		if value, _ := entry["command"].(string); value == command {
			return true
		}
		nested, _ := entry["hooks"].([]any)
		for _, nestedRaw := range nested {
			nestedEntry, _ := nestedRaw.(map[string]any)
			if nestedEntry["type"] == "command" {
				if value, _ := nestedEntry["command"].(string); value == command {
					return true
				}
			}
		}
	}
	return false
}
