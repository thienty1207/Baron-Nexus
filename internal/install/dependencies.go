package install

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ComponentState is the read-only discovery result used to decide whether a
// component needs mutation. An empty LatestVersion is never treated as a
// successful latest check.
type ComponentState struct {
	Name                 string
	Installed            bool
	LocalVersion         string
	LatestVersion        string
	NeedsUpdate          bool
	Changed              bool
	ConfigurationChanged bool
}

// DependencyReport combines a component state with the safe installation
// source recorded by callers. It never contains child-process output.
type DependencyReport struct {
	State  ComponentState
	Source string
}

type NPMDependencySpec struct {
	Name    string
	Package string
	Command string
}

// NormalizeVersion accepts release tags and bounded command output while
// keeping comparisons independent of a leading v prefix.
func NormalizeVersion(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("version is empty")
	}
	normalized := reportedVersion(value)
	if normalized == "" {
		return "", fmt.Errorf("%q does not contain a semantic version", value)
	}
	return normalized, nil
}

func componentState(name, localOutput, latestOutput string, installed bool) (ComponentState, error) {
	latest, err := NormalizeVersion(latestOutput)
	if err != nil {
		return ComponentState{}, fmt.Errorf("latest %s version is unknown: %w", name, err)
	}
	local := ""
	if installed {
		local, err = NormalizeVersion(localOutput)
		if err != nil {
			return ComponentState{}, fmt.Errorf("installed %s version is unverifiable: %w", name, err)
		}
	}
	return ComponentState{
		Name:          name,
		Installed:     installed,
		LocalVersion:  local,
		LatestVersion: latest,
		NeedsUpdate:   !installed || local != latest,
	}, nil
}

// DiscoverNPMDependencyLatest performs the mandatory local inspection and
// live npm registry query without installing or changing any files.
func DiscoverNPMDependencyLatest(ctx context.Context, runner CommandRunner, spec NPMDependencySpec) (ComponentState, error) {
	if runner == nil {
		return ComponentState{}, errors.New("Node/npm runner is not configured")
	}
	if strings.TrimSpace(spec.Name) == "" || strings.TrimSpace(spec.Package) == "" || strings.TrimSpace(spec.Command) == "" {
		return ComponentState{}, errors.New("npm dependency name, package, and command are required")
	}
	if _, err := runner.LookPath("npm"); err != nil {
		return ComponentState{}, fmt.Errorf("Node/npm is required to check the latest %s version", spec.Name)
	}
	installed := commandAvailable(runner, spec.Command)
	localOutput := ""
	if installed {
		var err error
		localOutput, err = runner.Run(ctx, spec.Command, "--version")
		if err != nil {
			return ComponentState{}, fmt.Errorf("verify installed %s version", spec.Name)
		}
	}
	latestOutput, err := runner.Run(ctx, "npm", "view", spec.Package, "version")
	if err != nil {
		return ComponentState{}, fmt.Errorf("check latest %s version through npm: registry query failed", spec.Name)
	}
	return componentState(spec.Name, localOutput, latestOutput, installed)
}

// EnsureNPMDependencyLatest checks the live npm version and mutates only when
// the local command is missing or differs from the exact resolved version.
func EnsureNPMDependencyLatest(ctx context.Context, runner CommandRunner, spec NPMDependencySpec, reporters ...ProgressReporter) (DependencyReport, error) {
	state, err := DiscoverNPMDependencyLatest(ctx, runner, spec)
	if err != nil {
		return DependencyReport{}, err
	}
	return EnsureNPMDependencyState(ctx, runner, spec, state, reporters...)
}

// EnsureNPMDependencyState applies a previously completed read-only discovery
// result. It is the mutation half of the discovery/mutation split and never
// performs another registry query.
func EnsureNPMDependencyState(ctx context.Context, runner CommandRunner, spec NPMDependencySpec, state ComponentState, reporters ...ProgressReporter) (DependencyReport, error) {
	reporter := firstProgressReporter(reporters...)
	if runner == nil {
		return DependencyReport{}, errors.New("Node/npm runner is not configured")
	}
	if strings.TrimSpace(spec.Name) == "" || strings.TrimSpace(spec.Package) == "" || strings.TrimSpace(spec.Command) == "" {
		return DependencyReport{}, errors.New("npm dependency name, package, and command are required")
	}
	if state.Name == "" {
		state.Name = spec.Name
	}
	latestVersion, err := NormalizeVersion(state.LatestVersion)
	if err != nil {
		return DependencyReport{}, fmt.Errorf("latest %s version is unknown: %w", spec.Name, err)
	}
	state.LatestVersion = latestVersion
	if state.Installed {
		localVersion, localErr := NormalizeVersion(state.LocalVersion)
		if localErr != nil {
			return DependencyReport{}, fmt.Errorf("installed %s version is unverifiable: %w", spec.Name, localErr)
		}
		state.LocalVersion = localVersion
	}
	state.NeedsUpdate = !state.Installed || state.LocalVersion != state.LatestVersion
	if !state.NeedsUpdate {
		reportStep(reporter, fmt.Sprintf("%s %s is already latest.", spec.Name, state.LatestVersion))
		return DependencyReport{State: state, Source: "npm:" + spec.Package}, nil
	}
	reportStep(reporter, fmt.Sprintf("Updating %s to %s...", spec.Name, state.LatestVersion))
	if err := installGlobalNPM(ctx, runner, spec.Package+"@"+state.LatestVersion); err != nil {
		return DependencyReport{}, fmt.Errorf("install %s %s: %w", spec.Name, state.LatestVersion, err)
	}
	if _, err := runner.LookPath(spec.Command); err != nil {
		return DependencyReport{}, fmt.Errorf("%s was installed but %s is not on PATH", spec.Name, spec.Command)
	}
	output, err := runner.Run(ctx, spec.Command, "--version")
	if err != nil {
		return DependencyReport{}, fmt.Errorf("verify %s installation", spec.Name)
	}
	installedVersion, err := NormalizeVersion(output)
	if err != nil || installedVersion != state.LatestVersion {
		return DependencyReport{}, fmt.Errorf("verify %s installation: expected version %s", spec.Name, state.LatestVersion)
	}
	state.Installed = true
	state.LocalVersion = installedVersion
	state.NeedsUpdate = false
	state.Changed = true
	reportStep(reporter, fmt.Sprintf("%s %s installed.", spec.Name, installedVersion))
	return DependencyReport{State: state, Source: "npm:" + spec.Package}, nil
}
