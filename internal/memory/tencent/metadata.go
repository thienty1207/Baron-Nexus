package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

// KnowledgeMetadata is the MemoryCore management-plane record for a Wiki,
// CodeGraph, or another knowledge source. Content stays in MemoryKnowledge;
// this record only binds the asset to its owner and service location.
type KnowledgeMetadata struct {
	KnowledgeID string          `json:"knowledge_id,omitempty"`
	ID          string          `json:"id,omitempty"`
	Type        string          `json:"type,omitempty"`
	Name        string          `json:"name,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	ServiceURL  string          `json:"service_url,omitempty"`
	TeamID      string          `json:"team_id,omitempty"`
	UserID      string          `json:"user_id,omitempty"`
	AgentID     string          `json:"agent_id,omitempty"`
	ProjectID   string          `json:"project_id,omitempty"`
	WikiID      string          `json:"wiki_id,omitempty"`
	CodeGraphID string          `json:"code_graph_id,omitempty"`
	RepoURL     string          `json:"repo_url,omitempty"`
	Repository  string          `json:"repository,omitempty"`
	Branch      string          `json:"branch,omitempty"`
	Commit      string          `json:"commit,omitempty"`
	Status      string          `json:"status,omitempty"`
	Version     string          `json:"version,omitempty"`
	UpdatedAt   string          `json:"updated_at,omitempty"`
	Raw         json.RawMessage `json:"-"`
}

type KnowledgeMetadataPage struct {
	Items []KnowledgeMetadata
	Total int
}

var allowedKnowledgeMetadataPaths = map[string]bool{
	"/v3/knowledge/create": true,
	"/v3/knowledge/get":    true,
	"/v3/knowledge/update": true,
	"/v3/knowledge/delete": true,
	"/v3/knowledge/list":   true,
}

// MetadataOperation is an allowlisted management-plane escape hatch. It is
// deliberately separate from MemoryOperation so a caller cannot accidentally
// send content-plane data to the metadata router.
func (c *Client) MetadataOperation(ctx context.Context, isolation contracts.IsolationContext, path string, body map[string]any) (KnowledgeResult, error) {
	if !allowedKnowledgeMetadataPaths[path] {
		return KnowledgeResult{}, fmt.Errorf("Tencent knowledge metadata path %q is not allowlisted", path)
	}
	if err := validateIsolation(isolation); err != nil {
		return KnowledgeResult{}, err
	}
	payload := cloneMap(body)
	for key, value := range map[string]string{
		"team_id": isolation.TeamID, "user_id": isolation.UserID,
		"agent_id": isolation.AgentID, "project_id": isolation.ProjectID,
	} {
		if value != "" {
			payload[key] = value
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return KnowledgeResult{}, err
	}
	if len(encoded) > 256*1024 {
		return KnowledgeResult{}, errors.New("Tencent knowledge metadata request is larger than the 256 KiB safety bound")
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, json.RawMessage(encoded), &raw); err != nil {
		return KnowledgeResult{}, err
	}
	return decodeKnowledgeResult(raw)
}

func (c *Client) CreateKnowledgeMetadata(ctx context.Context, isolation contracts.IsolationContext, metadata KnowledgeMetadata) (KnowledgeMetadata, error) {
	if strings.TrimSpace(metadata.Type) == "" {
		return KnowledgeMetadata{}, errors.New("knowledge metadata type is required")
	}
	if strings.TrimSpace(metadata.KnowledgeID) == "" && strings.TrimSpace(metadata.ID) == "" {
		return KnowledgeMetadata{}, errors.New("knowledge metadata knowledge_id is required")
	}
	result, err := c.MetadataOperation(ctx, isolation, "/v3/knowledge/create", structToMap(metadata))
	if err != nil {
		return KnowledgeMetadata{}, err
	}
	return metadataFromResult(result)
}

func (c *Client) GetKnowledgeMetadata(ctx context.Context, isolation contracts.IsolationContext, knowledgeID string) (KnowledgeMetadata, error) {
	if strings.TrimSpace(knowledgeID) == "" {
		return KnowledgeMetadata{}, errors.New("knowledge metadata knowledge_id is required")
	}
	result, err := c.MetadataOperation(ctx, isolation, "/v3/knowledge/get", map[string]any{"knowledge_id": knowledgeID})
	if err != nil {
		return KnowledgeMetadata{}, err
	}
	return metadataFromResult(result)
}

func (c *Client) UpdateKnowledgeMetadata(ctx context.Context, isolation contracts.IsolationContext, metadata KnowledgeMetadata) (KnowledgeMetadata, error) {
	if strings.TrimSpace(metadata.KnowledgeID) == "" && strings.TrimSpace(metadata.ID) == "" {
		return KnowledgeMetadata{}, errors.New("knowledge metadata knowledge_id is required")
	}
	result, err := c.MetadataOperation(ctx, isolation, "/v3/knowledge/update", structToMap(metadata))
	if err != nil {
		return KnowledgeMetadata{}, err
	}
	return metadataFromResult(result)
}

func (c *Client) DeleteKnowledgeMetadata(ctx context.Context, isolation contracts.IsolationContext, knowledgeIDs []string) (KnowledgeResult, error) {
	if len(knowledgeIDs) == 0 {
		return KnowledgeResult{}, errors.New("at least one knowledge_id is required")
	}
	return c.MetadataOperation(ctx, isolation, "/v3/knowledge/delete", map[string]any{"knowledge_ids": boundedIDs(knowledgeIDs)})
}

func (c *Client) ListKnowledgeMetadata(ctx context.Context, isolation contracts.IsolationContext, typeName string, limit int) (KnowledgeMetadataPage, error) {
	body := map[string]any{"limit": boundedKnowledgeLimit(limit)}
	if typeName != "" {
		body["type"] = typeName
	}
	result, err := c.MetadataOperation(ctx, isolation, "/v3/knowledge/list", body)
	if err != nil {
		return KnowledgeMetadataPage{}, err
	}
	return metadataPageFromResult(result)
}

func metadataFromResult(result KnowledgeResult) (KnowledgeMetadata, error) {
	var metadata KnowledgeMetadata
	if err := json.Unmarshal(result.Data, &metadata); err != nil {
		var wrapper struct {
			Knowledge KnowledgeMetadata `json:"knowledge"`
			Item      KnowledgeMetadata `json:"item"`
		}
		if nestedErr := json.Unmarshal(result.Data, &wrapper); nestedErr != nil {
			return KnowledgeMetadata{}, fmt.Errorf("decode Tencent knowledge metadata: %w", err)
		}
		metadata = wrapper.Knowledge
		if metadata.KnowledgeID == "" && metadata.ID == "" {
			metadata = wrapper.Item
		}
	}
	metadata.KnowledgeID = firstNonEmpty(metadata.KnowledgeID, metadata.ID, metadata.WikiID, metadata.CodeGraphID)
	metadata.ID = firstNonEmpty(metadata.ID, metadata.KnowledgeID)
	metadata.Raw = append([]byte(nil), result.Data...)
	if metadata.KnowledgeID == "" {
		return KnowledgeMetadata{}, errors.New("Tencent knowledge metadata response contained no knowledge_id")
	}
	return metadata, nil
}

func metadataPageFromResult(result KnowledgeResult) (KnowledgeMetadataPage, error) {
	var wrapper struct {
		Items []KnowledgeMetadata `json:"items"`
		Total int                 `json:"total"`
	}
	if err := json.Unmarshal(result.Data, &wrapper); err != nil {
		var items []KnowledgeMetadata
		if nestedErr := json.Unmarshal(result.Data, &items); nestedErr != nil {
			return KnowledgeMetadataPage{}, fmt.Errorf("decode Tencent knowledge metadata list: %w", err)
		}
		wrapper.Items = items
	}
	for index := range wrapper.Items {
		wrapper.Items[index].KnowledgeID = firstNonEmpty(wrapper.Items[index].KnowledgeID, wrapper.Items[index].ID, wrapper.Items[index].WikiID, wrapper.Items[index].CodeGraphID)
		wrapper.Items[index].ID = firstNonEmpty(wrapper.Items[index].ID, wrapper.Items[index].KnowledgeID)
	}
	if wrapper.Total == 0 {
		wrapper.Total = len(wrapper.Items)
	}
	return KnowledgeMetadataPage{Items: wrapper.Items, Total: wrapper.Total}, nil
}

func structToMap(value any) map[string]any {
	data, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	if result == nil {
		return map[string]any{}
	}
	return result
}

func cloneMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source)+4)
	for key, value := range source {
		result[key] = value
	}
	return result
}

// CapabilitySet is the feature view used by setup/doctor. The server may
// return a richer list; unknown fields are intentionally ignored at this edge.
type CapabilitySet struct {
	Version  string          `json:"version,omitempty"`
	Features map[string]bool `json:"features,omitempty"`
	Raw      json.RawMessage `json:"-"`
}

func (c *Client) DiscoverCapabilities(ctx context.Context) (CapabilitySet, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, "/v3/meta/capabilities", map[string]any{}, &raw); err != nil {
		return CapabilitySet{}, fmt.Errorf("discover Tencent capabilities: %w", err)
	}
	result, err := decodeKnowledgeResult(raw)
	if err != nil {
		return CapabilitySet{}, err
	}
	var capabilities CapabilitySet
	if err := json.Unmarshal(result.Data, &capabilities); err != nil {
		return CapabilitySet{}, fmt.Errorf("decode Tencent capability response: %w", err)
	}
	if capabilities.Features == nil {
		capabilities.Features = map[string]bool{}
	}
	capabilities.Raw = append([]byte(nil), result.Data...)
	return capabilities, nil
}
