package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/install"
	"github.com/baron-shared-brain/baron/internal/managedruntime"
)

func TestSetCredentialAutomaticallyWritesManagedStrixEnvironment(t *testing.T) {
	root := t.TempDir()
	dshHome := filepath.Join(root, "dsh")
	t.Setenv("DSH_HOME", dshHome)
	t.Setenv("DEEPSEEK_API_KEY", "")
	runtimeRoot := filepath.Join(root, "managed-runtime")
	paths, err := managedruntime.ResolvePaths(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Credentials, 0o700); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(root, "global.json")
	if err := config.SaveGlobalState(globalPath, config.GlobalState{ManagedRuntime: &config.ManagedRuntimeState{Root: runtimeRoot}}); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = globalPath
	application.ReadSecret = func(io.Reader) ([]byte, error) { return []byte("new-provider-key"), nil }
	application.ValidateProviderCredential = func(context.Context, string, string) error { return nil }
	if err := application.SetCredential("deepseek"); err != nil {
		t.Fatal(err)
	}
	stored, err := install.ReadDSHProviderKey(map[string]string{"DSH_HOME": dshHome})
	if err != nil || stored != "new-provider-key" {
		t.Fatalf("DSH key=%q err=%v", stored, err)
	}
	values, err := config.ReadEnvFile(filepath.Join(paths.Credentials, "strix.env"))
	if err != nil {
		t.Fatal(err)
	}
	if values["DEEPSEEK_API_KEY"] != "new-provider-key" || values["STRIX_PROVIDER"] != "deepseek" {
		t.Fatalf("Strix environment was not populated: %#v", values)
	}
}

func TestResolveDSHCredentialFansOutValidatedKeyToManagedStrixDuringBootstrap(t *testing.T) {
	root := t.TempDir()
	dshHome := filepath.Join(root, "dsh")
	t.Setenv("DSH_HOME", dshHome)
	t.Setenv("DEEPSEEK_API_KEY", "")
	runtimeRoot := filepath.Join(root, "managed-runtime")
	paths, err := managedruntime.ResolvePaths(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Credentials, 0o700); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(root, "global.json")
	if err := config.SaveGlobalState(globalPath, config.GlobalState{
		ManagedRuntime: &config.ManagedRuntimeState{Root: runtimeRoot},
	}); err != nil {
		t.Fatal(err)
	}
	application := New()
	application.GlobalPath = globalPath
	application.ReadSecret = func(io.Reader) ([]byte, error) { return []byte("bootstrap-provider-key"), nil }
	application.ValidateProviderCredential = func(context.Context, string, string) error { return nil }

	if _, err := application.resolveDSHCredential(); err != nil {
		t.Fatal(err)
	}
	values, err := config.ReadEnvFile(filepath.Join(paths.Credentials, "strix.env"))
	if err != nil {
		t.Fatal(err)
	}
	if values["LLM_API_KEY"] != "bootstrap-provider-key" || values["DEEPSEEK_API_KEY"] != "bootstrap-provider-key" {
		t.Fatalf("bootstrap key did not reach managed Strix environment: %#v", values)
	}
}
