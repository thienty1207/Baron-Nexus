package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/memory/tencent"
	"github.com/baron-shared-brain/baron/internal/storage"
)

const (
	maxSeedFiles       = 32
	maxSeedFileBytes   = 256 * 1024
	maxSeedTotalBytes  = 3 * 1024 * 1024
	defaultBranch      = "main"
	projectWikiPrefix  = "Baron Nexus project wiki"
	projectGraphPrefix = "Baron Nexus project code graph"
)

type RepositoryInfo struct {
	Remote string
	Branch string
	Commit string
}

type ProvisionOptions struct {
	Root            string
	ProjectID       string
	ProjectName     string
	Isolation       contracts.IsolationContext
	Core            *tencent.Client
	Knowledge       *tencent.KnowledgeClient
	ServiceURL      string
	Store           *storage.Store
	Repository      RepositoryInfo
	Now             func() time.Time
	ReadinessBudget time.Duration
	PollInterval    time.Duration
	Secrets         []string
}

// ProvisionProject is best-effort for the remote side: local setup has already
// persisted the project identity. A returned registry always records the last
// known state and a safe, redacted error when Tencent is unavailable.
func ProvisionProject(ctx context.Context, options ProvisionOptions) (storage.KnowledgeRegistry, error) {
	if options.Store == nil {
		return storage.KnowledgeRegistry{}, errors.New("knowledge provisioning store is required")
	}
	if strings.TrimSpace(options.ProjectID) == "" {
		return storage.KnowledgeRegistry{}, errors.New("knowledge provisioning project ID is required")
	}
	registry, err := options.Store.GetKnowledgeRegistry(ctx, options.ProjectID)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, os.ErrNotExist) {
		registry = storage.KnowledgeRegistry{ProjectID: options.ProjectID}
	} else if err != nil {
		return storage.KnowledgeRegistry{}, err
	}
	previousCommit := registry.LastSyncCommit
	registry.ProjectID = options.ProjectID
	registry.TeamID = options.Isolation.TeamID
	registry.UserID = options.Isolation.UserID
	registry.AgentID = options.Isolation.AgentID
	info := options.Repository
	if info.Branch == "" && info.Remote == "" && info.Commit == "" {
		info = InspectRepository(ctx, options.Root)
	}
	registry.Repository = info.Remote
	registry.Branch = firstNonEmpty(info.Branch, defaultBranch)
	if info.Commit != "" {
		if previousCommit != "" && previousCommit != info.Commit {
			registry.SupersededBy = info.Commit
			registry.ConflictStatus = "superseded"
		} else if registry.ConflictStatus == "" {
			registry.ConflictStatus = "none"
		}
		registry.LastSyncCommit = info.Commit
	}
	registry.ServiceURL = firstNonEmpty(options.ServiceURL, knowledgeServiceURL(options.Knowledge))
	if err := options.Store.UpsertKnowledgeRegistry(ctx, registry); err != nil {
		return registry, err
	}
	readinessBudget, pollInterval := readinessSettings(options)
	pendingMessage := ""

	if options.Knowledge == nil {
		return registry, recordProvisionError(ctx, options.Store, registry, "Tencent Knowledge client is not configured")
	}
	if err := validateProvisioningIsolation(options.Isolation); err != nil {
		return registry, recordProvisionError(ctx, options.Store, registry, err.Error())
	}

	wikiName := projectWikiPrefix + " " + options.ProjectID
	wiki, wikiErr := ensureWiki(ctx, options, registry, wikiName)
	if wikiErr != nil {
		return registry, recordProvisionError(ctx, options.Store, registry, wikiErr.Error())
	}
	registry.WikiID = firstNonEmpty(wiki.ID, wiki.WikiID)
	registry.WikiStatus = firstNonEmpty(wiki.Status, "ready")
	registry.WikiIngestStatus = "pending"
	if options.Core != nil {
		metadata, metadataErr := options.Core.CreateKnowledgeMetadata(ctx, options.Isolation, tencent.KnowledgeMetadata{
			KnowledgeID: registry.WikiID, Type: "wiki", Name: wikiName, Summary: "Baron Nexus project documentation",
			ServiceURL: registry.ServiceURL, TeamID: options.Isolation.TeamID, UserID: options.Isolation.UserID,
			AgentID: options.Isolation.AgentID, ProjectID: options.ProjectID, WikiID: registry.WikiID,
		})
		if metadataErr != nil {
			_ = enqueueKnowledgeRetry(ctx, options.Store, registry, storage.QueueOperationMetadataRepair, "wiki_metadata")
			return registry, recordProvisionError(ctx, options.Store, registry, metadataErr.Error())
		}
		registry.WikiMetadataID = firstNonEmpty(metadata.KnowledgeID, metadata.ID)
	}
	seedFiles, seedErr := CollectSeedFilesWithSecrets(options.Root, options.ProjectName, options.Secrets)
	if seedErr != nil {
		return registry, recordProvisionError(ctx, options.Store, registry, seedErr.Error())
	}
	if _, err := options.Knowledge.WriteWikiRaw(ctx, options.Isolation, registry.WikiID, seedFiles); err != nil {
		_ = enqueueKnowledgeRetry(ctx, options.Store, registry, storage.QueueOperationWikiIngest, "wiki_raw_write")
		return registry, recordProvisionError(ctx, options.Store, registry, err.Error())
	}
	if _, err := options.Knowledge.IngestWiki(ctx, options.Isolation, registry.WikiID); err != nil {
		_ = enqueueKnowledgeRetry(ctx, options.Store, registry, storage.QueueOperationWikiIngest, "wiki_ingest")
		return registry, recordProvisionError(ctx, options.Store, registry, err.Error())
	}
	registry.WikiIngestStatus = "pending"
	if wikiReady, waitErr := waitForWiki(ctx, options.Knowledge, options.Isolation, registry.WikiID, readinessBudget, pollInterval); waitErr == nil {
		registry.WikiStatus = firstNonEmpty(wikiReady.Status, "ready")
		registry.WikiIngestStatus = "ready"
		if version := firstNonEmpty(wikiReady.Version, wikiReady.LastSyncAt); version != "" {
			registry.WikiIngestVersion = version
		}
	} else if errors.Is(waitErr, context.DeadlineExceeded) {
		pendingMessage = "Wiki ingest is still pending; baron repair will poll it again"
		registry.WikiStatus = "pending"
		_ = enqueueKnowledgeRetry(ctx, options.Store, registry, storage.QueueOperationWikiIngest, "wiki_readiness")
	} else {
		registry.WikiStatus = "failed"
		registry.WikiIngestStatus = "failed"
		_ = enqueueKnowledgeRetry(ctx, options.Store, registry, storage.QueueOperationWikiIngest, "wiki_readiness")
		return registry, recordProvisionError(ctx, options.Store, registry, waitErr.Error())
	}
	registry.LastError = ""
	if err := options.Store.UpsertKnowledgeRegistry(ctx, registry); err != nil {
		return registry, err
	}

	if info.Remote == "" {
		registry.CodeGraphStatus = "local_only"
		registry.CodeGraphSyncStatus = "not_applicable"
		registry.LastError = "no verified Git remote; CodeGraph deferred until a remote repository is configured"
		if err := options.Store.UpsertKnowledgeRegistry(ctx, registry); err != nil {
			return registry, err
		}
		return registry, nil
	}

	graphName := projectGraphPrefix + " " + options.ProjectID
	graph, graphErr := ensureCodeGraph(ctx, options, registry, graphName, info)
	if graphErr != nil {
		return registry, recordProvisionError(ctx, options.Store, registry, graphErr.Error())
	}
	registry.CodeGraphID = firstNonEmpty(graph.ID, graph.CodeGraphID)
	registry.CodeGraphStatus = firstNonEmpty(graph.Status, "pending")
	registry.CodeGraphSyncStatus = "pending"
	registry.CodeGraphCommit = firstNonEmpty(graph.CommitHash, info.Commit, registry.CodeGraphCommit)
	if options.Core != nil {
		metadata, metadataErr := options.Core.CreateKnowledgeMetadata(ctx, options.Isolation, tencent.KnowledgeMetadata{
			KnowledgeID: registry.CodeGraphID, Type: "code-graph", Name: graphName, Summary: "Baron Nexus project CodeGraph",
			ServiceURL: registry.ServiceURL, TeamID: options.Isolation.TeamID, UserID: options.Isolation.UserID,
			AgentID: options.Isolation.AgentID, ProjectID: options.ProjectID, CodeGraphID: registry.CodeGraphID,
			RepoURL: info.Remote, Repository: info.Remote, Branch: registry.Branch, Commit: registry.LastSyncCommit,
		})
		if metadataErr != nil {
			_ = enqueueKnowledgeRetry(ctx, options.Store, registry, storage.QueueOperationMetadataRepair, "codegraph_metadata")
			return registry, recordProvisionError(ctx, options.Store, registry, metadataErr.Error())
		}
		registry.CodeGraphMetadataID = firstNonEmpty(metadata.KnowledgeID, metadata.ID)
	}
	if _, err := options.Knowledge.SyncCodeGraph(ctx, options.Isolation, registry.CodeGraphID); err != nil {
		_ = enqueueKnowledgeRetry(ctx, options.Store, registry, storage.QueueOperationCodeGraphSync, "codegraph_sync")
		return registry, recordProvisionError(ctx, options.Store, registry, err.Error())
	}
	if graphReady, waitErr := waitForCodeGraph(ctx, options.Knowledge, options.Isolation, registry.CodeGraphID, readinessBudget, pollInterval); waitErr == nil {
		registry.CodeGraphStatus = firstNonEmpty(graphReady.Status, "ready")
		registry.CodeGraphSyncStatus = "ready"
		registry.CodeGraphCommit = firstNonEmpty(graphReady.CommitHash, info.Commit, registry.CodeGraphCommit)
	} else if errors.Is(waitErr, context.DeadlineExceeded) {
		pendingMessage = firstNonEmpty(pendingMessage, "CodeGraph indexing is still pending; baron repair will poll it again")
		registry.CodeGraphStatus = "pending"
		_ = enqueueKnowledgeRetry(ctx, options.Store, registry, storage.QueueOperationCodeGraphSync, "codegraph_readiness")
	} else {
		registry.CodeGraphStatus = "failed"
		registry.CodeGraphSyncStatus = "failed"
		_ = enqueueKnowledgeRetry(ctx, options.Store, registry, storage.QueueOperationCodeGraphSync, "codegraph_readiness")
		return registry, recordProvisionError(ctx, options.Store, registry, waitErr.Error())
	}
	registry.LastError = pendingMessage
	if err := options.Store.UpsertKnowledgeRegistry(ctx, registry); err != nil {
		return registry, err
	}
	return registry, nil
}

