package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
)

const (
	TencentMemoryRepository = "https://github.com/TencentCloud/TencentDB-Agent-Memory.git"
	tencentProxyRuntimeUID  = "10001"
)

// TencentDeploymentOptions describes the Baron-managed copy of the official
// global-images deployment. An empty ref resolves the upstream default HEAD
// once and then uses that immutable commit for the operation.
type TencentDeploymentOptions struct {
	Root       string
	Repository string
	Ref        string
	UseSudo    bool
	Runtime    TencentRuntimeConfig
	PullLatest bool
}

// EnsureTencentDeployment fetches or verifies the latest resolved upstream deployment,
// preserves its .env.example structure, and starts the official Core,
// MemoryHub/Panel, Proxy, and combined Knowledge stack. It never prints command output because the upstream scripts may echo
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
	if _, err := os.Lstat(options.Root); errors.Is(err, os.ErrNotExist) {
		if missing := options.Runtime.MissingProviderValues(); len(missing) > 0 {
			return errors.New("Tencent runtime configuration is required before the first checkout; set " + strings.Join(missing, ", ") + " or export the BARON_TENCENT_* variables, then rerun baron tencent-memory init")
		}
	}
	if _, err := runner.LookPath("git"); err != nil {
		return errors.New("Git is required to fetch the latest Tencent Agent Memory deployment")
	}
	if _, err := runner.LookPath("docker"); err != nil {
		return errors.New("Docker CLI is required for Tencent Agent Memory initialization; install Docker first")
	}
	if strings.TrimSpace(options.Ref) == "" {
		resolvedRef, resolveErr := resolveLatestTencentRef(ctx, runner, options.Repository)
		if resolveErr != nil {
			return resolveErr
		}
		options.Ref = resolvedRef
	}
	if err := validateTencentRef(options.Ref); err != nil {
		return err
	}
	if err := ensureManagedCheckout(ctx, runner, options); err != nil {
		return err
	}
	deployDir := filepath.Join(options.Root, "deploy", "global-images")
	if err := ensureUpstreamEnv(deployDir); err != nil {
		return err
	}
	if err := EnsureTencentRuntimeEnv(deployDir, options.Runtime); err != nil {
		return err
	}
	verifyScript := filepath.Join(deployDir, "verify.sh")
	if err := requireRegularFile(verifyScript); err != nil {
		return fmt.Errorf("Tencent deployment verification script is unavailable: %w", err)
	}
	if _, err := runManagedScript(ctx, runner, options.UseSudo, options.PullLatest, verifyScript, "--skip-llm"); err != nil {
		return errors.New("Tencent deployment verification failed; inspect the managed global-images .env and Docker setup")
	}
	startScript := filepath.Join(deployDir, "start-all.sh")
	if err := requireRegularFile(startScript); err != nil {
		return fmt.Errorf("Tencent deployment start script is unavailable: %w", err)
	}
	if err := ensureTencentProxyConfigReadable(ctx, runner, options.UseSudo, deployDir); err != nil {
		return err
	}
	_, startErr := runManagedScript(ctx, runner, options.UseSudo, options.PullLatest, startScript)
	if options.UseSudo {
		// The official proxy script can regenerate its bind-mounted config with
		// root ownership while starting. Repair the file and retry only the
		// proxy when that leaves tdai-proxy exited; this directly handles the
		// non-root image permission failure without weakening the file mode.
		if repairErr := ensureTencentProxyConfigReadable(ctx, runner, options.UseSudo, deployDir); repairErr != nil {
			if startErr != nil {
				return errors.New("Tencent deployment start failed; inspect the managed global-images .env and Docker logs")
			}
			return repairErr
		}
		if startErr != nil {
			if restartErr := restartExitedTencentProxy(ctx, runner); restartErr == nil {
				startErr = nil
			}
		}
	}
	if startErr != nil {
		return errors.New("Tencent deployment start failed; inspect the managed global-images .env and Docker logs")
	}
	if err := ensureManagedRestartPolicy(ctx, runner, options.UseSudo); err != nil {
		return err
	}
	if options.UseSudo {
		if err := restoreManagedOwnership(ctx, runner, options.Root); err != nil {
			return err
		}
		if err := ensureTencentProxyConfigReadable(ctx, runner, options.UseSudo, deployDir); err != nil {
			return err
		}
		if err := restartExitedTencentProxy(ctx, runner); err != nil {
			return err
		}
	}
	manifest, err := resolveTencentDeploymentManifest(ctx, runner, options)
	if err != nil {
		return err
	}
	if manifest.ResolvedCommit != options.Ref {
		return errors.New("Tencent deployment resolved a different commit than the requested immutable ref")
	}
	if err := writeTencentDeploymentManifest(options.Root, manifest); err != nil {
		return fmt.Errorf("record Tencent deployment manifest: %w", err)
	}
	return nil
}

func restartExitedTencentProxy(ctx context.Context, runner CommandRunner) error {
	output, err := runSudo(ctx, runner, "docker", "inspect", "--format={{.State.Status}}", "tdai-proxy")
	if err != nil {
		// The regular health checks will classify a missing container. Do not
		// turn an upstream inspect incompatibility into a destructive action.
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(output)) {
	case "exited", "dead", "created":
		if _, err := runSudo(ctx, runner, "docker", "restart", "tdai-proxy"); err != nil {
			return errors.New("Tencent proxy remained stopped after automatic permission repair; inspect Docker logs")
		}
	}
	return nil
}

