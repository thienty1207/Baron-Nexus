package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

var allowedMemoryPaths = map[string]bool{
	"/v3/conversation/add": true, "/v3/conversation/query": true, "/v3/conversation/search": true,
	"/v3/conversation/delete": true, "/v3/conversation/count": true,
	"/v3/atomic/update": true, "/v3/atomic/query": true, "/v3/atomic/search": true,
	"/v3/atomic/delete": true, "/v3/atomic/count": true,
	"/v3/scenario/ls": true, "/v3/scenario/read": true, "/v3/scenario/write": true,
	"/v3/scenario/rm": true, "/v3/scenario/count": true,
	"/v3/core/read": true, "/v3/core/write": true, "/v3/core/count": true,
	"/v3/chat-memory/clear": true,
}

// MemoryOperation is the typed escape hatch for the complete v3 L0-L3 data
// plane. The allowlist prevents arbitrary URL construction while raw Data lets
// callers consume upstream schema additions without losing isolation checks.
func (c *Client) MemoryOperation(ctx context.Context, isolation contracts.IsolationContext, path string, body map[string]any) (KnowledgeResult, error) {
	if !allowedMemoryPaths[path] {
		return KnowledgeResult{}, fmt.Errorf("Tencent memory path %q is not allowlisted", path)
	}
	if err := validateIsolation(isolation); err != nil {
		return KnowledgeResult{}, err
	}
	body = cloneMap(body)
	for key, value := range map[string]string{
		"team_id":    isolation.TeamID,
		"agent_id":   isolation.AgentID,
		"user_id":    isolation.UserID,
		"project_id": isolation.ProjectID,
		"session_id": isolation.SessionID,
	} {
		if value != "" {
			body[key] = value
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return KnowledgeResult{}, err
	}
	if len(encoded) > 1<<20 {
		return KnowledgeResult{}, errors.New("Tencent memory request is larger than the 1 MiB safety bound")
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, json.RawMessage(encoded), &raw); err != nil {
		return KnowledgeResult{}, err
	}
	return decodeKnowledgeResult(raw)
}

func (c *Client) ConversationQuery(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/conversation/query", body)
}

func (c *Client) ConversationAdd(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/conversation/add", body)
}

func (c *Client) ConversationSearch(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/conversation/search", body)
}

func (c *Client) DeleteConversations(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/conversation/delete", body)
}

func (c *Client) CountConversations(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/conversation/count", body)
}

func (c *Client) AtomicQuery(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/atomic/query", body)
}

func (c *Client) AtomicSearch(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/atomic/search", body)
}

func (c *Client) AtomicUpdate(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/atomic/update", body)
}

func (c *Client) DeleteAtomic(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/atomic/delete", body)
}

func (c *Client) CountAtomic(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/atomic/count", body)
}

func (c *Client) ListScenarios(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/scenario/ls", body)
}

func (c *Client) WriteScenario(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/scenario/write", body)
}

func (c *Client) RemoveScenario(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/scenario/rm", body)
}

func (c *Client) CountScenarios(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/scenario/count", body)
}

func (c *Client) WriteCore(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/core/write", body)
}

func (c *Client) CountCore(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/core/count", body)
}

func (c *Client) ClearChatMemory(ctx context.Context, isolation contracts.IsolationContext, body map[string]any) (KnowledgeResult, error) {
	return c.MemoryOperation(ctx, isolation, "/v3/chat-memory/clear", body)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
