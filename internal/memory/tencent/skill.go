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

type Skill struct {
	ID          string `json:"id,omitempty"`
	SkillID     string `json:"skill_id,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	TeamID      string `json:"team_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	UserID      string `json:"user_id,omitempty"`
	Content     string `json:"content,omitempty"`
	Status      string `json:"status,omitempty"`
}

var allowedSkillPaths = map[string]bool{
	"/v3/skill/create": true, "/v3/skill/get": true, "/v3/skill/list": true,
	"/v3/skill/search": true, "/v3/skill/update": true, "/v3/skill/delete": true,
	"/v3/skill/version/list": true, "/v3/skill/version/get": true,
	"/v3/skill/resource/ls": true, "/v3/skill/resource/read": true,
	"/v3/skill/resource/write": true, "/v3/skill/resource/rm": true,
	"/v3/skill/extract": true,
}

// SkillOperation exposes the upstream Skill Memory surface while retaining
// the same strict project/team/agent/user isolation as the memory data plane.
func (c *Client) SkillOperation(ctx context.Context, isolation contracts.IsolationContext, path string, body map[string]any) (KnowledgeResult, error) {
	if !allowedSkillPaths[path] {
		return KnowledgeResult{}, fmt.Errorf("Tencent skill path %q is not allowlisted", path)
	}
	if err := validateIsolation(isolation); err != nil {
		return KnowledgeResult{}, err
	}
	payload := cloneMap(body)
	for key, value := range map[string]string{
		"team_id": isolation.TeamID, "agent_id": isolation.AgentID,
		"user_id": isolation.UserID, "project_id": isolation.ProjectID,
	} {
		if value != "" {
			payload[key] = value
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return KnowledgeResult{}, err
	}
	if len(encoded) > 1<<20 {
		return KnowledgeResult{}, errors.New("Tencent skill request is larger than the 1 MiB safety bound")
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, json.RawMessage(encoded), &raw); err != nil {
		return KnowledgeResult{}, err
	}
	return decodeKnowledgeResult(raw)
}

func (c *Client) CreateSkill(ctx context.Context, isolation contracts.IsolationContext, name, content string) (Skill, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(content) == "" {
		return Skill{}, errors.New("skill name and content are required")
	}
	result, err := c.SkillOperation(ctx, isolation, "/v3/skill/create", map[string]any{"name": name, "content": boundedSkillText(content, 512*1024)})
	return skillFromResult(result, err)
}

func (c *Client) GetSkill(ctx context.Context, isolation contracts.IsolationContext, skillID string) (Skill, error) {
	if strings.TrimSpace(skillID) == "" {
		return Skill{}, errors.New("skill_id is required")
	}
	result, err := c.SkillOperation(ctx, isolation, "/v3/skill/get", map[string]any{"skill_id": skillID})
	return skillFromResult(result, err)
}

func (c *Client) ListSkills(ctx context.Context, isolation contracts.IsolationContext, limit int) (KnowledgeResult, error) {
	return c.SkillOperation(ctx, isolation, "/v3/skill/list", map[string]any{"limit": boundedKnowledgeLimit(limit)})
}

func (c *Client) SearchSkills(ctx context.Context, isolation contracts.IsolationContext, query string, limit int) (KnowledgeResult, error) {
	if strings.TrimSpace(query) == "" {
		return KnowledgeResult{}, errors.New("skill search query is required")
	}
	return c.SkillOperation(ctx, isolation, "/v3/skill/search", map[string]any{"query": boundedSkillText(query, 4000), "limit": boundedKnowledgeLimit(limit)})
}

func (c *Client) UpdateSkill(ctx context.Context, isolation contracts.IsolationContext, skillID string, fields map[string]any) (KnowledgeResult, error) {
	if strings.TrimSpace(skillID) == "" {
		return KnowledgeResult{}, errors.New("skill_id is required")
	}
	body := cloneMap(fields)
	body["skill_id"] = skillID
	if content, ok := body["content"].(string); ok {
		body["content"] = boundedSkillText(content, 512*1024)
	}
	return c.SkillOperation(ctx, isolation, "/v3/skill/update", body)
}

func (c *Client) DeleteSkills(ctx context.Context, isolation contracts.IsolationContext, skillIDs []string) (KnowledgeResult, error) {
	if len(skillIDs) == 0 {
		return KnowledgeResult{}, errors.New("at least one skill_id is required")
	}
	return c.SkillOperation(ctx, isolation, "/v3/skill/delete", map[string]any{"skill_ids": boundedIDs(skillIDs)})
}

func (c *Client) ListSkillVersions(ctx context.Context, isolation contracts.IsolationContext, skillID string, limit int) (KnowledgeResult, error) {
	return c.SkillOperation(ctx, isolation, "/v3/skill/version/list", map[string]any{"skill_id": skillID, "limit": boundedKnowledgeLimit(limit)})
}

func (c *Client) GetSkillVersion(ctx context.Context, isolation contracts.IsolationContext, skillID, version string) (KnowledgeResult, error) {
	return c.SkillOperation(ctx, isolation, "/v3/skill/version/get", map[string]any{"skill_id": skillID, "version": version})
}

func (c *Client) ListSkillResources(ctx context.Context, isolation contracts.IsolationContext, skillID string) (KnowledgeResult, error) {
	return c.SkillOperation(ctx, isolation, "/v3/skill/resource/ls", map[string]any{"skill_id": skillID})
}

func (c *Client) ReadSkillResource(ctx context.Context, isolation contracts.IsolationContext, skillID, path string) (KnowledgeResult, error) {
	return c.SkillOperation(ctx, isolation, "/v3/skill/resource/read", map[string]any{"skill_id": skillID, "path": path})
}

func (c *Client) WriteSkillResource(ctx context.Context, isolation contracts.IsolationContext, skillID, path, content string) (KnowledgeResult, error) {
	if len(content) > 512*1024 {
		content = content[:512*1024]
	}
	return c.SkillOperation(ctx, isolation, "/v3/skill/resource/write", map[string]any{"skill_id": skillID, "path": path, "content": content})
}

func (c *Client) RemoveSkillResource(ctx context.Context, isolation contracts.IsolationContext, skillID, path string) (KnowledgeResult, error) {
	return c.SkillOperation(ctx, isolation, "/v3/skill/resource/rm", map[string]any{"skill_id": skillID, "path": path})
}

func (c *Client) ExtractSkills(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.SkillOperation(ctx, isolation, "/v3/skill/extract", body)
}

func skillFromResult(result KnowledgeResult, err error) (Skill, error) {
	if err != nil {
		return Skill{}, err
	}
	var skill Skill
	if unmarshalErr := json.Unmarshal(result.Data, &skill); unmarshalErr != nil {
		return Skill{}, unmarshalErr
	}
	skill.ID = firstNonEmpty(skill.ID, skill.SkillID)
	skill.SkillID = firstNonEmpty(skill.SkillID, skill.ID)
	if skill.ID == "" {
		return Skill{}, errors.New("Tencent Skill response contained no skill_id")
	}
	return skill, nil
}

func boundedSkillText(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}
