package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/baron-shared-brain/baron/internal/continuity"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/memory/tencent"
	"github.com/baron-shared-brain/baron/internal/storage"
)

type Retriever struct {
	Client   *tencent.KnowledgeClient
	Registry storage.KnowledgeRegistry
}

var _ continuity.KnowledgeBackend = (*Retriever)(nil)

func NewRetriever(client *tencent.KnowledgeClient, registry storage.KnowledgeRegistry) *Retriever {
	return &Retriever{Client: client, Registry: registry}
}

func (r *Retriever) Retrieve(ctx context.Context, isolation contracts.IsolationContext, query contracts.MemoryQuery) ([]continuity.KnowledgeCitation, error) {
	if r == nil || r.Client == nil {
		return nil, fmt.Errorf("Tencent Knowledge client is not configured")
	}
	if strings.TrimSpace(query.Text) == "" && !containsQueryKind(query, "session_start") {
		return nil, nil
	}
	var citations []continuity.KnowledgeCitation
	var failures []string
	if containsQueryKind(query, "session_start") {
		if r.Registry.WikiID != "" {
			asset, err := r.Client.GetWiki(ctx, isolation, r.Registry.WikiID)
			if err != nil {
				failures = append(failures, "wiki status: "+err.Error())
			} else {
				citations = append(citations, continuity.KnowledgeCitation{Source: "wiki-status", Reference: asset.ID, Content: "Wiki status=" + firstNonEmpty(asset.Status, "unknown"), Trust: "historical-reference-only", Freshness: firstNonEmpty(asset.Version, asset.LastSyncAt, asset.Status)})
			}
		}
		if r.Registry.CodeGraphID != "" {
			result, err := r.Client.StatusCodeGraph(ctx, isolation, r.Registry.CodeGraphID)
			if err != nil {
				failures = append(failures, "codegraph status: "+err.Error())
			} else if status, commit := decodeGraphStatus(result.Data); status != "" {
				citations = append(citations, continuity.KnowledgeCitation{Source: "codegraph-status", Reference: r.Registry.CodeGraphID, Content: "CodeGraph status=" + status, Trust: "historical-reference-only", Freshness: firstNonEmpty(commit, status)})
			}
		}
	}
	if r.Registry.WikiID != "" && strings.TrimSpace(query.Text) != "" {
		result, err := r.Client.SearchWiki(ctx, isolation, r.Registry.WikiID, query.Text, query.Limit)
		if err != nil {
			failures = append(failures, "wiki: "+err.Error())
		} else {
			citations = append(citations, decodeCitations(result.Data, "wiki")...)
		}
	}
	if r.Registry.WikiID != "" && wantsWikiGraph(query) && len(citations) < boundedCitationLimit(query.Limit) {
		result, err := r.Client.WikiGraph(ctx, isolation, r.Registry.WikiID)
		if err != nil {
			failures = append(failures, "wiki graph: "+err.Error())
		} else {
			citations = append(citations, decodeCitations(result.Data, "wiki-graph")...)
		}
	}
	if r.Registry.CodeGraphID != "" && len(citations) < boundedCitationLimit(query.Limit) {
		if len(query.Symbols) > 0 {
			for _, symbol := range boundedQueryValues(query.Symbols, 4, 512) {
				for _, lookup := range []struct {
					name string
					read func() (tencent.KnowledgeResult, error)
				}{
					{name: "callers", read: func() (tencent.KnowledgeResult, error) {
						return r.Client.Callers(ctx, isolation, r.Registry.CodeGraphID, symbol, query.Limit)
					}},
					{name: "callees", read: func() (tencent.KnowledgeResult, error) {
						return r.Client.Callees(ctx, isolation, r.Registry.CodeGraphID, symbol, query.Limit)
					}},
					{name: "impact", read: func() (tencent.KnowledgeResult, error) {
						return r.Client.Impact(ctx, isolation, r.Registry.CodeGraphID, symbol, 2)
					}},
				} {
					result, err := lookup.read()
					if err != nil {
						failures = append(failures, "codegraph "+lookup.name+": "+err.Error())
						continue
					}
					citations = append(citations, decodeCitations(result.Data, "codegraph-"+lookup.name)...)
					if len(citations) >= boundedCitationLimit(query.Limit) {
						break
					}
				}
				if len(citations) >= boundedCitationLimit(query.Limit) {
					break
				}
			}
		} else {
			for _, file := range boundedQueryValues(query.Files, 4, 1024) {
				result, err := r.Client.Files(ctx, isolation, r.Registry.CodeGraphID, file, "", "json", false, 2)
				if err != nil {
					failures = append(failures, "codegraph files: "+err.Error())
					continue
				}
				citations = append(citations, decodeCitations(result.Data, "codegraph-files")...)
				if len(citations) >= boundedCitationLimit(query.Limit) {
					break
				}
			}
			if strings.TrimSpace(query.Text) != "" && len(citations) < boundedCitationLimit(query.Limit) {
				result, err := r.Client.SearchCodeGraph(ctx, isolation, r.Registry.CodeGraphID, query.Text)
				if err != nil {
					failures = append(failures, "codegraph: "+err.Error())
				} else {
					citations = append(citations, decodeCitations(result.Data, "codegraph")...)
				}
			}
		}
	}
	limit := boundedCitationLimit(query.Limit)
	if len(citations) > limit {
		citations = citations[:limit]
	}
	if len(failures) > 0 {
		return citations, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return citations, nil
}

func wantsWikiGraph(query contracts.MemoryQuery) bool {
	for _, kind := range query.Kinds {
		if kind == "wiki_graph" || kind == "architecture" || kind == "dependency_graph" {
			return true
		}
	}
	return false
}

func boundedQueryValues(values []string, limit, maxLength int) []string {
	result := make([]string, 0, limit)
	seen := make(map[string]bool)
	for _, value := range values {
		value = truncateQueryValue(value, maxLength)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
		if len(result) == limit {
			break
		}
	}
	return result
}

func truncateQueryValue(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return value[:max]
	}
	return value
}

