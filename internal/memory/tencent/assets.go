package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

const metadataPageSize = 100

// KnowledgeAssetRegistration describes the metadata-plane asset that mirrors a
// Wiki or CodeGraph in MemoryKnowledge. The asset ID is supplied by the
// knowledge service and is therefore stable across Baron reinstall/repair.
type KnowledgeAssetRegistration struct {
	AssetID      string
	AssetType    string
	Name         string
	OwnerUserID  string
	SourceType   string
	Description  string
	SourceRef    string
	Visibility   string
	Status       string
	ContentRef   string
	MetadataJSON string
}

type ManagedAsset struct {
	AssetID     string `json:"asset_id"`
	TeamID      string `json:"team_id"`
	AssetType   string `json:"asset_type"`
	Name        string `json:"name"`
	OwnerUserID string `json:"owner_user_id"`
	Visibility  string `json:"visibility"`
}

type AgentFixedAssetBinding struct {
	AssetID       string `json:"asset_id"`
	AssetType     string `json:"asset_type"`
	InjectionMode string `json:"injection_mode,omitempty"`
	Priority      int    `json:"priority,omitempty"`
	CreatedBy     string `json:"created_by"`
}

// EnsureKnowledgeAsset makes the metadata asset visible to Memory Hub. It is
// deliberately a create-if-missing operation: setup must not overwrite a
// record owned by another team/user or silently change its type.
func (c *Client) EnsureKnowledgeAsset(ctx context.Context, isolation contracts.IsolationContext, registration KnowledgeAssetRegistration) error {
	if strings.TrimSpace(registration.AssetID) == "" {
		return errors.New("Tencent knowledge asset ID is required")
	}
	if strings.TrimSpace(registration.AssetType) == "" {
		return errors.New("Tencent knowledge asset type is required")
	}
	if strings.TrimSpace(registration.Name) == "" {
		return errors.New("Tencent knowledge asset name is required")
	}
	if strings.TrimSpace(registration.OwnerUserID) == "" {
		return errors.New("Tencent knowledge asset owner is required")
	}
	if err := validateIsolation(isolation); err != nil {
		return err
	}
	if registration.OwnerUserID != isolation.UserID {
		return errors.New("Tencent knowledge asset owner must match the isolated user")
	}

	assets, err := c.listManagedAssets(ctx, isolation, "")
	if err != nil {
		return err
	}
	for _, asset := range assets {
		if asset.AssetID != registration.AssetID {
			continue
		}
		if asset.TeamID != "" && asset.TeamID != isolation.TeamID {
			return fmt.Errorf("Tencent knowledge asset %s belongs to another team", registration.AssetID)
		}
		if asset.AssetType != "" && asset.AssetType != registration.AssetType {
			return fmt.Errorf("Tencent knowledge asset %s has type %q, want %q", registration.AssetID, asset.AssetType, registration.AssetType)
		}
		if asset.OwnerUserID != "" && asset.OwnerUserID != registration.OwnerUserID {
			return fmt.Errorf("Tencent knowledge asset %s belongs to another owner", registration.AssetID)
		}
		return nil
	}

	payload := map[string]any{
		"asset_id":      registration.AssetID,
		"team_id":       isolation.TeamID,
		"asset_type":    registration.AssetType,
		"name":          registration.Name,
		"owner_user_id": registration.OwnerUserID,
		"source_type":   firstNonEmpty(registration.SourceType, "manual"),
		"visibility":    firstNonEmpty(registration.Visibility, "team"),
	}
	for key, value := range map[string]string{
		"description": registration.Description, "source_ref": registration.SourceRef,
		"status": registration.Status, "content_ref": registration.ContentRef,
		"metadata_json": registration.MetadataJSON,
	} {
		if strings.TrimSpace(value) != "" {
			payload[key] = value
		}
	}
	result, err := c.MetaOperation(ctx, isolation, "/v3/meta/asset/create", payload)
	if err != nil {
		return err
	}
	return metadataResultError("asset/create", result)
}

// EnsureAgentFixedAsset keeps all existing fixed assets and adds one
// knowledge binding only when it is absent. This avoids the destructive
// list-then-set behavior that could otherwise drop chat memory or skills.
func (c *Client) EnsureAgentFixedAsset(ctx context.Context, isolation contracts.IsolationContext, assetID, assetType string) error {
	if strings.TrimSpace(assetID) == "" {
		return errors.New("Tencent fixed asset ID is required")
	}
	if strings.TrimSpace(assetType) == "" {
		return errors.New("Tencent fixed asset type is required")
	}
	if strings.TrimSpace(isolation.AgentID) == "" {
		return errors.New("agent_id is required for Tencent fixed asset binding")
	}
	if err := validateIsolation(isolation); err != nil {
		return err
	}

	bindings, err := c.listAgentFixedAssets(ctx, isolation)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if binding.AssetID != assetID {
			continue
		}
		if binding.AssetType != "" && binding.AssetType != assetType {
			return fmt.Errorf("Tencent fixed asset %s has type %q, want %q", assetID, binding.AssetType, assetType)
		}
		return nil
	}

	for index := range bindings {
		if strings.TrimSpace(bindings[index].InjectionMode) == "" {
			bindings[index].InjectionMode = "summary"
		}
		if bindings[index].Priority == 0 {
			bindings[index].Priority = 50
		}
		if strings.TrimSpace(bindings[index].CreatedBy) == "" {
			bindings[index].CreatedBy = isolation.UserID
		}
	}
	bindings = append(bindings, AgentFixedAssetBinding{
		AssetID:       assetID,
		AssetType:     assetType,
		InjectionMode: "tool",
		Priority:      50,
		CreatedBy:     isolation.UserID,
	})
	result, err := c.MetaOperation(ctx, isolation, "/v3/meta/agent-fixed-asset/set", map[string]any{
		"agent_id": isolation.AgentID,
		"bindings": bindings,
	})
	if err != nil {
		return err
	}
	return metadataResultError("agent-fixed-asset/set", result)
}

