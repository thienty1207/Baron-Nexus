package project

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
)

const (
	agentsFileName     = "AGENTS.md"
	managedAgentsStart = "<!-- BARON:MANAGED:START"
	managedAgentsEnd   = "<!-- BARON:MANAGED:END -->"
)

//go:embed assets/AGENTS.md
var managedAgentsTemplate string

// ensureManagedAgents installs the Baron contract without replacing
// project-specific instructions outside the managed block.
func ensureManagedAgents(root string) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("AGENTS.md project root is required")
	}

	path := filepath.Join(root, agentsFileName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return config.AtomicWriteFile(path, []byte(normalizeManagedAgents(managedAgentsTemplate)), 0o644)
	}
	if err != nil {
		return fmt.Errorf("inspect AGENTS.md: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("AGENTS.md is a symlink; refusing to modify it")
	}
	if info.IsDir() {
		return errors.New("AGENTS.md is a directory")
	}

	existing, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read AGENTS.md: %w", err)
	}
	updated, changed, err := mergeManagedAgents(string(existing), managedAgentsTemplate)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if err := config.AtomicWriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}
	return nil
}

func mergeManagedAgents(existing, managed string) (string, bool, error) {
	managedBlock := strings.TrimRight(managed, "\r\n")
	startCount := strings.Count(existing, managedAgentsStart)
	endCount := strings.Count(existing, managedAgentsEnd)
	if startCount > 1 || endCount > 1 {
		return "", false, errors.New("AGENTS.md contains multiple Baron managed blocks")
	}
	if startCount != endCount {
		return "", false, errors.New("AGENTS.md contains an incomplete Baron managed block")
	}
	if startCount == 0 {
		if strings.TrimSpace(existing) == "" {
			return managedBlock + "\n", true, nil
		}
		separator := "\n"
		if !strings.HasSuffix(existing, "\n") {
			separator = "\n\n"
		}
		return existing + separator + managedBlock + "\n", true, nil
	}

	start := strings.Index(existing, managedAgentsStart)
	end := strings.Index(existing, managedAgentsEnd)
	if start < 0 || end < start {
		return "", false, errors.New("AGENTS.md contains an invalid Baron managed block")
	}
	end += len(managedAgentsEnd)
	updated := existing[:start] + managedBlock + existing[end:]
	return updated, updated != existing, nil
}

func normalizeManagedAgents(content string) string {
	return strings.TrimRight(content, "\r\n") + "\n"
}
