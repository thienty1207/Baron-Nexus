package tencent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// IdentityProvision is a short-lived compensation receipt. It contains only
// opaque Tencent IDs, never a user key or admin key. A resource is added only
// after the current EnsureIdentity call created it, so Rollback cannot remove
// pre-existing Baron or legacy metadata.
type IdentityProvision struct {
	client     *Client
	created    []createdMetadata
	rolledBack bool
	committed  bool
}

type createdMetadata struct {
	resource string
	id       string
	userID   string
	teamID   string
	client   *Client
}

func (p *IdentityProvision) add(resource, id, userID, teamID string) {
	p.addWithClient(resource, id, userID, teamID, nil)
}

func (p *IdentityProvision) addWithClient(resource, id, userID, teamID string, client *Client) {
	if p == nil || strings.TrimSpace(resource) == "" || strings.TrimSpace(id) == "" {
		return
	}
	if client == nil {
		client = p.client
	}
	p.created = append(p.created, createdMetadata{resource: resource, id: id, userID: userID, teamID: teamID, client: client})
}

// Commit discards the compensation receipt after all post-provisioning
// verification and persistence have succeeded.
func (p *IdentityProvision) Commit() {
	if p == nil {
		return
	}
	p.created = nil
	p.committed = true
}

// Rollback deletes only resources recorded by this provisioning transaction.
// It continues best-effort through the reverse dependency order so a failed
// team deletion does not prevent cleanup of a newly-created key/user.
func (p *IdentityProvision) Rollback(ctx context.Context) error {
	if p == nil || p.committed || p.rolledBack {
		return nil
	}
	p.rolledBack = true
	if p.client == nil {
		return errors.New("identity rollback has no Tencent client")
	}
	var failures []string
	for index := len(p.created) - 1; index >= 0; index-- {
		item := p.created[index]
		client := item.client
		if client == nil {
			client = p.client
		}
		if client == nil {
			failures = append(failures, fmt.Errorf("rollback of %s %s has no Tencent client", item.resource, item.id).Error())
			continue
		}
		if err := client.deleteCreatedMetadata(ctx, item); err != nil {
			failures = append(failures, err.Error())
		}
	}
	p.created = nil
	if len(failures) > 0 {
		return fmt.Errorf("Tencent metadata rollback incomplete: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (c *Client) deleteCreatedMetadata(ctx context.Context, item createdMetadata) error {
	if strings.TrimSpace(item.id) == "" {
		return fmt.Errorf("refused rollback of %s without an opaque ID", item.resource)
	}
	var path string
	var body map[string]any
	switch item.resource {
	case "user":
		path = "/v3/meta/user/delete"
		body = map[string]any{"user_ids": []string{item.id}}
	case "user-key":
		path = "/v3/meta/user-key/revoke"
		body = map[string]any{"key_id": item.id}
	case "team":
		path = "/v3/meta/team/delete"
		body = map[string]any{"team_ids": []string{item.id}}
	default:
		return fmt.Errorf("refused rollback of unsupported resource %s", item.resource)
	}
	if err := c.do(ctx, http.MethodPost, path, body, nil); err != nil {
		return fmt.Errorf("delete newly-created %s %s: %w", item.resource, item.id, err)
	}
	return nil
}

// opaqueEntityID rejects the fallback values used by decodeAgents for a
// response that exposes only a secret user-key string. A compensation action
// must never place that secret in a delete request or receipt.
func opaqueEntityID(entity namedEntity) string {
	if entity.ID == "" || entity.ID == entity.UserKey || entity.ID == entity.KeyValue || entity.ID == entity.DefaultUserKey {
		return ""
	}
	return entity.ID
}
