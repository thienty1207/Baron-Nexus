package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/managedruntime"
)

type StrixRuntime struct {
	PythonPath      string
	UVPath          string
	StrixPath       string
	EnvironmentPath string
	DockerBackend   string
	Runner          CommandRunner
}

type ManagedRuntimeInput struct {
	Paths      managedruntime.Paths
	Plan       managedruntime.ResolutionPlan
	Generation string
	Progress   ProgressReporter
	Runner     CommandRunner
}

func EnsureStrixRuntime(ctx context.Context, input ManagedRuntimeInput) (StrixRuntime, error) {
	if err := ctx.Err(); err != nil {
		return StrixRuntime{}, err
	}
	if err := input.Plan.Validate(); err != nil {
		return StrixRuntime{}, err
	}
	if strings.TrimSpace(input.Generation) == "" {
		return StrixRuntime{}, errors.New("managed Strix generation is required")
	}
	generation, err := input.Paths.Generation(input.Generation)
	if err != nil {
		return StrixRuntime{}, err
	}
	components := make(map[managedruntime.ComponentID]managedruntime.ComponentPlan, len(input.Plan.Components))
	for _, component := range input.Plan.Components {
		components[component.ID] = component
	}
	for _, dependency := range []managedruntime.ComponentID{managedruntime.ComponentUV, managedruntime.ComponentPython, managedruntime.ComponentStrix} {
		if _, ok := components[dependency]; !ok {
			return StrixRuntime{}, fmt.Errorf("Strix dependency %s is missing from the resolution plan", dependency)
		}
	}
	result := StrixRuntime{DockerBackend: "managed"}
	for _, item := range []struct {
		id     managedruntime.ComponentID
		target *string
		name   string
	}{
		{managedruntime.ComponentUV, &result.UVPath, "uv"},
		{managedruntime.ComponentPython, &result.PythonPath, "python"},
		{managedruntime.ComponentStrix, &result.StrixPath, "strix"},
	} {
		componentPath := filepath.Join(generation, string(item.id))
		if err := input.Paths.ValidateOwned(componentPath); err != nil {
			return StrixRuntime{}, err
		}
		if _, err := os.Stat(componentPath); err != nil {
			return StrixRuntime{}, fmt.Errorf("managed Strix dependency %s is not staged", item.name)
		}
		executableName := item.name
		if configured := strings.TrimSpace(components[item.id].EntryPoint); configured != "" {
			executableName = configured
		}
		candidate, err := managedruntime.FindExecutableNamed(componentPath, executableName)
		if err != nil {
			return StrixRuntime{}, fmt.Errorf("resolve managed Strix dependency %s executable: %w", item.name, err)
		}
		*item.target = candidate
	}
	result.EnvironmentPath = filepath.Join(input.Paths.Credentials, "strix.env")
	if err := input.Paths.ValidateOwned(result.EnvironmentPath); err != nil {
		return StrixRuntime{}, err
	}
	if _, err := os.Stat(result.EnvironmentPath); errors.Is(err, os.ErrNotExist) {
		if err := config.WriteEnv(result.EnvironmentPath, map[string]string{
			"LLM_API_BASE":   defaultStrixDeepSeekBase,
			"STRIX_LLM":      defaultStrixDeepSeekModel,
			"STRIX_PROVIDER": "deepseek",
		}); err != nil {
			return StrixRuntime{}, fmt.Errorf("write managed Strix environment: %w", err)
		}
	} else if err != nil {
		return StrixRuntime{}, fmt.Errorf("inspect managed Strix environment: %w", err)
	}
	result.Runner = input.Runner
	return result, nil
}

func ProbeStrixRuntime(ctx context.Context, runtime StrixRuntime) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(runtime.StrixPath) == "" {
		return errors.New("managed Strix executable is not configured")
	}
	if runtime.Runner == nil {
		return errors.New("managed Strix command runner is not configured")
	}
	for _, args := range [][]string{{"--version"}, {"--help"}} {
		if _, err := runtime.Runner.Run(ctx, runtime.StrixPath, args...); err != nil {
			return fmt.Errorf("probe Strix %s: %w", args[0], err)
		}
	}
	return nil
}

func EnsureLanguageRuntime(ctx context.Context, component managedruntime.ComponentPlan, input ManagedRuntimeInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(string(component.ID)) == "" {
		return errors.New("language runtime component is required")
	}
	generation, err := input.Paths.Generation(input.Generation)
	if err != nil {
		return err
	}
	componentPath := filepath.Join(generation, string(component.ID))
	if err := input.Paths.ValidateOwned(componentPath); err != nil {
		return err
	}
	info, err := os.Stat(componentPath)
	if err != nil {
		return fmt.Errorf("managed language runtime %s is not staged: %w", component.ID, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("managed language runtime %s staging path is not a directory", component.ID)
	}
	if input.Progress != nil {
		input.Progress.Step(string(component.ID) + " runtime ready")
	}
	return nil
}
