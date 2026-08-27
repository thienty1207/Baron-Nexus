package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
)

var codexEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PreCompact", "PostCompact", "Stop", "SessionEnd"}

const codexHookTimeoutSeconds = 3

const baronDSHProfilePatchBlock = `# baron-owned: ddg-search
- insert:
    - id: baron-ddg-search
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: 'ddg-search'
        transport: 'stdio'
        command: 'uvx'
        args: ['duckduckgo-mcp-server']
`

func MergeCodexHooks(path, binary string) error {
	if binary == "" {
		binary = "baron"
	}
	root, err := readJSONMap(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		if err := backupBeforeEdit(path); err != nil {
			return err
		}
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	for _, event := range codexEvents {
		items, _ := hooks[event].([]any)
		command := binary + " hook codex " + event
		found := false
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if value, _ := entry["command"].(string); value == command {
				entry["timeout"] = codexHookTimeoutSeconds
				found = true
				break
			}
			commands, _ := entry["hooks"].([]any)
			for _, commandRaw := range commands {
				commandEntry, ok := commandRaw.(map[string]any)
				if ok && commandEntry["type"] == "command" && commandEntry["command"] == command {
					commandEntry["timeout"] = codexHookTimeoutSeconds
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			items = append(items, map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": command, "timeout": codexHookTimeoutSeconds,
				"statusMessage": "Baron project continuity",
			}}})
		}
		hooks[event] = items
	}
	return writeJSONMap(path, root, 0o600)
}

// RemoveCodexHooks removes only Baron lifecycle hook entries and preserves
// unrelated user hooks. It is safe to call repeatedly after the file is gone.
func RemoveCodexHooks(path, binary string) (bool, error) {
	if path == "" {
		return false, errors.New("Codex hooks path is required")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, errors.New("refusing to edit Codex hooks through a symlink or non-regular file")
	}
	if binary == "" {
		binary = "baron"
	}
	root, err := readJSONMap(path)
	if err != nil {
		return false, err
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return false, nil
	}
	changed := false
	for event, raw := range hooks {
		items, ok := raw.([]any)
		if !ok {
			continue
		}
		filtered := make([]any, 0, len(items))
		for _, item := range items {
			kept, remove, itemChanged := removeBaronHookEntry(item, binary)
			if remove {
				changed = true
				continue
			}
			if itemChanged {
				changed = true
			}
			filtered = append(filtered, kept)
		}
		if len(filtered) == 0 {
			delete(hooks, event)
			if len(items) > 0 {
				changed = true
			}
		} else {
			hooks[event] = filtered
		}
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}
	if !changed {
		return false, nil
	}
	if len(root) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		return true, nil
	}
	if err := writeJSONMap(path, root, info.Mode().Perm()); err != nil {
		return false, err
	}
	return true, nil
}

func removeBaronHookEntry(raw any, binary string) (any, bool, bool) {
	entry, ok := raw.(map[string]any)
	if !ok {
		return raw, false, false
	}
	if command, ok := entry["command"].(string); ok && isBaronCodexHook(command, binary) {
		return nil, true, false
	}
	nested, hasNested := entry["hooks"].([]any)
	if !hasNested {
		return raw, false, false
	}
	filtered := make([]any, 0, len(nested))
	changed := false
	for _, item := range nested {
		commandEntry, ok := item.(map[string]any)
		if ok {
			if command, commandOK := commandEntry["command"].(string); commandOK && isBaronCodexHook(command, binary) {
				changed = true
				continue
			}
		}
		filtered = append(filtered, item)
	}
	if !changed {
		return raw, false, false
	}
	if len(filtered) > 0 {
		entry["hooks"] = filtered
		return entry, false, true
	}
	delete(entry, "hooks")
	for key := range entry {
		if key != "statusMessage" && key != "timeout" {
			return entry, false, true
		}
	}
	return nil, true, false
}

func isBaronCodexHook(command, binary string) bool {
	return strings.HasPrefix(strings.TrimSpace(command), strings.TrimSpace(binary)+" hook codex ")
}

type DSHOptions struct {
	AdapterCommand string
	Version        string
}

type DSHReport struct {
	Components     []string
	ActionRequired string
}

// DSHProfilePatchPath returns the current profile-owned patch location used by
// DSH's profile bundle system. DSH_HOME remains user-owned; Baron only edits
// its own marked block in the selected profile.
func DSHProfilePatchPath(profile string) (string, error) {
	if profile == "" {
		profile = "web"
	}
	home := os.Getenv("DSH_HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(home, ".dsh")
	}
	return filepath.Join(home, "profiles", profile, "cordis.patch.yml"), nil
}

