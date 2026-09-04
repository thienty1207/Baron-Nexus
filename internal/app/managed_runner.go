package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/install"
	"github.com/baron-shared-brain/baron/internal/managedruntime"
)

var errManagedRuntimeExecutable = errors.New("managed runtime executable is unavailable")

type managedCommandRunner struct {
	base         install.CommandRunner
	commands     map[string]string
	managedNames map[string]struct{}
	path         string
}

func newManagedCommandRunner(base install.CommandRunner, state config.ManagedRuntimeState) (install.CommandRunner, error) {
	if base == nil {
		return nil, errors.New("managed command runner base is not configured")
	}
	paths, err := managedruntime.ResolvePaths(state.Root)
	if err != nil {
		return nil, err
	}
	generation, err := paths.Generation(state.CurrentGeneration)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(generation)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("managed runtime generation is unavailable: %w", errManagedRuntimeExecutable)
	}
	commands := make(map[string]string)
	managedNames := make(map[string]struct{})
	directories := []string{generation}
	components := []struct {
		id    managedruntime.ComponentID
		names []string
	}{
		{managedruntime.ComponentUV, []string{"uv", "uvx"}},
		{managedruntime.ComponentPython, []string{"python", "python3"}},
		{managedruntime.ComponentStrix, []string{"strix"}},
		{managedruntime.ComponentBun, []string{"bun"}},
		{managedruntime.ComponentGo, []string{"go"}},
		{managedruntime.ComponentNode, []string{"node"}},
		{managedruntime.ComponentNPM, []string{"npm", "npx"}},
		{managedruntime.ComponentPNPM, []string{"pnpm"}},
		{managedruntime.ComponentDSH, []string{"dsh"}},
		{managedruntime.ComponentCodex, []string{"codex"}},
	}
	for _, component := range components {
		for _, name := range component.names {
			managedNames[name] = struct{}{}
		}
		componentRoot := filepath.Join(generation, string(component.id))
		if err := paths.ValidateOwned(componentRoot); err != nil {
			return nil, err
		}
		componentFound := false
		for _, name := range component.names {
			if componentPath, findErr := managedruntime.FindExecutableNamed(componentRoot, name); findErr == nil {
				commands[name] = componentPath
				componentFound = true
				directories = append(directories, filepath.Dir(componentPath))
			}
		}
		if componentFound {
			directories = append(directories, componentRoot, filepath.Join(componentRoot, "bin"), filepath.Join(componentRoot, "Scripts"))
		}
	}
	pathParts := make([]string, 0, len(directories)+1)
	seen := map[string]struct{}{}
	appendDirectory := func(directory string) {
		if strings.TrimSpace(directory) == "" {
			return
		}
		directory = filepath.Clean(directory)
		if _, ok := seen[directory]; ok {
			return
		}
		seen[directory] = struct{}{}
		pathParts = append(pathParts, directory)
	}
	for _, directory := range directories {
		appendDirectory(directory)
	}
	for _, directory := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		appendDirectory(directory)
	}
	return managedCommandRunner{base: base, commands: commands, managedNames: managedNames, path: strings.Join(pathParts, string(os.PathListSeparator))}, nil
}

func (r managedCommandRunner) LookPath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if path, ok := r.commands[name]; ok {
		return path, nil
	}
	if _, managed := r.managedNames[name]; managed {
		return "", fmt.Errorf("%w: %s", errManagedRuntimeExecutable, name)
	}
	return r.base.LookPath(name)
}

func (r managedCommandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if _, ok := r.base.(install.EnvironmentCommandRunner); ok {
		return r.RunWithEnvironment(ctx, nil, name, args...)
	}
	executable, err := r.LookPath(name)
	if err != nil {
		return "", err
	}
	return r.base.Run(ctx, executable, args...)
}

func (r managedCommandRunner) RunWithEnvironment(ctx context.Context, environment map[string]string, name string, args ...string) (string, error) {
	return r.runWithEnvironment(ctx, environment, "", name, args...)
}

func (r managedCommandRunner) RunWithEnvironmentInDir(ctx context.Context, environment map[string]string, workDir, name string, args ...string) (string, error) {
	return r.runWithEnvironment(ctx, environment, workDir, name, args...)
}

func (r managedCommandRunner) runWithEnvironment(ctx context.Context, environment map[string]string, workDir, name string, args ...string) (string, error) {
	executable, err := r.LookPath(name)
	if err != nil {
		return "", err
	}
	values := make(map[string]string, len(environment)+1)
	for key, value := range environment {
		values[key] = value
	}
	// The managed generation must win over the host PATH, while the caller's
	// provider values remain limited to the explicit environment map.
	values["PATH"] = r.path
	if strings.TrimSpace(workDir) != "" {
		isolated, ok := r.base.(install.WorkingDirectoryEnvironmentCommandRunner)
		if !ok {
			return "", errors.New("managed command runner base does not support working-directory isolation")
		}
		return isolated.RunWithEnvironmentInDir(ctx, values, workDir, executable, args...)
	}
	isolated, ok := r.base.(install.EnvironmentCommandRunner)
	if !ok {
		return "", errors.New("managed command runner base does not support environment isolation")
	}
	return isolated.RunWithEnvironment(ctx, values, executable, args...)
}

func (a *App) commandRunnerForGlobal(global config.GlobalState) (install.CommandRunner, error) {
	if global.ManagedRuntime == nil || strings.TrimSpace(global.ManagedRuntime.Root) == "" || strings.TrimSpace(global.ManagedRuntime.CurrentGeneration) == "" {
		return a.commandRunner(), nil
	}
	return newManagedCommandRunner(a.commandRunner(), *global.ManagedRuntime)
}
