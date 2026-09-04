package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/testsupport"
)

func TestGlobalStateIsAtomicAndSecretSafeInStandardConfigFile(t *testing.T) {
	if !testsupport.UnixModeBitsReliable() {
		t.Skip("Windows ACLs do not expose Unix permission bits")
	}
	path := filepath.Join(t.TempDir(), "baron", "global.json")
	state := GlobalState{
		Identity:      contracts.Identity{UserID: "usr-baron", UserKey: "sk-secret", TeamID: "team-baron", Endpoint: "http://127.0.0.1:8420"},
		DSHComponents: map[string]bool{"baron-dsh-adapter": true}, CodexHooksInstalled: true, CodexAdapterPath: "/tmp/baron/codex-adapter",
	}
	if err := SaveGlobalState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadGlobalState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Identity.TeamID != state.Identity.TeamID || !loaded.DSHComponents["baron-dsh-adapter"] || loaded.CodexAdapterPath != state.CodexAdapterPath {
		t.Fatalf("global state lost fields: %#v", loaded)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sk-secret") {
		t.Fatal("global state did not retain credential for runtime use")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("global state is too permissive: %o", info.Mode().Perm())
	}
}

func TestGlobalStatePreservesAdditiveManagedRuntimeState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baron", "global.json")
	state := GlobalState{
		Identity: contracts.Identity{UserID: "user", TeamID: "team"},
		ManagedRuntime: &ManagedRuntimeState{
			Root:              filepath.Join(t.TempDir(), "managed-runtime"),
			CurrentGeneration: "generation-1",
			PlanID:            "plan-1",
		},
	}
	if err := SaveGlobalState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadGlobalState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ManagedRuntime.CurrentGeneration != "generation-1" || loaded.ManagedRuntime.PlanID != "plan-1" {
		t.Fatalf("managed runtime state was not retained: %#v", loaded.ManagedRuntime)
	}
}