// EnsureDSHProfilePatch registers the mandatory free DuckDuckGo MCP through
// DSH's official @deepseek-ai/dsh-mcp-client patch shape. It preserves all
// unrelated YAML and recognizes an existing user-owned ddg-search entry.
func EnsureDSHProfilePatch(path string) error {
	if path == "" {
		return errors.New("DSH profile patch path is required")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("refusing to edit DSH profile patch through a symlink or non-regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	text := string(data)
	if strings.Contains(text, "baron-owned: ddg-search") || strings.Contains(text, "serverName: 'ddg-search'") || strings.Contains(text, "serverName: \"ddg-search\"") {
		return nil
	}
	if _, statErr := os.Stat(path); statErr == nil {
		if err := backupBeforeEdit(path); err != nil {
			return err
		}
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	// A freshly-created DSH profile uses a valid empty YAML sequence (`[]`)
	// after its explanatory header. Appending a second sequence would create an
	// invalid YAML document, so replace only that empty-list marker while
	// preserving the user/header comments and any existing patch rows.
	text = replaceEmptyDSHPatchSequence(text)
	text += baronDSHProfilePatchBlock
	perm := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		perm = info.Mode().Perm()
	}
	return config.AtomicWriteFile(path, []byte(text), perm)
}

// RemoveDSHProfilePatch removes the Baron-owned marker block while preserving
// all user-authored profile patch content.
func RemoveDSHProfilePatch(path string) (bool, error) {
	if path == "" {
		return false, errors.New("DSH profile patch path is required")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, errors.New("refusing to edit DSH profile patch through a symlink or non-regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	lines := strings.SplitAfter(string(data), "\n")
	expected := strings.Split(strings.TrimSuffix(baronDSHProfilePatchBlock, "\n"), "\n")
	start := -1
	for index, line := range lines {
		if strings.TrimSpace(line) != "# baron-owned: ddg-search" {
			continue
		}
		if index+len(expected) > len(lines) {
			continue
		}
		matches := true
		for offset, expectedLine := range expected {
			if strings.TrimSpace(lines[index+offset]) != strings.TrimSpace(expectedLine) {
				matches = false
				break
			}
		}
		if matches {
			start = index
			break
		}
	}
	if start < 0 {
		return false, nil
	}
	remaining := append([]string(nil), lines[:start]...)
	remaining = append(remaining, lines[start+len(expected):]...)
	updated := strings.Join(remaining, "")
	if strings.TrimSpace(updated) == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		return true, nil
	}
	if err := config.AtomicWriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return false, err
	}
	return true, nil
}

func replaceEmptyDSHPatchSequence(text string) string {
	lines := strings.Split(text, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.TrimSpace(lines[index]) == "" {
			continue
		}
		if strings.TrimSpace(lines[index]) == "[]" {
			lines = append(lines[:index], lines[index+1:]...)
			return strings.Join(lines, "\n")
		}
		break
	}
	return text
}

func EnsureDSHBaseline(path string, options DSHOptions) (DSHReport, error) {
	if options.AdapterCommand == "" {
		options.AdapterCommand = "baron hook dsh"
	}
	if options.Version == "" {
		options.Version = LatestDependencySelector
	}
	root, err := readJSONMap(path)
	if err != nil {
		return DSHReport{}, err
	}
	if _, err := os.Stat(path); err == nil {
		if err := backupBeforeEdit(path); err != nil {
			return DSHReport{}, err
		}
	}
	plugins, _ := root["plugins"].([]any)
	for _, name := range []string{"dsh-superpowers", "dsh-reverse-skill", "baron-dsh-adapter"} {
		if !containsString(plugins, name) {
			plugins = append(plugins, name)
		}
	}
	root["plugins"] = plugins
	mcp, _ := root["mcpServers"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
	}
	if _, exists := mcp["ddg-search"]; !exists {
		mcp["ddg-search"] = map[string]any{"command": "uvx", "args": []string{"duckduckgo-mcp-server"}, "baron_owned": true}
	}
	root["mcpServers"] = mcp
	baronFragment, _ := root["baron"].(map[string]any)
	if baronFragment == nil {
		baronFragment = map[string]any{}
	}
	baronFragment["adapter_command"] = options.AdapterCommand
	baronFragment["dsh_version"] = options.Version
	baronFragment["managed"] = true
	root["baron"] = baronFragment
	if err := writeJSONMap(path, root, 0o600); err != nil {
		return DSHReport{}, err
	}
	components := []string{"duckduckgo-search", "dsh-superpowers", "dsh-reverse-skill", "baron-dsh-adapter"}
	return DSHReport{Components: components, ActionRequired: "Configure the DeepSeek API key through the supported DSH credential flow."}, nil
}

func DSHComponents(path string) (map[string]bool, error) {
	root, err := readJSONMap(path)
	if err != nil {
		return nil, err
	}
	components := map[string]bool{}
	plugins, _ := root["plugins"].([]any)
	components["superpowers-dsh"] = containsString(plugins, "dsh-superpowers")
	components["dsh-reverse-skill"] = containsString(plugins, "dsh-reverse-skill")
	components["baron-dsh-adapter"] = containsString(plugins, "baron-dsh-adapter")
	mcp, _ := root["mcpServers"].(map[string]any)
	_, components["duckduckgo-search"] = mcp["ddg-search"]
	return components, nil
}

type Receipt struct {
	Component string    `json:"component"`
	Version   string    `json:"version"`
	Source    string    `json:"source"`
	Checksum  string    `json:"checksum,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Secret    string    `json:"-"`
}

func WriteReceipt(path string, receipt Receipt) error {
	receipt.CreatedAt = receipt.CreatedAt.UTC()
	if receipt.CreatedAt.IsZero() {
		receipt.CreatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(path, append(data, '\n'), 0o600)
}

func LegacyCollision(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0
}

func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("decode Baron-owned config: %w", err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

func writeJSONMap(path string, root map[string]any, perm os.FileMode) error {
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(path, append(data, '\n'), perm)
}

func backupBeforeEdit(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	backup := fmt.Sprintf("%s.baron-backup-%d", path, time.Now().UTC().UnixNano())
	return config.AtomicWriteFile(backup, data, 0o600)
}

func containsString(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func ConfigPath(base, name string) string {
	return filepath.Join(base, name)
}
