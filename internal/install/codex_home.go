package install

import (
	"os"
	"path/filepath"
	"strings"
)

// CodexHome returns the home directory used by Codex for its user-scoped
// configuration. CODEX_HOME is the supported override; without it, Codex
// defaults to ~/.codex rather than the platform's generic config directory.
func CodexHome() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CODEX_HOME")); configured != "" {
		return filepath.Clean(configured), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

// CodexHooksPath returns the official user-scoped hooks file discovered by
// Codex. Keeping this resolution in one place prevents Baron from installing
// hooks into ~/.config/codex, which Codex does not use for hooks.json.
func CodexHooksPath() (string, error) {
	home, err := CodexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "hooks.json"), nil
}
