package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

func TestGlobalStateIsAtomicAndSecretSafeInStandardConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baron", "global.json")
	state := GlobalState{
		Identity:      contracts.Identity{UserID: "usr-baron", UserKey: "sk-secret", TeamID: "team-baron", Endpoint: "http://127.0.0.1:8420"},
		DSHComponents: map[string]bool{"baron-dsh-adapter": true}, CodexHooksInstalled: true,
	}
	if err := SaveGlobalState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadGlobalState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Identity.TeamID != state.Identity.TeamID || !loaded.DSHComponents["baron-dsh-adapter"] {
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