func readinessSettings(options ProvisionOptions) (time.Duration, time.Duration) {
	budget := options.ReadinessBudget
	if budget <= 0 {
		budget = 750 * time.Millisecond
	}
	interval := options.PollInterval
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	return budget, interval
}

func waitForWiki(ctx context.Context, client *tencent.KnowledgeClient, isolation contracts.IsolationContext, wikiID string, budget, interval time.Duration) (tencent.KnowledgeAsset, error) {
	readinessCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return client.WaitWikiReady(readinessCtx, isolation, wikiID, interval)
}

func waitForCodeGraph(ctx context.Context, client *tencent.KnowledgeClient, isolation contracts.IsolationContext, graphID string, budget, interval time.Duration) (tencent.KnowledgeAsset, error) {
	readinessCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return client.WaitCodeGraphReady(readinessCtx, isolation, graphID, interval)
}

func ensureWiki(ctx context.Context, options ProvisionOptions, registry storage.KnowledgeRegistry, name string) (tencent.KnowledgeAsset, error) {
	if registry.WikiID != "" {
		asset, err := options.Knowledge.GetWiki(ctx, options.Isolation, registry.WikiID)
		if err == nil {
			return asset, nil
		}
	}
	assets, err := options.Knowledge.ListWikis(ctx, options.Isolation, "", 100, 0)
	if err != nil {
		return tencent.KnowledgeAsset{}, err
	}
	var matches []tencent.KnowledgeAsset
	for _, asset := range assets {
		if asset.Name == name {
			matches = append(matches, asset)
		}
	}
	if len(matches) > 1 {
		return tencent.KnowledgeAsset{}, fmt.Errorf("ambiguous Tencent Wiki assets for project %s", options.ProjectID)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return options.Knowledge.CreateWiki(ctx, options.Isolation, name)
}

func ensureCodeGraph(ctx context.Context, options ProvisionOptions, registry storage.KnowledgeRegistry, name string, info RepositoryInfo) (tencent.KnowledgeAsset, error) {
	if registry.CodeGraphID != "" {
		asset, err := options.Knowledge.GetCodeGraph(ctx, options.Isolation, registry.CodeGraphID)
		if err == nil {
			return asset, nil
		}
	}
	assets, err := options.Knowledge.ListCodeGraphs(ctx, options.Isolation, "", 100, 0)
	if err != nil {
		return tencent.KnowledgeAsset{}, err
	}
	var matches []tencent.KnowledgeAsset
	for _, asset := range assets {
		if (asset.Name == name || asset.RepoURL == info.Remote) && (asset.Branch == "" || asset.Branch == info.Branch) {
			matches = append(matches, asset)
		}
	}
	if len(matches) > 1 {
		return tencent.KnowledgeAsset{}, fmt.Errorf("ambiguous Tencent CodeGraph assets for project %s", options.ProjectID)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return options.Knowledge.CreateCodeGraph(ctx, options.Isolation, info.Remote, firstNonEmpty(info.Branch, defaultBranch), name)
}

func recordProvisionError(ctx context.Context, store *storage.Store, registry storage.KnowledgeRegistry, message string) error {
	registry.LastError = config.Redact(message, nil)
	if err := store.UpsertKnowledgeRegistry(ctx, registry); err != nil {
		return fmt.Errorf("knowledge provisioning failed: %s; persist registry failure: %w", registry.LastError, err)
	}
	return errors.New(registry.LastError)
}

func enqueueKnowledgeRetry(ctx context.Context, store *storage.Store, registry storage.KnowledgeRegistry, operation, action string) error {
	payload, err := json.Marshal(map[string]string{
		"project_id": registry.ProjectID, "team_id": registry.TeamID, "user_id": registry.UserID,
		"agent_id": registry.AgentID, "wiki_id": registry.WikiID, "code_graph_id": registry.CodeGraphID,
		"action": action,
	})
	if err != nil {
		return err
	}
	_, err = store.EnqueueSync(ctx, storage.QueueItem{
		ProjectID: registry.ProjectID, Operation: operation,
		IdempotencyKey: operation + ":" + registry.ProjectID + ":" + action,
		Payload:        payload,
	})
	return err
}

func validateProvisioningIsolation(isolation contracts.IsolationContext) error {
	if err := isolation.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(isolation.UserID) == "" {
		return errors.New("user_id is required for knowledge provisioning")
	}
	return nil
}

func knowledgeServiceURL(client *tencent.KnowledgeClient) string {
	if client == nil {
		return ""
	}
	// The client intentionally does not expose credentials or its internal
	// HTTP transport. The endpoint is supplied by the caller in production;
	// this helper is replaced by ProvisionOptions.ServiceURL when needed.
	return ""
}

func responseStatus(data json.RawMessage, fallback string) string {
	var value struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(data, &value) == nil && strings.TrimSpace(value.Status) != "" {
		return value.Status
	}
	return fallback
}

func InspectRepository(ctx context.Context, root string) RepositoryInfo {
	if strings.TrimSpace(root) == "" {
		return RepositoryInfo{}
	}
	remote := gitOutput(ctx, root, "config", "--get", "remote.origin.url")
	branch := gitOutput(ctx, root, "symbolic-ref", "--short", "HEAD")
	if branch == "" {
		branch = gitOutput(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	}
	commit := gitOutput(ctx, root, "rev-parse", "HEAD")
	return RepositoryInfo{Remote: sanitizeRemote(remote), Branch: branch, Commit: commit}
}

func gitOutput(ctx context.Context, root string, args ...string) string {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	data, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func sanitizeRemote(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		if strings.Contains(value, "@") || strings.Contains(value, "token") || strings.Contains(value, "secret") {
			return ""
		}
		return value
	}
	if parsed.User != nil {
		parsed.User = nil
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func CollectSeedFiles(root, projectName string) ([]tencent.KnowledgeFile, error) {
	return CollectSeedFilesWithSecrets(root, projectName, nil)
}

func CollectSeedFilesWithSecrets(root, projectName string, secrets []string) ([]tencent.KnowledgeFile, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("knowledge seed root is required")
	}
	type candidate struct {
		path string
		data string
	}
	var candidates []candidate
	total := 0
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative != "." && (relative == ".git" || relative == ".baron" || strings.HasPrefix(filepath.Base(relative), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(candidates) >= maxSeedFiles || total >= maxSeedTotalBytes || !isSeedPath(relative) || ignoredByGit(context.Background(), root, relative) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if len(data) == 0 {
			return nil
		}
		if len(data) > maxSeedFileBytes {
			data = data[:maxSeedFileBytes]
		}
		data = []byte(config.Redact(string(data), secrets))
		if total+len(data) > maxSeedTotalBytes {
			data = data[:maxSeedTotalBytes-total]
		}
		if len(data) == 0 {
			return nil
		}
		candidates = append(candidates, candidate{path: relative, data: string(data)})
		total += len(data)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })
	files := make([]tencent.KnowledgeFile, 0, len(candidates)+1)
	for _, item := range candidates {
		files = append(files, tencent.KnowledgeFile{Filename: item.path, Content: item.data})
	}
	if len(files) == 0 {
		files = append(files, tencent.KnowledgeFile{Filename: "baron-project.md", Content: "# " + firstNonEmpty(projectName, "Project") + "\n\nProvisioned by Baron Nexus.\n"})
	}
	return files, nil
}

func isSeedPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	lower := strings.ToLower(filepath.ToSlash(path))
	for _, blocked := range []string{".env", ".pem", ".key", "id_rsa", "secret", "credential", "password", "token"} {
		if strings.Contains(base, blocked) {
			return false
		}
	}
	if strings.HasPrefix(lower, "docs/") || strings.HasPrefix(lower, "doc/") {
		return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".txt")
	}
	return strings.HasPrefix(base, "readme") || strings.Contains(base, "architecture") || strings.Contains(base, "design") || strings.Contains(base, "changelog") || strings.HasPrefix(base, "adr-")
}

func ignoredByGit(ctx context.Context, root, relative string) bool {
	command := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-q", "--", filepath.FromSlash(relative))
	return command.Run() == nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