func containsQueryKind(query contracts.MemoryQuery, want string) bool {
	for _, kind := range query.Kinds {
		if kind == want {
			return true
		}
	}
	return false
}

func decodeGraphStatus(data json.RawMessage) (string, string) {
	var status struct {
		Status     string `json:"status"`
		CommitHash string `json:"commit_hash"`
		Commit     string `json:"commit"`
	}
	if json.Unmarshal(data, &status) != nil {
		return "", ""
	}
	return status.Status, firstNonEmpty(status.CommitHash, status.Commit)
}

func decodeCitations(data json.RawMessage, source string) []continuity.KnowledgeCitation {
	var values []map[string]any
	if json.Unmarshal(data, &values) != nil {
		var wrapper map[string]json.RawMessage
		if json.Unmarshal(data, &wrapper) != nil {
			return nil
		}
		for _, key := range []string{"items", "results", "nodes", "files", "matches"} {
			if raw := wrapper[key]; len(raw) > 0 && json.Unmarshal(raw, &values) == nil {
				break
			}
		}
	}
	result := make([]continuity.KnowledgeCitation, 0, len(values))
	for _, value := range values {
		content := firstString(value, "content", "text", "snippet", "summary", "code", "name")
		if content == "" {
			encoded, _ := json.Marshal(value)
			content = string(encoded)
		}
		reference := firstString(value, "ref", "path", "file", "symbol", "id", "node_id")
		result = append(result, continuity.KnowledgeCitation{Source: source, Reference: reference, Content: content, Trust: "historical-reference-only", Freshness: firstString(value, "updated_at", "commit", "version", "status")})
	}
	return result
}

func firstString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func boundedCitationLimit(limit int) int {
	if limit <= 0 {
		return 10
	}
	if limit > 20 {
		return 20
	}
	return limit
}
