package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
)

// KnowledgeResult is the bounded, envelope-aware result shared by Wiki,
// CodeGraph, tools, metadata, and Skill calls. Data is kept raw at the edge so
// callers can opt into the upstream schema without coupling the core to it.
type KnowledgeResult struct {
	Code           int
	Message        string
	RequestID      string
	Data           json.RawMessage
	HistoricalOnly bool
}

type KnowledgeAsset struct {
	ID          string          `json:"id,omitempty"`
	WikiID      string          `json:"wiki_id,omitempty"`
	CodeGraphID string          `json:"code_graph_id,omitempty"`
	TeamID      string          `json:"team_id,omitempty"`
	UserID      string          `json:"owner_user_id,omitempty"`
	Name        string          `json:"name,omitempty"`
	RepoURL     string          `json:"repo_url,omitempty"`
	RepoName    string          `json:"repo_name,omitempty"`
	Branch      string          `json:"branch,omitempty"`
	CommitHash  string          `json:"commit_hash,omitempty"`
	ServiceURL  string          `json:"service_url,omitempty"`
	Status      string          `json:"status,omitempty"`
	Version     string          `json:"version,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	SyncError   string          `json:"sync_error,omitempty"`
	LastSyncAt  string          `json:"last_sync_at,omitempty"`
	PageCount   int             `json:"page_count,omitempty"`
	Stats       json.RawMessage `json:"stats,omitempty"`
	Raw         json.RawMessage `json:"-"`
}

type KnowledgeFile struct {
	Filename string `json:"filename"`
	Content  string `json:"content,omitempty"`
}

type KnowledgePage struct {
	Ref     string `json:"ref"`
	Content string `json:"content,omitempty"`
}

type KnowledgeClient struct {
	client   *Client
	endpoint string
}

// KnowledgeAPI is the narrow typed surface used by project provisioning and
// context orchestration. Individual methods still return bounded raw data for
// response-schema additions made upstream.
type KnowledgeAPI interface {
	CreateWiki(context.Context, contracts.IsolationContext, string) (KnowledgeAsset, error)
	CreateCodeGraph(context.Context, contracts.IsolationContext, string, string, string) (KnowledgeAsset, error)
	IngestWiki(context.Context, contracts.IsolationContext, string) (KnowledgeResult, error)
	SyncCodeGraph(context.Context, contracts.IsolationContext, string) (KnowledgeResult, error)
	SearchWiki(context.Context, contracts.IsolationContext, string, string, int) (KnowledgeResult, error)
	SearchCodeGraph(context.Context, contracts.IsolationContext, string, string) (KnowledgeResult, error)
}

var _ KnowledgeAPI = (*KnowledgeClient)(nil)

func NewKnowledgeClient(cfg Config) *KnowledgeClient {
	client := NewClient(cfg)
	endpoint := cfg.KnowledgeEndpoint
	if endpoint == "" {
		endpoint = cfg.Endpoint
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(endpoint, "/v3") {
		endpoint += "/v3"
	}
	return &KnowledgeClient{client: client, endpoint: endpoint}
}

func (k *KnowledgeClient) call(ctx context.Context, isolation contracts.IsolationContext, path string, body map[string]any) (KnowledgeResult, error) {
	return k.callWithIsolation(ctx, isolation, path, body, true)
}

func (k *KnowledgeClient) callWithoutIsolationFields(ctx context.Context, isolation contracts.IsolationContext, path string, body map[string]any) (KnowledgeResult, error) {
	return k.callWithIsolation(ctx, isolation, path, body, false)
}

func (k *KnowledgeClient) callWithIsolation(ctx context.Context, isolation contracts.IsolationContext, path string, body map[string]any, includeIsolationFields bool) (KnowledgeResult, error) {
	if err := validateIsolation(isolation); err != nil {
		return KnowledgeResult{}, err
	}
	if body == nil {
		body = map[string]any{}
	}
	// Most Knowledge schemas accept the shared identity fields. A few
	// resource-status routes deliberately accept only their resource ID; keep
	// those narrow upstream contracts exact while authentication and the
	// validated project context remain enforced by the client boundary.
	if includeIsolationFields {
		for key, value := range map[string]string{
			"team_id":    isolation.TeamID,
			"user_id":    isolation.UserID,
			"agent_id":   isolation.AgentID,
			"project_id": isolation.ProjectID,
		} {
			if _, exists := body[key]; !exists && value != "" {
				body[key] = value
			}
		}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return KnowledgeResult{}, err
	}
	if len(data) > 1<<20 {
		return KnowledgeResult{}, errors.New("Tencent Knowledge request is larger than the 1 MiB safety bound")
	}
	var raw json.RawMessage
	if err := k.client.doEndpoint(ctx, k.endpoint, "POST", path, json.RawMessage(data), &raw); err != nil {
		return KnowledgeResult{}, err
	}
	return decodeKnowledgeResult(raw)
}

func decodeKnowledgeResult(raw json.RawMessage) (KnowledgeResult, error) {
	var envelope struct {
		Code      int             `json:"code"`
		Message   string          `json:"message"`
		RequestID string          `json:"request_id"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return KnowledgeResult{}, fmt.Errorf("decode Tencent Knowledge envelope: %w", err)
	}
	return KnowledgeResult{Code: envelope.Code, Message: envelope.Message, RequestID: envelope.RequestID, Data: envelope.Data, HistoricalOnly: true}, nil
}

