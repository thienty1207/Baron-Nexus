package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
)

const (
	TencentMemoryRepository = "https://github.com/TencentCloud/TencentDB-Agent-Memory.git"
	TencentMemoryRef        = "97f94654280b2932c35ba4806a491999ed244cc9"
)

// TencentDeploymentOptions describes the Baron-managed copy of the official
// global-images deployment. The repository and ref are explicit so a repair
// cannot silently follow a moving upstream branch.
type TencentDeploymentOptions struct {
	Root       string
	Repository string
	Ref        string
}

// EnsureTencentDeployment fetches or verifies the pinned upstream deployment,
// preserves its .env.example structure, and starts the official three-service
// stack. It never prints command output because the upstream scripts may echo
// credentials created during initialization.
func EnsureTencentDeployment(ctx context.Context, runner CommandRunner, options TencentDeploymentOptions) error {
	if runner == nil {
		return errors.New("Docker deployment runner is not configured")
	}
	if strings.TrimSpace(options.Root) == "" {
		return errors.New("Tencent deployment directory is required")
	}
	if options.Repository == "" {
		options.Repository = TencentMemoryRepository
	}
	if options.Ref == "" {
		options.Ref = TencentMemoryRef
	}
	if _, err := runner.LookPath("git"); err != nil {
		return errors.New("Git is required to fetch the pinned Tencent Agent Memory deployment")
	}
	if _, err := runner.LookPath("docker"); err != nil {
		return errors.New("Docker CLI is required for Tencent Agent Memory initialization; install Docker first")
	}
	if err := ensureManagedCheckout(ctx, runner, options); err != nil {
		return err
	}
	deployDir := filepath.Join(options.Root, "deploy", "global-images")
	if err := ensureUpstreamEnv(deployDir); err != nil {
		return err
	}
	verifyScript := filepath.Join(deployDir, "verify.sh")
	if err := requireRegularFile(verifyScript); err != nil {
		return fmt.Errorf("Tencent deployment verification script is unavailable: %w", err)
	}
	if _, err := runner.Run(ctx, verifyScript, "--skip-llm"); err != nil {
		return errors.New("Tencent deployment verification failed; inspect the managed global-images .env and Docker setup")
	}
	startScript := filepath.Join(deployDir, "start-all.sh")
	if err := requireRegularFile(startScript); err != nil {
		return fmt.Errorf("Tencent deployment start script is unavailable: %w", err)
	}
	if _, err := runner.Run(ctx, startScript); err != nil {
		return errors.New("Tencent deployment start failed; inspect the managed global-images .env and Docker logs")
	}
	return nil
}

func ensureManagedCheckout(ctx context.Context, runner CommandRunner, options TencentDeploymentOptions) error {
	info, err := os.Lstat(options.Root)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(options.Root), 0o700); err != nil {
			return fmt.Errorf("create Tencent deployment parent: %w", err)
		}
		if _, err := runner.Run(ctx, "git", "clone", "--no-checkout", options.Repository, options.Root); err != nil {
			return errors.New("clone the pinned Tencent Agent Memory repository")
		}
		if _, err := runner.Run(ctx, "git", "-C", options.Root, "fetch", "--depth", "1", "origin", options.Ref); err != nil {
			return errors.New("fetch the pinned Tencent Agent Memory revision")
		}
		if _, err := runner.Run(ctx, "git", "-C", options.Root, "checkout", "--detach", options.Ref); err != nil {
			return errors.New("check out the pinned Tencent Agent Memory revision")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Tencent deployment directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("Tencent deployment directory is not a safe managed directory")
	}
	if _, err := os.Stat(filepath.Join(options.Root, ".git")); err != nil {
		return errors.New("existing Tencent deployment directory is not a Git checkout; move it aside and retry")
	}
	if _, err := runner.Run(ctx, "git", "-C", options.Root, "fetch", "--depth", "1", "origin", options.Ref); err != nil {
		return errors.New("refresh the pinned Tencent Agent Memory revision")
	}
	if _, err := runner.Run(ctx, "git", "-C", options.Root, "checkout", "--detach", options.Ref); err != nil {
		return errors.New("check out the pinned Tencent Agent Memory revision")
	}
	return nil
}

func ensureUpstreamEnv(deployDir string) error {
	if err := requireDirectory(deployDir); err != nil {
		return err
	}
	envPath := filepath.Join(deployDir, ".env")
	if info, err := os.Lstat(envPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("Tencent deployment .env is not a safe regular file")
		}
		_ = os.Chmod(envPath, 0o600)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	example := filepath.Join(deployDir, ".env.example")
	data, err := os.ReadFile(example)
	if err != nil {
		return fmt.Errorf("read Tencent deployment .env.example: %w", err)
	}
	return config.AtomicWriteFile(envPath, data, 0o600)
}

func requireDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

func requireRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

// TencentAdminKey reads the upstream-created admin key only for the current
// process. Callers must not place this value in project configuration.
func TencentAdminKey(root string) (string, error) {
	path := filepath.Join(root, "deploy", "global-images", ".admin-key")
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("Tencent deployment admin key has weak permissions")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", errors.New("Tencent deployment admin key is empty")
	}
	return key, nil
}
