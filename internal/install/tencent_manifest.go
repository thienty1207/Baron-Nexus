package install

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
)

const tencentDeploymentManifestName = "deployment-manifest.json"

// TencentDeploymentManifest is a non-secret record of the exact deployment
// revision and container image identities Baron last started. It deliberately
// contains no .env values, admin key, user key, or provider credential.
type TencentDeploymentManifest struct {
	SchemaVersion         int                 `json:"schema_version"`
	Repository            string              `json:"repository"`
	RequestedRef          string              `json:"requested_ref"`
	ResolvedCommit        string              `json:"resolved_commit"`
	ContainerImageDigests map[string][]string `json:"container_image_digests,omitempty"`
	UnresolvedContainers  []string            `json:"unresolved_containers,omitempty"`
	UpdatedAt             time.Time           `json:"updated_at"`
}

func isImmutableTencentRef(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateTencentRef(value string) error {
	if !isImmutableTencentRef(value) {
		return errors.New("Tencent deployment ref must be a 40-character immutable commit SHA; moving branches and tags are not accepted")
	}
	return nil
}

func resolveTencentDeploymentManifest(ctx context.Context, runner CommandRunner, options TencentDeploymentOptions) (TencentDeploymentManifest, error) {
	commitOutput, err := runner.Run(ctx, "git", "-C", options.Root, "rev-parse", "HEAD")
	if err != nil {
		return TencentDeploymentManifest{}, errors.New("resolve the checked-out Tencent deployment commit")
	}
	commit := strings.TrimSpace(strings.SplitN(commitOutput, "\n", 2)[0])
	if !isImmutableTencentRef(commit) {
		return TencentDeploymentManifest{}, errors.New("Tencent deployment did not resolve to an immutable commit SHA")
	}
	manifest := TencentDeploymentManifest{
		SchemaVersion:         1,
		Repository:            options.Repository,
		RequestedRef:          options.Ref,
		ResolvedCommit:        commit,
		ContainerImageDigests: map[string][]string{},
		UpdatedAt:             time.Now().UTC(),
	}
	for _, container := range []string{"tdai-memory-core", "tdai-memory-hub", "tdai-proxy"} {
		command := "docker"
		args := []string{"inspect", "--format={{json .RepoDigests}}", container}
		if options.UseSudo {
			command = "sudo"
			args = append([]string{"-n", "docker"}, args...)
		}
		imageOutput, inspectErr := runner.Run(ctx, command, args...)
		if inspectErr != nil {
			manifest.UnresolvedContainers = append(manifest.UnresolvedContainers, container)
			continue
		}
		var digests []string
		if json.Unmarshal([]byte(strings.TrimSpace(strings.SplitN(imageOutput, "\n", 2)[0])), &digests) != nil || len(digests) == 0 {
			manifest.UnresolvedContainers = append(manifest.UnresolvedContainers, container)
			continue
		}
		validDigests := make([]string, 0, len(digests))
		for _, digest := range digests {
			if imageDigestIsValid(digest) {
				validDigests = append(validDigests, digest)
			}
		}
		if len(validDigests) == 0 {
			manifest.UnresolvedContainers = append(manifest.UnresolvedContainers, container)
			continue
		}
		manifest.ContainerImageDigests[container] = validDigests
	}
	return manifest, nil
}

func imageDigestIsValid(value string) bool {
	index := strings.LastIndex(value, "@sha256:")
	if index < 0 {
		return false
	}
	value = value[index+len("@sha256:"):]
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func writeTencentDeploymentManifest(root string, manifest TencentDeploymentManifest) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("Tencent deployment root is required for its manifest")
	}
	if err := validateTencentRef(manifest.RequestedRef); err != nil {
		return err
	}
	if !isImmutableTencentRef(manifest.ResolvedCommit) {
		return errors.New("Tencent deployment manifest has no immutable resolved commit")
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(filepath.Join(root, tencentDeploymentManifestName), append(data, '\n'), 0o600)
}

func readTencentDeploymentManifest(root string) (TencentDeploymentManifest, error) {
	data, err := os.ReadFile(filepath.Join(root, tencentDeploymentManifestName))
	if err != nil {
		return TencentDeploymentManifest{}, err
	}
	var manifest TencentDeploymentManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return TencentDeploymentManifest{}, fmt.Errorf("decode Tencent deployment manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 || validateTencentRef(manifest.RequestedRef) != nil || !isImmutableTencentRef(manifest.ResolvedCommit) {
		return TencentDeploymentManifest{}, errors.New("Tencent deployment manifest is unsupported or not immutable")
	}
	return manifest, nil
}

// ReadTencentDeploymentManifest exposes the redacted deployment identity to
// status/doctor callers without exposing any managed runtime secret.
func ReadTencentDeploymentManifest(root string) (TencentDeploymentManifest, error) {
	return readTencentDeploymentManifest(root)
}

// UpdateTencentDeployment performs a pinned deployment update and restores the
// previous checked-out commit plus service startup if verification/startup
// fails. Docker volumes and the managed .env are intentionally left in place.
func UpdateTencentDeployment(ctx context.Context, runner CommandRunner, options TencentDeploymentOptions) (TencentDeploymentManifest, error) {
	if strings.TrimSpace(options.Root) == "" {
		return TencentDeploymentManifest{}, errors.New("Tencent deployment directory is required")
	}
	if options.Repository == "" {
		options.Repository = TencentMemoryRepository
	}
	if options.Ref == "" {
		options.Ref = TencentMemoryRef
	}
	if err := validateTencentRef(options.Ref); err != nil {
		return TencentDeploymentManifest{}, err
	}
	previous, err := readTencentDeploymentManifest(options.Root)
	if err != nil {
		return TencentDeploymentManifest{}, fmt.Errorf("read the previous Tencent deployment manifest before update: %w", err)
	}
	options.PullLatest = true
	if err := EnsureTencentDeployment(ctx, runner, options); err != nil {
		rollbackOptions := options
		rollbackOptions.Ref = previous.ResolvedCommit
		rollbackOptions.PullLatest = false
		if rollbackErr := RollbackTencentDeployment(ctx, runner, rollbackOptions, previous); rollbackErr != nil {
			return previous, fmt.Errorf("Tencent deployment update failed: %v; rollback failed: %w", err, rollbackErr)
		}
		return previous, fmt.Errorf("Tencent deployment update failed and was rolled back: %w", err)
	}
	updated, err := readTencentDeploymentManifest(options.Root)
	if err != nil {
		return TencentDeploymentManifest{}, fmt.Errorf("read the updated Tencent deployment manifest: %w", err)
	}
	return updated, nil
}

// RollbackTencentDeployment starts a previously recorded immutable deployment
// without pulling a moving target or deleting service data.
func RollbackTencentDeployment(ctx context.Context, runner CommandRunner, options TencentDeploymentOptions, manifest TencentDeploymentManifest) error {
	if runner == nil {
		return errors.New("Docker deployment runner is not configured")
	}
	if err := validateTencentRef(manifest.ResolvedCommit); err != nil {
		return fmt.Errorf("rollback manifest is invalid: %w", err)
	}
	if options.Repository == "" {
		options.Repository = firstNonEmptyTencent(manifest.Repository, TencentMemoryRepository)
	}
	options.Ref = manifest.ResolvedCommit
	if err := ensureManagedCheckout(ctx, runner, options); err != nil {
		return fmt.Errorf("check out the previous Tencent deployment: %w", err)
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
		return err
	}
	if _, err := runManagedScript(ctx, runner, options.UseSudo, false, verifyScript, "--skip-llm"); err != nil {
		return errors.New("previous Tencent deployment verification failed during rollback")
	}
	startScript := filepath.Join(deployDir, "start-all.sh")
	if err := requireRegularFile(startScript); err != nil {
		return err
	}
	if _, err := runManagedScript(ctx, runner, options.UseSudo, false, startScript); err != nil {
		return errors.New("previous Tencent deployment failed to restart during rollback")
	}
	if err := ensureManagedRestartPolicy(ctx, runner, options.UseSudo); err != nil {
		return err
	}
	if options.UseSudo {
		if err := restoreManagedOwnership(ctx, runner, options.Root); err != nil {
			return err
		}
	}
	resolved, err := resolveTencentDeploymentManifest(ctx, runner, options)
	if err != nil {
		return err
	}
	if resolved.ResolvedCommit != manifest.ResolvedCommit {
		return errors.New("Tencent rollback checked out a different commit than the recorded previous deployment")
	}
	resolved.RequestedRef = manifest.RequestedRef
	resolved.Repository = manifest.Repository
	return writeTencentDeploymentManifest(options.Root, resolved)
}

func firstNonEmptyTencent(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
