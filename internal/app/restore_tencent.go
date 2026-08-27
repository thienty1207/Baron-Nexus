package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/install"
	"github.com/baron-shared-brain/baron/internal/memory/tencent"
)

// TencentRestoreFunc is the restore-time boundary for Tencent deployment and
// metadata verification. The default implementation is fully automated; the
// injectable form keeps staging-order tests deterministic and offline.
type TencentRestoreFunc func(context.Context, string, config.GlobalState) (config.GlobalState, error)

func (a *App) restoreTencentBeforeBinding(ctx context.Context, stage string, state config.GlobalState) (config.GlobalState, error) {
	if a.TencentRestore != nil {
		return a.TencentRestore(ctx, stage, state)
	}
	if !requiresTencentRestore(state) {
		return state, nil
	}
	return a.restoreTencentState(ctx, state)
}

func requiresTencentRestore(state config.GlobalState) bool {
	identity := state.Identity
	return strings.TrimSpace(identity.Endpoint) != "" ||
		strings.TrimSpace(state.TencentInstallPath) != "" ||
		len(state.ProjectBindings) > 0
}

// restoreTencentState restores the external sidecar before local state is
// committed. Backups deliberately omit Docker volumes and secrets, so this
// path reuses the current managed deployment when healthy or runs the same
// managed Linux bootstrap used by tencent-memory init. It then verifies the
// Baron identity and each recorded project agent without creating replacements.
func (a *App) restoreTencentState(ctx context.Context, state config.GlobalState) (config.GlobalState, error) {
	globalPath, err := a.globalPath()
	if err != nil {
		return state, err
	}
	endpoint := firstNonEmptyString(os.Getenv("BARON_TENCENT_ENDPOINT"), state.Identity.Endpoint, "http://127.0.0.1:8420")
	hubEndpoint := firstNonEmptyString(os.Getenv("BARON_TENCENT_HUB_ENDPOINT"), state.Identity.HubEndpoint, "http://127.0.0.1:8125")
	proxyEndpoint := firstNonEmptyString(os.Getenv("BARON_TENCENT_PROXY_ENDPOINT"), "http://127.0.0.1:8096")
	knowledgeEndpoint := firstNonEmptyString(os.Getenv("BARON_TENCENT_KNOWLEDGE_ENDPOINT"), state.Identity.KnowledgeEndpoint, "http://127.0.0.1:8424")
	serviceID := firstNonEmptyString(os.Getenv("BARON_TENCENT_SERVICE_ID"), state.Identity.ServiceID, "default")
	deploymentRoot := restoreDeploymentRoot(globalPath, state)

	runtimeConfig, err := a.resolveTencentRuntimeConfig(deploymentRoot)
	if err != nil {
		return state, err
	}
	dockerReport, err := install.EnsureDocker(ctx, a.commandRunner(), install.DockerBootstrapOptions{})
	if err != nil {
		return state, err
	}

	adminKey := strings.TrimSpace(os.Getenv("BARON_TENCENT_ADMIN_KEY"))
	if adminKey == "" {
		if managedKey, keyErr := install.TencentAdminKey(deploymentRoot); keyErr == nil {
			adminKey = managedKey
		} else if !errors.Is(keyErr, os.ErrNotExist) {
			return state, keyErr
		}
	}
	client := tencent.NewClient(tencent.Config{
		Endpoint: endpoint, HubEndpoint: hubEndpoint, AdminKey: adminKey,
		ServiceID: serviceID, HTTPClient: a.HTTPClient,
	})
	health := func() error {
		if err := client.Health(ctx); err != nil {
			return err
		}
		if err := client.HealthAt(ctx, hubEndpoint); err != nil {
			return fmt.Errorf("Tencent MemoryHub unavailable: %w", err)
		}
		if err := client.HealthAt(ctx, proxyEndpoint); err != nil {
			return fmt.Errorf("Tencent proxy unavailable: %w", err)
		}
		if err := client.HealthAt(ctx, knowledgeEndpoint); err != nil {
			return fmt.Errorf("Tencent Knowledge Service unavailable: %w", err)
		}
		return nil
	}
	if healthErr := health(); healthErr != nil {
		if err := install.EnsureTencentDeployment(ctx, a.commandRunner(), install.TencentDeploymentOptions{
			Root: deploymentRoot, UseSudo: dockerReport.UsedSudo, PullLatest: true, Runtime: runtimeConfig,
		}); err != nil {
			return state, fmt.Errorf("Tencent services are unavailable and automatic restore deployment failed: %w", err)
		}
		if adminKey == "" {
			if managedKey, keyErr := install.TencentAdminKey(deploymentRoot); keyErr == nil {
				adminKey = managedKey
			} else {
				adminKey, keyErr = a.resolveTencentAdminKey(deploymentRoot)
				if keyErr != nil {
					return state, keyErr
				}
			}
		}
		client = tencent.NewClient(tencent.Config{
			Endpoint: endpoint, HubEndpoint: hubEndpoint, AdminKey: adminKey,
			ServiceID: serviceID, HTTPClient: a.HTTPClient,
		})
		if err := health(); err != nil {
			return state, err
		}
	}
	if adminKey == "" {
		adminKey, err = a.resolveTencentAdminKey(deploymentRoot)
		if err != nil {
			return state, err
		}
		client = tencent.NewClient(tencent.Config{
			Endpoint: endpoint, HubEndpoint: hubEndpoint, AdminKey: adminKey,
			ServiceID: serviceID, HTTPClient: a.HTTPClient,
		})
	}

	identity, provision, err := client.EnsureIdentityWithExistingUserKey(ctx,
		contracts.IdentitySpec{UserName: "baron", TeamName: "baron-projects"}, state.Identity.UserKey)
	if err != nil {
		return state, err
	}
	rollback := func(cause error) (config.GlobalState, error) {
		if rollbackErr := provision.Rollback(ctx); rollbackErr != nil {
			return state, fmt.Errorf("%v; restored Tencent identity rollback failed: %w", cause, rollbackErr)
		}
		return state, cause
	}
	identity.KnowledgeEndpoint = knowledgeEndpoint
	identityClient := tencent.NewClient(tencent.Config{
		Endpoint: endpoint, HubEndpoint: hubEndpoint, UserKey: identity.UserKey,
		ServiceID: serviceID, HTTPClient: a.HTTPClient,
	})
	if err := identityClient.VerifyAuth(ctx); err != nil {
		return rollback(err)
	}
	if state.Identity.TeamID != "" && state.Identity.TeamID != identity.TeamID {
		return rollback(fmt.Errorf("restored Tencent team mismatch: expected %s, got %s", state.Identity.TeamID, identity.TeamID))
	}
	if state.Identity.UserID != "" && state.Identity.UserID != identity.UserID {
		return rollback(fmt.Errorf("restored Tencent user mismatch: expected %s, got %s", state.Identity.UserID, identity.UserID))
	}
	if state.ProjectBindings == nil {
		state.ProjectBindings = map[string]contracts.ProjectBinding{}
	}
	for projectID, expected := range state.ProjectBindings {
		if strings.TrimSpace(projectID) == "" {
			return rollback(errors.New("restored Tencent project binding has an empty project ID"))
		}
		if expected.ProjectID != "" && expected.ProjectID != projectID {
			return rollback(fmt.Errorf("restored Tencent project binding key mismatch for %s", projectID))
		}
		binding, findErr := identityClient.FindProjectAgent(ctx, contracts.IsolationContext{
			ProjectID: projectID, TeamID: identity.TeamID, UserID: identity.UserID, ServiceID: identity.ServiceID,
		})
		if findErr != nil {
			return rollback(findErr)
		}
		if expected.AgentID != "" && expected.AgentID != binding.AgentID {
			return rollback(fmt.Errorf("restored Tencent agent mismatch for %s: expected %s, got %s", projectID, expected.AgentID, binding.AgentID))
		}
		state.ProjectBindings[projectID] = binding
	}
	state.Identity = identity
	state.TencentInstallPath = deploymentRoot
	provision.Commit()
	return state, nil
}

func restoreDeploymentRoot(globalPath string, state config.GlobalState) string {
	if value := strings.TrimSpace(os.Getenv("BARON_TENCENT_MEMORY_DIR")); value != "" {
		return value
	}
	if value := strings.TrimSpace(state.TencentInstallPath); value != "" {
		if _, err := os.Stat(value); err == nil {
			return value
		}
	}
	return filepath.Join(filepath.Dir(globalPath), "tencent-memory")
}