func assetFromResult(result KnowledgeResult, idField string) (KnowledgeAsset, error) {
	var asset KnowledgeAsset
	if len(result.Data) == 0 || string(result.Data) == "null" {
		return asset, errors.New("Tencent Knowledge response contained no asset data")
	}
	if err := json.Unmarshal(result.Data, &asset); err != nil {
		return asset, fmt.Errorf("decode Tencent Knowledge asset: %w", err)
	}
	if asset.ID == "" {
		if idField == "wiki_id" {
			asset.ID = asset.WikiID
		} else if idField == "code_graph_id" {
			asset.ID = asset.CodeGraphID
		}
	}
	asset.Raw = append([]byte(nil), result.Data...)
	if asset.ID == "" {
		return asset, fmt.Errorf("Tencent Knowledge response contained no %s", idField)
	}
	return asset, nil
}

func assetsFromResult(result KnowledgeResult, idField string) ([]KnowledgeAsset, error) {
	var wrapper struct {
		Items []KnowledgeAsset `json:"items"`
	}
	if err := json.Unmarshal(result.Data, &wrapper); err == nil && wrapper.Items != nil {
		return normalizeKnowledgeAssets(wrapper.Items, idField), nil
	}
	var assets []KnowledgeAsset
	if err := json.Unmarshal(result.Data, &assets); err != nil {
		return nil, fmt.Errorf("decode Tencent Knowledge asset list: %w", err)
	}
	return normalizeKnowledgeAssets(assets, idField), nil
}

func normalizeKnowledgeAssets(assets []KnowledgeAsset, idField string) []KnowledgeAsset {
	for index := range assets {
		if assets[index].ID == "" {
			if idField == "wiki_id" {
				assets[index].ID = assets[index].WikiID
			} else {
				assets[index].ID = assets[index].CodeGraphID
			}
		}
		assets[index].Raw, _ = json.Marshal(assets[index])
	}
	return assets
}

func (k *KnowledgeClient) CreateWiki(ctx context.Context, isolation contracts.IsolationContext, name string) (KnowledgeAsset, error) {
	if strings.TrimSpace(name) == "" {
		return KnowledgeAsset{}, errors.New("wiki name is required")
	}
	result, err := k.call(ctx, isolation, "/wiki/create", map[string]any{"team_id": isolation.TeamID, "name": name})
	if err != nil {
		return KnowledgeAsset{}, err
	}
	return assetFromResult(result, "wiki_id")
}

func (k *KnowledgeClient) GetWiki(ctx context.Context, isolation contracts.IsolationContext, wikiID string) (KnowledgeAsset, error) {
	result, err := k.call(ctx, isolation, "/wiki/get", map[string]any{"wiki_id": wikiID})
	if err != nil {
		return KnowledgeAsset{}, err
	}
	return assetFromResult(result, "wiki_id")
}

func (k *KnowledgeClient) ListWikis(ctx context.Context, isolation contracts.IsolationContext, status string, limit, offset int) ([]KnowledgeAsset, error) {
	body := map[string]any{"team_id": isolation.TeamID, "limit": boundedKnowledgeLimit(limit), "offset": maxKnowledgeOffset(offset)}
	if status != "" {
		body["status"] = status
	}
	result, err := k.call(ctx, isolation, "/wiki/list", body)
	if err != nil {
		return nil, err
	}
	return assetsFromResult(result, "wiki_id")
}

