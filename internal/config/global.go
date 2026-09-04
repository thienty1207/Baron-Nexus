package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

type GlobalState struct {
	Version             int                                 `json:"version"`
	Identity            contracts.Identity                  `json:"identity"`
	DSHConfigPath       string                              `json:"dsh_config_path,omitempty"`
	DSHHomePath         string                              `json:"dsh_home_path,omitempty"`
	DSHProfilePatchPath string                              `json:"dsh_profile_patch_path,omitempty"`
	DSHComponents       map[string]bool                     `json:"dsh_components,omitempty"`
	CodexHooksPath      string                              `json:"codex_hooks_path,omitempty"`
	CodexHomePath       string                              `json:"codex_home_path,omitempty"`
	CodexAdapterPath    string                              `json:"codex_adapter_path,omitempty"`
	CodexHooksInstalled bool                                `json:"codex_hooks_installed"`
	PermissionDirectory string                              `json:"permission_directory,omitempty"`
	TencentInstallPath  string                              `json:"tencent_install_path,omitempty"`
	ProjectBindings     map[string]contracts.ProjectBinding `json:"project_bindings,omitempty"`
	ProjectRoots        map[string]string                   `json:"project_roots,omitempty"`
	Receipts            []string                            `json:"receipts,omitempty"`
	ManagedRuntime      *ManagedRuntimeState                `json:"managed_runtime,omitempty"`
	UpdatedAt           time.Time                           `json:"updated_at"`
}

// ManagedRuntimeState is additive state for the Baron-owned toolchain. It is
// intentionally receipt/path metadata only; credentials remain in protected
// files and are never serialized into global.json.
type ManagedRuntimeState struct {
	Root               string   `json:"root,omitempty"`
	CurrentGeneration  string   `json:"current_generation,omitempty"`
	PreviousGeneration string   `json:"previous_generation,omitempty"`
	PlanID             string   `json:"plan_id,omitempty"`
	Receipts           []string `json:"receipts,omitempty"`
	LauncherDirectory  string   `json:"launcher_directory,omitempty"`
	Launchers          []string `json:"launchers,omitempty"`
	RestartRequired    bool     `json:"restart_required,omitempty"`
}

func DefaultGlobalStatePath() (string, error) {
	dir, err := GlobalConfigDir("baron")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "global.json"), nil
}

func LoadGlobalState(path string) (GlobalState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return GlobalState{Version: 1, DSHComponents: map[string]bool{}, ProjectBindings: map[string]contracts.ProjectBinding{}, ProjectRoots: map[string]string{}}, nil
	}
	if err != nil {
		return GlobalState{}, err
	}
	var state GlobalState
	if err := json.Unmarshal(data, &state); err != nil {
		return GlobalState{}, fmt.Errorf("decode global Baron state: %w", err)
	}
	if state.Version == 0 {
		state.Version = 1
	}
	if state.DSHComponents == nil {
		state.DSHComponents = map[string]bool{}
	}
	if state.ProjectBindings == nil {
		state.ProjectBindings = map[string]contracts.ProjectBinding{}
	}
	if state.ProjectRoots == nil {
		state.ProjectRoots = map[string]string{}
	}
	return state, nil
}

func SaveGlobalState(path string, state GlobalState) error {
	if state.Version == 0 {
		state.Version = 1
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	if state.DSHComponents == nil {
		state.DSHComponents = map[string]bool{}
	}
	if state.ProjectBindings == nil {
		state.ProjectBindings = map[string]contracts.ProjectBinding{}
	}
	if state.ProjectRoots == nil {
		state.ProjectRoots = map[string]string{}
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWriteFile(path, append(data, '\n'), 0o600)
}
