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

// The management router is intentionally allowlisted. This covers the
// user/team/agent/task/asset/membership/access surfaces documented by the
// upstream v3 management plane without exposing arbitrary path construction.
var allowedMetaPaths = map[string]bool{}

func init() {
	for _, resource := range []string{"user", "user-key", "team", "agent", "task", "asset", "membership", "access"} {
		for _, action := range []string{"list", "get", "create", "update", "delete"} {
			allowedMetaPaths["/v3/meta/"+resource+"/"+action] = true
		}
	}
	allowedMetaPaths["/v3/meta/asset/list-accessible"] = true
	allowedMetaPaths["/v3/meta/agent-fixed-asset/list"] = true
	allowedMetaPaths["/v3/meta/agent-fixed-asset/set"] = true
	allowedMetaPaths["/v3/meta/auth/verify"] = true
	allowedMetaPaths["/v3/meta/capabilities"] = true
}

type MetadataEntity struct {
	ID          string          `json:"id,omitempty"`
	UserID      string          `json:"user_id,omitempty"`
	TeamID      string          `json:"team_id,omitempty"`
	AgentID     string          `json:"agent_id,omitempty"`
	TaskID      string          `json:"task_id,omitempty"`
	AssetID     string          `json:"asset_id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Type        string          `json:"type,omitempty"`
	ProjectID   string          `json:"project_id,omitempty"`
	Description string          `json:"description,omitempty"`
	Status      string          `json:"status,omitempty"`
	Raw         json.RawMessage `json:"-"`
}

// MetaOperation is a typed management-plane escape hatch for provisioning
// and repair. It never puts bearer/user keys in JSON and always carries the
// known ownership fields from the caller.
func (c *Client) MetaOperation(ctx context.Context, isolation contracts.IsolationContext, path string, body map[string]any) (KnowledgeResult, error) {
	if !allowedMetaPaths[path] {
		return KnowledgeResult{}, fmt.Errorf("Tencent metadata path %q is not allowlisted", path)
	}
	if path != "/v3/meta/auth/verify" {
		if strings.TrimSpace(isolation.TeamID) == "" {
			return KnowledgeResult{}, errors.New("team_id is required for Tencent metadata operations")
		}
		if strings.TrimSpace(isolation.UserID) == "" {
			return KnowledgeResult{}, errors.New("user_id is required for Tencent metadata operations")
		}
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
		return KnowledgeResult{}, errors.New("Tencent metadata request is larger than the 256 KiB safety bound")
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, json.RawMessage(encoded), &raw); err != nil {
		return KnowledgeResult{}, err
	}
	return decodeKnowledgeResult(raw)
}

func (c *Client) ListAccessibleAssets(ctx context.Context, isolation contracts.IsolationContext, visibility string, limit int) (KnowledgeResult, error) {
	body := map[string]any{"visibility": visibility, "limit": boundedKnowledgeLimit(limit)}
	return c.MetaOperation(ctx, isolation, "/v3/meta/asset/list-accessible", body)
}