func (c *Client) listManagedAssets(ctx context.Context, isolation contracts.IsolationContext, assetType string) ([]ManagedAsset, error) {
	var assets []ManagedAsset
	for pageNumber := 0; pageNumber < 1000; pageNumber++ {
		offset := pageNumber * metadataPageSize
		body := map[string]any{"team_id": isolation.TeamID, "limit": metadataPageSize, "offset": offset}
		if strings.TrimSpace(assetType) != "" {
			body["asset_type"] = assetType
		}
		result, err := c.MetaOperation(ctx, isolation, "/v3/meta/asset/list", body)
		if err != nil {
			return nil, err
		}
		if err := metadataResultError("asset/list", result); err != nil {
			return nil, err
		}
		page, err := decodeManagedAssetPage(result.Data)
		if err != nil {
			return nil, err
		}
		assets = append(assets, page.Items...)
		if len(page.Items) < metadataPageSize || (page.Total > 0 && len(assets) >= page.Total) {
			return assets, nil
		}
	}
	return nil, errors.New("Tencent managed asset pagination exceeded the safety bound")
}

func (c *Client) listAgentFixedAssets(ctx context.Context, isolation contracts.IsolationContext) ([]AgentFixedAssetBinding, error) {
	var bindings []AgentFixedAssetBinding
	for pageNumber := 0; pageNumber < 1000; pageNumber++ {
		offset := pageNumber * metadataPageSize
		result, err := c.MetaOperation(ctx, isolation, "/v3/meta/agent-fixed-asset/list", map[string]any{
			"agent_id": isolation.AgentID, "limit": metadataPageSize, "offset": offset,
		})
		if err != nil {
			return nil, err
		}
		if err := metadataResultError("agent-fixed-asset/list", result); err != nil {
			return nil, err
		}
		page, err := decodeAgentFixedAssetPage(result.Data)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, page.Items...)
		if len(page.Items) < metadataPageSize || (page.Total > 0 && len(bindings) >= page.Total) {
			return bindings, nil
		}
	}
	return nil, errors.New("Tencent fixed asset pagination exceeded the safety bound")
}

type managedAssetPage struct {
	Items []ManagedAsset
	Total int
}

type agentFixedAssetPage struct {
	Items []AgentFixedAssetBinding
	Total int
}

func decodeManagedAssetPage(data []byte) (managedAssetPage, error) {
	var wrapper struct {
		Items []ManagedAsset `json:"items"`
		Total int            `json:"total"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Items != nil {
		if wrapper.Total == 0 {
			wrapper.Total = len(wrapper.Items)
		}
		return managedAssetPage{Items: wrapper.Items, Total: wrapper.Total}, nil
	}
	var items []ManagedAsset
	if err := json.Unmarshal(data, &items); err != nil {
		return managedAssetPage{}, fmt.Errorf("decode Tencent managed asset list: %w", err)
	}
	return managedAssetPage{Items: items, Total: len(items)}, nil
}

func decodeAgentFixedAssetPage(data []byte) (agentFixedAssetPage, error) {
	var wrapper struct {
		Items []AgentFixedAssetBinding `json:"items"`
		Total int                      `json:"total"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Items != nil {
		if wrapper.Total == 0 {
			wrapper.Total = len(wrapper.Items)
		}
		return agentFixedAssetPage{Items: wrapper.Items, Total: wrapper.Total}, nil
	}
	var items []AgentFixedAssetBinding
	if err := json.Unmarshal(data, &items); err != nil {
		return agentFixedAssetPage{}, fmt.Errorf("decode Tencent fixed asset list: %w", err)
	}
	return agentFixedAssetPage{Items: items, Total: len(items)}, nil
}

func metadataResultError(operation string, result KnowledgeResult) error {
	if result.Code == 0 {
		return nil
	}
	message := strings.TrimSpace(result.Message)
	if message == "" {
		message = "unknown error"
	}
	return fmt.Errorf("Tencent metadata %s returned code %d: %s", operation, result.Code, message)
}