func ensureManagedRestartPolicy(ctx context.Context, runner CommandRunner, useSudo bool) error {
	for _, container := range []string{"tdai-memory-core", "tdai-memory-hub", "tdai-proxy"} {
		args := []string{"update", "--restart", "unless-stopped", container}
		if useSudo {
			args = append([]string{"docker"}, args...)
		}
		var err error
		if useSudo {
			_, err = runSudo(ctx, runner, args...)
		} else {
			_, err = runner.Run(ctx, "docker", args...)
		}
		if err != nil {
			return fmt.Errorf("set Docker restart policy for %s: %w", container, err)
		}
	}
	return nil
}

func runManagedScript(ctx context.Context, runner CommandRunner, useSudo, pullLatest bool, script string, args ...string) (string, error) {
	if pullLatest {
		if useSudo {
			commandArgs := append([]string{"env", "PULL=1", "bash", script}, args...)
			return runSudo(ctx, runner, commandArgs...)
		}
		commandArgs := append([]string{"PULL=1", script}, args...)
		return runner.Run(ctx, "env", commandArgs...)
	}
	if useSudo {
		commandArgs := append([]string{"bash", script}, args...)
		return runSudo(ctx, runner, commandArgs...)
	}
	return runner.Run(ctx, script, args...)
}

func restoreManagedOwnership(ctx context.Context, runner CommandRunner, root string) error {
	identity, err := user.Current()
	if err != nil || identity.Uid == "" || identity.Gid == "" {
		return errors.New("managed Tencent files were created with sudo but the current user identity could not be resolved; inspect the deployment directory ownership before retrying")
	}
	if _, err := runSudo(ctx, runner, "chown", "-R", identity.Uid+":"+identity.Gid, root); err != nil {
		return errors.New("managed Tencent files were created but ownership could not be restored; run sudo chown -R $(id -u):$(id -g) " + root)
	}
	return nil
}

// ensureTencentProxyConfigReadable prepares the official proxy's bind-mounted
// config for its non-root runtime user. The upstream script creates the file
// after start-all.sh begins; when Baron invokes that script through sudo, the
// directory/file inherit root-only permissions and UID 10001 cannot read the
// mount. The file contains the upstream API key, so it is kept mode 0400 and
// owned by the container UID instead of being made world-readable.
func ensureTencentProxyConfigReadable(ctx context.Context, runner CommandRunner, useSudo bool, deployDir string) error {
	if !useSudo {
		// The Linux bootstrap path uses sudo whenever the Docker socket is not
		// directly readable. Docker Desktop/other non-sudo runtimes own the
		// host-side permission model and must not be changed by Baron here.
		return nil
	}
	configDir := filepath.Join(deployDir, ".proxy-config")
	configFile := filepath.Join(configDir, "config.yaml")
	for _, operation := range []struct {
		name string
		args []string
	}{
		{name: "mkdir", args: []string{"-p", configDir}},
		{name: "chmod", args: []string{"0755", configDir}},
		{name: "touch", args: []string{configFile}},
		{name: "chown", args: []string{tencentProxyRuntimeUID + ":" + tencentProxyRuntimeUID, configFile}},
		{name: "chmod", args: []string{"0400", configFile}},
	} {
		commandArgs := append([]string{operation.name}, operation.args...)
		if _, err := runSudo(ctx, runner, commandArgs...); err != nil {
			return fmt.Errorf("prepare Tencent proxy config permissions (%s): %w", operation.name, err)
		}
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
			return errors.New("clone the latest Tencent Agent Memory repository")
		}
		if _, err := runner.Run(ctx, "git", "-C", options.Root, "fetch", "--depth", "1", "origin", options.Ref); err != nil {
			return errors.New("fetch the resolved Tencent Agent Memory revision")
		}
		if _, err := runner.Run(ctx, "git", "-C", options.Root, "checkout", "--detach", options.Ref); err != nil {
			return errors.New("check out the resolved Tencent Agent Memory revision")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Tencent deployment directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("Tencent deployment directory is not a safe managed directory")
	}
	if options.UseSudo {
		owner := currentUserOwner()
		if owner == "" {
			return errors.New("managed Tencent deployment owner could not be resolved for update; rerun with a normal user account")
		}
		if _, err := runSudo(ctx, runner, "chown", "-R", owner, options.Root); err != nil {
			return errors.New("managed Tencent deployment ownership could not be prepared for update; rerun after granting sudo")
		}
	}
	if _, err := os.Stat(filepath.Join(options.Root, ".git")); err != nil {
		return errors.New("existing Tencent deployment directory is not a Git checkout; move it aside and retry")
	}
	if _, err := runner.Run(ctx, "git", "-C", options.Root, "fetch", "--depth", "1", "origin", options.Ref); err != nil {
		return errors.New("refresh the resolved Tencent Agent Memory revision")
	}
	if _, err := runner.Run(ctx, "git", "-C", options.Root, "checkout", "--detach", options.Ref); err != nil {
		return errors.New("check out the resolved Tencent Agent Memory revision")
	}
	return nil
}

func currentUserOwner() string {
	identity, err := user.Current()
	if err != nil || identity.Uid == "" || identity.Gid == "" {
		return ""
	}
	return identity.Uid + ":" + identity.Gid
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