func (k *KnowledgeClient) IngestWiki(ctx context.Context, isolation contracts.IsolationContext, wikiID string) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/wiki/ingest", map[string]any{"wiki_id": wikiID})
}

// WaitWikiReady polls the bounded Wiki asset status after an ingest request.
// The caller owns the context budget, so setup can leave a still-indexing Wiki
// pending locally instead of blocking the agent indefinitely.
func (k *KnowledgeClient) WaitWikiReady(ctx context.Context, isolation contracts.IsolationContext, wikiID string, interval time.Duration) (KnowledgeAsset, error) {
	if strings.TrimSpace(wikiID) == "" {
		return KnowledgeAsset{}, errors.New("wiki_id is required")
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for {
		result, err := k.call(ctx, isolation, "/wiki/get", map[string]any{"wiki_id": wikiID})
		if err != nil {
			return KnowledgeAsset{}, err
		}
		asset, assetErr := assetFromResult(result, "wiki_id")
		if assetErr != nil {
			// Some compatible Knowledge deployments return only status for a
			// readiness request. Preserve the known asset ID and continue polling
			// instead of turning that valid response into a permanent failure.
			asset = KnowledgeAsset{ID: wikiID, WikiID: wikiID, Status: knowledgeResponseStatus(result.Data, "pending")}
		}
		status := strings.ToLower(strings.TrimSpace(asset.Status))
		switch status {
		case "ready", "completed", "complete", "success", "succeeded":
			return asset, nil
		case "failed", "error", "cancelled", "canceled":
			return asset, fmt.Errorf("Tencent Wiki ingest failed: %s", asset.SyncError)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return KnowledgeAsset{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (k *KnowledgeClient) DeleteWikis(ctx context.Context, isolation contracts.IsolationContext, wikiIDs []string) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/wiki/delete", map[string]any{"wiki_ids": boundedIDs(wikiIDs)})
}

func (k *KnowledgeClient) ListWikiRaw(ctx context.Context, isolation contracts.IsolationContext, wikiID string) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/wiki/raw/ls", map[string]any{"wiki_id": wikiID})
}

func (k *KnowledgeClient) ReadWikiRaw(ctx context.Context, isolation contracts.IsolationContext, wikiID string, filenames []string) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/wiki/raw/read", map[string]any{"wiki_id": wikiID, "filenames": boundedIDs(filenames)})
}

func (k *KnowledgeClient) WriteWikiRaw(ctx context.Context, isolation contracts.IsolationContext, wikiID string, files []KnowledgeFile) (KnowledgeResult, error) {
	files = boundedFiles(files)
	if len(files) == 0 {
		return k.call(ctx, isolation, "/wiki/raw/write", map[string]any{"wiki_id": wikiID, "files": files})
	}
	var result KnowledgeResult
	for start := 0; start < len(files); start += 10 {
		end := start + 10
		if end > len(files) {
			end = len(files)
		}
		result, err := k.writeWikiRawBatch(ctx, isolation, wikiID, files[start:end])
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func (k *KnowledgeClient) writeWikiRawBatch(ctx context.Context, isolation contracts.IsolationContext, wikiID string, files []KnowledgeFile) (KnowledgeResult, error) {
	result, err := k.call(ctx, isolation, "/wiki/raw/write", map[string]any{"wiki_id": wikiID, "files": files})
	if err == nil || !strings.Contains(err.Error(), "ENOENT") || !hasNestedKnowledgeFilename(files) {
		return result, err
	}
	// Older Tencent Knowledge deployments create raw/sources but not the
	// intermediate directories for nested filenames. Preserve the useful
	// source path on current deployments, and retry once with a collision-safe
	// flat alias only for that known legacy failure.
	return k.call(ctx, isolation, "/wiki/raw/write", map[string]any{"wiki_id": wikiID, "files": flattenKnowledgeFiles(files)})
}

func hasNestedKnowledgeFilename(files []KnowledgeFile) bool {
	for _, file := range files {
		if strings.ContainsAny(file.Filename, `/\\`) {
			return true
		}
	}
	return false
}

func flattenKnowledgeFiles(files []KnowledgeFile) []KnowledgeFile {
	used := make(map[string]int, len(files))
	flat := make([]KnowledgeFile, 0, len(files))
	for _, file := range files {
		name := strings.ReplaceAll(strings.ReplaceAll(file.Filename, `\`, "/"), "./", "")
		name = strings.ReplaceAll(name, "/", "__")
		if name == "" {
			name = "baron-source"
		}
		used[name]++
		if used[name] > 1 {
			name = fmt.Sprintf("%s__%d", name, used[name])
		}
		flat = append(flat, KnowledgeFile{Filename: name, Content: file.Content})
	}
	return flat
}

func (k *KnowledgeClient) RemoveWikiRaw(ctx context.Context, isolation contracts.IsolationContext, wikiID string, filenames []string) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/wiki/raw/rm", map[string]any{"wiki_id": wikiID, "filenames": boundedIDs(filenames)})
}

func (k *KnowledgeClient) ListWikiPages(ctx context.Context, isolation contracts.IsolationContext, wikiID string) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/wiki/page/ls", map[string]any{"wiki_id": wikiID})
}

func (k *KnowledgeClient) ReadWikiPages(ctx context.Context, isolation contracts.IsolationContext, wikiID string, refs []string) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/wiki/page/read", map[string]any{"wiki_id": wikiID, "refs": boundedIDs(refs)})
}

func (k *KnowledgeClient) WriteWikiPages(ctx context.Context, isolation contracts.IsolationContext, wikiID string, pages []KnowledgePage) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/wiki/page/write", map[string]any{"wiki_id": wikiID, "pages": boundedPages(pages)})
}

func (k *KnowledgeClient) RemoveWikiPages(ctx context.Context, isolation contracts.IsolationContext, wikiID string, refs []string) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/wiki/page/rm", map[string]any{"wiki_id": wikiID, "refs": boundedIDs(refs)})
}

func (k *KnowledgeClient) WikiGraph(ctx context.Context, isolation contracts.IsolationContext, wikiID string) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/wiki/graph", map[string]any{"wiki_id": wikiID})
}

func (k *KnowledgeClient) SearchWiki(ctx context.Context, isolation contracts.IsolationContext, wikiID, query string, limit int) (KnowledgeResult, error) {
	if strings.TrimSpace(query) == "" {
		return KnowledgeResult{}, errors.New("wiki search query is required")
	}
	return k.call(ctx, isolation, "/wiki/search", map[string]any{"wiki_id": wikiID, "query": query, "limit": boundedKnowledgeLimit(limit)})
}

func (k *KnowledgeClient) CreateCodeGraph(ctx context.Context, isolation contracts.IsolationContext, repoURL, branch, repoName string) (KnowledgeAsset, error) {
	if strings.TrimSpace(repoURL) == "" {
		return KnowledgeAsset{}, errors.New("code graph repository URL is required")
	}
	if branch == "" {
		branch = "main"
	}
	body := map[string]any{"team_id": isolation.TeamID, "repo_url": repoURL, "branch": branch}
	if repoName != "" {
		body["repo_name"] = repoName
	}
	result, err := k.call(ctx, isolation, "/code-graph/create", body)
	if err != nil {
		return KnowledgeAsset{}, err
	}
	return assetFromResult(result, "code_graph_id")
}

func (k *KnowledgeClient) ListCodeGraphs(ctx context.Context, isolation contracts.IsolationContext, status string, limit, offset int) ([]KnowledgeAsset, error) {
	body := map[string]any{"team_id": isolation.TeamID, "limit": boundedKnowledgeLimit(limit), "offset": maxKnowledgeOffset(offset)}
	if status != "" {
		body["status"] = status
	}
	result, err := k.call(ctx, isolation, "/code-graph/list", body)
	if err != nil {
		return nil, err
	}
	return assetsFromResult(result, "code_graph_id")
}

func (k *KnowledgeClient) GetCodeGraph(ctx context.Context, isolation contracts.IsolationContext, graphID string) (KnowledgeAsset, error) {
	result, err := k.call(ctx, isolation, "/code-graph/get", map[string]any{"code_graph_id": graphID})
	if err != nil {
		return KnowledgeAsset{}, err
	}
	return assetFromResult(result, "code_graph_id")
}

func (k *KnowledgeClient) SyncCodeGraph(ctx context.Context, isolation contracts.IsolationContext, graphID string) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/code-graph/sync", map[string]any{"code_graph_id": graphID})
}

func (k *KnowledgeClient) DeleteCodeGraphs(ctx context.Context, isolation contracts.IsolationContext, graphIDs []string) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/code-graph/delete", map[string]any{"code_graph_ids": boundedIDs(graphIDs)})
}

func (k *KnowledgeClient) SearchCodeGraph(ctx context.Context, isolation contracts.IsolationContext, graphID, query string) (KnowledgeResult, error) {
	if strings.TrimSpace(query) == "" {
		return KnowledgeResult{}, errors.New("code graph search query is required")
	}
	return k.call(ctx, isolation, "/code-graph/search", map[string]any{"code_graph_id": graphID, "query": query, "kind": "any", "limit": 20})
}

func (k *KnowledgeClient) ExploreCodeGraph(ctx context.Context, isolation contracts.IsolationContext, graphID, query string, maxFiles int) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/code-graph/explore", map[string]any{"code_graph_id": graphID, "query": query, "maxFiles": boundedKnowledgeLimit(maxFiles)})
}

func (k *KnowledgeClient) Callers(ctx context.Context, isolation contracts.IsolationContext, graphID, symbol string, limit int) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/code-graph/callers", map[string]any{"code_graph_id": graphID, "symbol": symbol, "limit": boundedKnowledgeLimit(limit)})
}

func (k *KnowledgeClient) Callees(ctx context.Context, isolation contracts.IsolationContext, graphID, symbol string, limit int) (KnowledgeResult, error) {
	return k.call(ctx, isolation, "/code-graph/callees", map[string]any{"code_graph_id": graphID, "symbol": symbol, "limit": boundedKnowledgeLimit(limit)})
}

func (k *KnowledgeClient) Impact(ctx context.Context, isolation contracts.IsolationContext, graphID, symbol string, depth int) (KnowledgeResult, error) {
	if depth < 1 {
		depth = 2
	}
	if depth > 10 {
		depth = 10
	}
	return k.call(ctx, isolation, "/code-graph/impact", map[string]any{"code_graph_id": graphID, "symbol": symbol, "depth": depth})
}

func (k *KnowledgeClient) Node(ctx context.Context, isolation contracts.IsolationContext, graphID, symbol, file string, line int, includeCode bool) (KnowledgeResult, error) {
	body := map[string]any{"code_graph_id": graphID, "symbol": symbol, "includeCode": includeCode}
	if file != "" {
		body["file"] = file
	}
	if line > 0 {
		body["line"] = line
	}
	return k.call(ctx, isolation, "/code-graph/node", body)
}

func (k *KnowledgeClient) StatusCodeGraph(ctx context.Context, isolation contracts.IsolationContext, graphID string) (KnowledgeResult, error) {
	return k.callWithoutIsolationFields(ctx, isolation, "/code-graph/status", map[string]any{"code_graph_id": graphID})
}

func (k *KnowledgeClient) Files(ctx context.Context, isolation contracts.IsolationContext, graphID, path, pattern, format string, includeMetadata bool, maxDepth int) (KnowledgeResult, error) {
	body := map[string]any{"code_graph_id": graphID, "format": format, "includeMetadata": includeMetadata}
	if path != "" {
		body["path"] = path
	}
	if pattern != "" {
		body["pattern"] = pattern
	}
	if maxDepth > 0 {
		body["maxDepth"] = maxDepth
	}
	return k.call(ctx, isolation, "/code-graph/files", body)
}

func (k *KnowledgeClient) WaitCodeGraphReady(ctx context.Context, isolation contracts.IsolationContext, graphID string, interval time.Duration) (KnowledgeAsset, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for {
		result, err := k.StatusCodeGraph(ctx, isolation, graphID)
		if err != nil {
			// Compatible Tencent deployments may expose a narrow status route
			// that is temporarily unavailable while the asset GET remains
			// authoritative. Preserve the status error only if the asset lookup
			// cannot recover a machine-readable state as well.
			asset, getErr := k.GetCodeGraph(ctx, isolation, graphID)
			if getErr != nil {
				return KnowledgeAsset{}, err
			}
			result = KnowledgeResult{Data: asset.Raw}
		}
		asset, assetErr := assetFromResult(result, "code_graph_id")
		if assetErr != nil {
			asset = KnowledgeAsset{ID: graphID, CodeGraphID: graphID, Status: knowledgeResponseStatus(result.Data, "")}
		}
		if strings.TrimSpace(asset.Status) == "" {
			// The current Tencent status endpoint returns human-readable
			// Markdown with no status field. The GET endpoint carries the
			// structured lifecycle status and is safe to use as the fallback.
			if fetched, getErr := k.GetCodeGraph(ctx, isolation, graphID); getErr == nil {
				asset = fetched
			}
		}
		status := strings.ToLower(strings.TrimSpace(asset.Status))
		if status == "ready" || status == "completed" || status == "complete" || status == "success" || status == "succeeded" {
			return asset, nil
		}
		if status == "failed" || status == "error" || status == "cancelled" || status == "canceled" {
			return asset, fmt.Errorf("Tencent CodeGraph indexing failed: %s", asset.SyncError)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return KnowledgeAsset{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func knowledgeResponseStatus(data json.RawMessage, fallback string) string {
	var value struct {
		Status    string `json:"status"`
		SyncError string `json:"sync_error"`
	}
	if json.Unmarshal(data, &value) == nil {
		if strings.TrimSpace(value.Status) != "" {
			return value.Status
		}
	}
	return fallback
}

var allowedKnowledgeTools = map[string]bool{
	"wiki_search": true, "wiki_graph": true, "code_graph_search": true,
	"code_graph_explore": true, "code_graph_callers": true, "code_graph_callees": true,
	"code_graph_impact": true, "code_graph_node": true, "code_graph_files": true,
}

func (k *KnowledgeClient) ListTools(ctx context.Context, isolation contracts.IsolationContext, knowledgeIDs ...string) (KnowledgeResult, error) {
	knowledgeID := ""
	for _, candidate := range knowledgeIDs {
		if strings.TrimSpace(candidate) != "" {
			knowledgeID = strings.TrimSpace(candidate)
			break
		}
	}
	if knowledgeID == "" {
		return KnowledgeResult{}, errors.New("Tencent Knowledge tools discovery requires a knowledge_id")
	}
	return k.call(ctx, isolation, "/tools/list", map[string]any{"knowledge_id": knowledgeID})
}

func (k *KnowledgeClient) CallTool(ctx context.Context, isolation contracts.IsolationContext, knowledgeID, name string, arguments map[string]any) (KnowledgeResult, error) {
	if strings.TrimSpace(knowledgeID) == "" {
		return KnowledgeResult{}, errors.New("Tencent Knowledge tool call requires a knowledge_id")
	}
	if !allowedKnowledgeTools[name] {
		return KnowledgeResult{}, fmt.Errorf("Tencent Knowledge tool %q is not allowlisted", name)
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return KnowledgeResult{}, err
	}
	if len(encoded) > 64*1024 {
		return KnowledgeResult{}, errors.New("Tencent Knowledge tool arguments exceed the 64 KiB safety bound")
	}
	return k.call(ctx, isolation, "/tools/call", map[string]any{"knowledge_id": knowledgeID, "tool_name": name, "params": arguments})
}

func boundedKnowledgeLimit(value int) int {
	if value <= 0 {
		return 20
	}
	if value > 200 {
		return 200
	}
	return value
}

func maxKnowledgeOffset(value int) int {
	if value < 0 {
		return 0
	}
	if value > 1_000_000 {
		return 1_000_000
	}
	return value
}

func boundedIDs(values []string) []string {
	if len(values) > 100 {
		values = values[:100]
	}
	return append([]string(nil), values...)
}

func boundedFiles(values []KnowledgeFile) []KnowledgeFile {
	if len(values) > 50 {
		values = values[:50]
	}
	for index := range values {
		values[index].Content = config.Redact(values[index].Content, nil)
		if len(values[index].Content) > 512*1024 {
			values[index].Content = values[index].Content[:512*1024]
		}
	}
	return append([]KnowledgeFile(nil), values...)
}

func boundedPages(values []KnowledgePage) []KnowledgePage {
	if len(values) > 20 {
		values = values[:20]
	}
	for index := range values {
		values[index].Content = config.Redact(values[index].Content, nil)
		if len(values[index].Content) > 512*1024 {
			values[index].Content = values[index].Content[:512*1024]
		}
	}
	return append([]KnowledgePage(nil), values...)
}
