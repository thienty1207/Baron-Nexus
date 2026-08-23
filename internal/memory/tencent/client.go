package tencent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
)

type Config struct {
	Endpoint    string
	HubEndpoint string
	UserKey     string
	AdminKey    string
	AuthToken   string
	ServiceID   string
	HTTPClient  *http.Client
	Timeout     time.Duration
}

type Client struct {
	config Config
	http   *http.Client
}

var _ contracts.MemoryBackend = (*Client)(nil)
var _ contracts.LayeredMemoryBackend = (*Client)(nil)

func NewClient(config Config) *Client {
	if config.Timeout <= 0 {
		config.Timeout = 3 * time.Second
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: config.Timeout}
	}
	return &Client{config: config, http: httpClient}
}

func (c *Client) Health(ctx context.Context) error {
	if err := c.do(ctx, http.MethodGet, "/health", nil, nil); err != nil {
		return fmt.Errorf("Tencent MemoryCore unavailable: %w", err)
	}
	return nil
}

// HealthAt checks a companion local service such as MemoryHub or the proxy
// while reusing the same bounded HTTP/auth behavior as the core client.
func (c *Client) HealthAt(ctx context.Context, endpoint string) error {
	if strings.TrimSpace(endpoint) == "" {
		return errors.New("Tencent companion endpoint is not configured")
	}
	companion := NewClient(Config{Endpoint: endpoint, HTTPClient: c.http, AuthToken: c.config.AuthToken, AdminKey: c.config.AdminKey, UserKey: c.config.UserKey, ServiceID: c.config.ServiceID, Timeout: c.config.Timeout})
	return companion.Health(ctx)
}

func (c *Client) VerifyAuth(ctx context.Context) error {
	var response map[string]any
	if err := c.do(ctx, http.MethodPost, "/v3/meta/auth/verify", nil, &response); err != nil {
		return fmt.Errorf("Tencent user-key authentication failed: %w", err)
	}
	return nil
}

func (c *Client) Capture(ctx context.Context, isolation contracts.IsolationContext, record contracts.MemoryRecord, idempotencyKey string) (contracts.MemoryReceipt, error) {
	if err := validateIsolation(isolation); err != nil {
		return contracts.MemoryReceipt{}, err
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return contracts.MemoryReceipt{}, errors.New("memory capture idempotency key is required")
	}
	if record.ProjectID != "" && record.ProjectID != isolation.ProjectID {
		return contracts.MemoryReceipt{}, errors.New("memory record project identity mismatch")
	}
	record.ProjectID = isolation.ProjectID
	record.Content = config.Redact(record.Content, c.secretValues())
	for key, value := range record.Metadata {
		record.Metadata[key] = config.Redact(value, c.secretValues())
	}
	record.ContentHash = ""
	record.Normalize()
	role := "assistant"
	if record.Kind == "user_prompt" {
		role = "user"
	}
	body := map[string]any{
		"messages": []map[string]string{{"role": role, "content": record.Content}},
		"team_id":  isolation.TeamID, "agent_id": isolation.AgentID, "user_id": isolation.UserID,
		"project_id": isolation.ProjectID, "session_id": firstNonEmpty(record.SessionID, isolation.SessionID),
		"idempotency_key": idempotencyKey, "metadata": record.Metadata,
	}
	var response struct {
		RequestID string `json:"request_id"`
		ID        string `json:"id"`
		Data      struct {
			RequestID string `json:"request_id"`
			ID        string `json:"id"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/v3/conversation/add", body, &response); err != nil {
		return contracts.MemoryReceipt{}, err
	}
	requestID := firstNonEmpty(response.RequestID, response.ID, response.Data.RequestID, response.Data.ID)
	if requestID == "" {
		requestID = idempotencyKey
	}
	return contracts.MemoryReceipt{RequestID: requestID, ContentHash: record.ContentHash, IdempotencyKey: idempotencyKey, DeliveredAt: time.Now().UTC()}, nil
}

func (c *Client) Search(ctx context.Context, isolation contracts.IsolationContext, query contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	if err := validateIsolation(isolation); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	body := map[string]any{
		"query": query.Text, "limit": limit,
		"team_id": isolation.TeamID, "agent_id": isolation.AgentID, "user_id": isolation.UserID,
		"project_id": isolation.ProjectID,
	}
	if isolation.SessionID != "" {
		body["session_id"] = isolation.SessionID
	}
	if !query.Since.IsZero() {
		body["since"] = query.Since.UTC().Format(time.RFC3339Nano)
	}
	if len(query.Kinds) > 0 {
		body["kinds"] = query.Kinds
	}
	var response json.RawMessage
	if err := c.do(ctx, http.MethodPost, "/v3/atomic/search", body, &response); err != nil {
		return nil, err
	}
	return decodeRecords(response, isolation.ProjectID), nil
}

func (c *Client) ReadCore(ctx context.Context, isolation contracts.IsolationContext, query contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	return c.readLayer(ctx, "/v3/core/read", isolation, query)
}

func (c *Client) ReadScenario(ctx context.Context, isolation contracts.IsolationContext, query contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	return c.readLayer(ctx, "/v3/scenario/read", isolation, query)
}

func (c *Client) SearchConversations(ctx context.Context, isolation contracts.IsolationContext, query contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	return c.readLayer(ctx, "/v3/conversation/search", isolation, query)
}

func (c *Client) readLayer(ctx context.Context, path string, isolation contracts.IsolationContext, query contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	if err := validateIsolation(isolation); err != nil {
		return nil, err
	}
	body := map[string]any{
		"query": query.Text, "limit": query.Limit,
		"team_id": isolation.TeamID, "agent_id": isolation.AgentID, "user_id": isolation.UserID,
		"project_id": isolation.ProjectID,
	}
	if isolation.SessionID != "" {
		body["session_id"] = isolation.SessionID
	}
	var response json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, body, &response); err != nil {
		return nil, err
	}
	return decodeRecords(response, isolation.ProjectID), nil
}

func (c *Client) EnsureProjectAgent(ctx context.Context, isolation contracts.IsolationContext, displayName string) (contracts.ProjectBinding, error) {
	if strings.TrimSpace(isolation.ProjectID) == "" || strings.TrimSpace(isolation.TeamID) == "" {
		return contracts.ProjectBinding{}, errors.New("project_id and team_id are required to provision an agent")
	}
	if strings.TrimSpace(isolation.UserID) == "" {
		return contracts.ProjectBinding{}, errors.New("user_id is required to provision an agent")
	}
	body := map[string]any{"team_id": isolation.TeamID, "user_id": isolation.UserID, "project_id": isolation.ProjectID}
	var response json.RawMessage
	if err := c.do(ctx, http.MethodPost, "/v3/meta/agent/list", body, &response); err != nil {
		return contracts.ProjectBinding{}, err
	}
	for _, agent := range decodeAgents(response) {
		if strings.Contains(agent.Description, "project_id="+isolation.ProjectID) || agent.ProjectID == isolation.ProjectID {
			return contracts.ProjectBinding{ProjectID: isolation.ProjectID, TeamID: isolation.TeamID, AgentID: agent.ID, AgentName: agent.Name, UserID: isolation.UserID}, nil
		}
	}
	for _, agent := range decodeAgents(response) {
		if agent.Name == displayName {
			displayName = displayName + " [" + shortID(isolation.ProjectID) + "]"
			break
		}
	}
	createBody := map[string]any{
		"name":        displayName,
		"description": "Baron project_id=" + isolation.ProjectID,
		"team_id":     isolation.TeamID, "user_id": isolation.UserID, "project_id": isolation.ProjectID,
	}
	var created json.RawMessage
	if err := c.do(ctx, http.MethodPost, "/v3/meta/agent/create", createBody, &created); err != nil {
		return contracts.ProjectBinding{}, err
	}
	agents := decodeAgents(created)
	if len(agents) == 0 {
		return contracts.ProjectBinding{}, errors.New("Tencent agent create returned no agent ID")
	}
	return contracts.ProjectBinding{ProjectID: isolation.ProjectID, TeamID: isolation.TeamID, AgentID: agents[0].ID, AgentName: firstNonEmpty(agents[0].Name, displayName), UserID: isolation.UserID}, nil
}

func (c *Client) EnsureIdentity(ctx context.Context, spec contracts.IdentitySpec) (contracts.Identity, error) {
	if strings.TrimSpace(spec.UserName) == "" {
		spec.UserName = "baron"
	}
	if strings.TrimSpace(spec.TeamName) == "" {
		spec.TeamName = "baron-projects"
	}
	user, err := c.ensureNamed(ctx, "/v3/meta/user/list", "/v3/meta/user/create", "username", spec.UserName)
	if err != nil {
		return contracts.Identity{}, err
	}
	userID := user.ID
	userKey := firstNonEmpty(user.UserKey, user.DefaultUserKey)
	if userKey == "" {
		var keys json.RawMessage
		if err := c.do(ctx, http.MethodPost, "/v3/meta/user-key/list", map[string]any{"user_id": userID}, &keys); err != nil {
			return contracts.Identity{}, err
		}
		keyItems := decodeAgents(keys)
		if len(keyItems) > 0 {
			userKey = firstNonEmpty(keyItems[0].UserKey, keyItems[0].ID)
		}
	}
	if userKey == "" {
		var created json.RawMessage
		if err := c.do(ctx, http.MethodPost, "/v3/meta/user-key/create", map[string]any{"user_id": userID}, &created); err != nil {
			return contracts.Identity{}, err
		}
		items := decodeAgents(created)
		if len(items) > 0 {
			userKey = firstNonEmpty(items[0].UserKey, items[0].ID)
		}
	}
	team, err := c.ensureNamed(ctx, "/v3/meta/team/list", "/v3/meta/team/create", "name", spec.TeamName, map[string]any{"user_id": userID})
	if err != nil {
		return contracts.Identity{}, err
	}
	return contracts.Identity{UserID: userID, UserKey: userKey, TeamID: team.ID, TeamName: spec.TeamName, ServiceID: firstNonEmpty(c.config.ServiceID, "default"), Endpoint: c.config.Endpoint, HubEndpoint: c.config.HubEndpoint}, nil
}

type namedEntity struct {
	ID             string `json:"id"`
	AgentID        string `json:"agent_id"`
	TeamID         string `json:"team_id"`
	UserID         string `json:"user_id"`
	UserKey        string `json:"user_key"`
	DefaultUserKey string `json:"default_user_key"`
	Name           string `json:"name"`
	Username       string `json:"username"`
	Description    string `json:"description"`
	ProjectID      string `json:"project_id"`
}

func (c *Client) ensureNamed(ctx context.Context, listPath, createPath, key, value string, extras ...map[string]any) (namedEntity, error) {
	body := map[string]any{key: value}
	for _, extra := range extras {
		for extraKey, extraValue := range extra {
			body[extraKey] = extraValue
		}
	}
	var response json.RawMessage
	if err := c.do(ctx, http.MethodPost, listPath, body, &response); err != nil {
		return namedEntity{}, err
	}
	items := decodeAgents(response)
	for _, item := range items {
		if item.Name == value || item.Username == value || item.ID == value {
			return item, nil
		}
	}
	var created json.RawMessage
	if err := c.do(ctx, http.MethodPost, createPath, body, &created); err != nil {
		return namedEntity{}, err
	}
	items = decodeAgents(created)
	if len(items) == 0 {
		return namedEntity{}, fmt.Errorf("Tencent %s returned no entity", createPath)
	}
	return items[0], nil
}

func validateIsolation(isolation contracts.IsolationContext) error {
	if err := isolation.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(isolation.UserID) == "" {
		return errors.New("user_id is required for Tencent v3 isolation")
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, target any) error {
	if strings.TrimSpace(c.config.Endpoint) == "" {
		return errors.New("Tencent endpoint is not configured")
	}
	base, err := url.Parse(strings.TrimRight(c.config.Endpoint, "/"))
	if err != nil {
		return fmt.Errorf("parse Tencent endpoint: %s", config.Redact(err.Error(), c.secretValues()))
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, base.String(), reader)
	if err != nil {
		return fmt.Errorf("create Tencent request: %s", config.Redact(err.Error(), c.secretValues()))
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.config.AuthToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.config.AuthToken)
	} else if c.config.AdminKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.config.AdminKey)
	} else if c.config.UserKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.config.UserKey)
	}
	if key := firstNonEmpty(c.config.UserKey, c.config.AdminKey); key != "" {
		request.Header.Set("x-tdai-user-key", key)
	}
	if c.config.ServiceID != "" {
		request.Header.Set("x-tdai-service-id", c.config.ServiceID)
	}
	httpClient := c.http
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("Tencent request %s %s: %s", method, path, config.Redact(err.Error(), c.secretValues()))
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Tencent request %s %s failed with HTTP %d: %s", method, path, response.StatusCode, config.Redact(string(data), c.secretValues()))
	}
	var envelope struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(data, &envelope) == nil && nonZeroResponseCode(envelope.Code) {
		message := firstNonEmpty(envelope.Message, envelope.Error, "Tencent API returned an error")
		return fmt.Errorf("Tencent request %s %s returned code %v: %s", method, path, envelope.Code, config.Redact(message, c.secretValues()))
	}
	if target == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode Tencent response: %w", err)
	}
	return nil
}

func (c *Client) secretValues() []string {
	return []string{c.config.UserKey, c.config.AdminKey, c.config.AuthToken}
}

func nonZeroResponseCode(value any) bool {
	switch code := value.(type) {
	case nil:
		return false
	case float64:
		return code != 0
	case string:
		return code != "" && code != "0" && !strings.EqualFold(code, "ok")
	case bool:
		return code
	default:
		return true
	}
}

func decodeRecords(data json.RawMessage, projectID string) []contracts.MemoryRecord {
	var envelope struct {
		Items    []contracts.MemoryRecord `json:"items"`
		Records  []contracts.MemoryRecord `json:"records"`
		Memories []contracts.MemoryRecord `json:"memories"`
		Data     json.RawMessage          `json:"data"`
	}
	var direct []contracts.MemoryRecord
	if json.Unmarshal(data, &direct) == nil && len(direct) > 0 {
		return normalizeRecords(direct, projectID)
	}
	if json.Unmarshal(data, &envelope) != nil {
		return nil
	}
	items := envelope.Items
	if len(items) == 0 {
		items = envelope.Records
	}
	if len(items) == 0 {
		items = envelope.Memories
	}
	if len(items) == 0 && len(envelope.Data) > 0 {
		var nested struct {
			Items    []contracts.MemoryRecord `json:"items"`
			Records  []contracts.MemoryRecord `json:"records"`
			Memories []contracts.MemoryRecord `json:"memories"`
		}
		if json.Unmarshal(envelope.Data, &nested) == nil {
			items = nested.Items
			if len(items) == 0 {
				items = nested.Records
			}
			if len(items) == 0 {
				items = nested.Memories
			}
		}
	}
	return normalizeRecords(items, projectID)
}

func normalizeRecords(items []contracts.MemoryRecord, projectID string) []contracts.MemoryRecord {
	for index := range items {
		if items[index].ProjectID == "" {
			items[index].ProjectID = projectID
		}
		items[index].HistoricalOnly = true
		items[index].Normalize()
	}
	return items
}

func decodeAgents(data json.RawMessage) []namedEntity {
	var envelope struct {
		Items          []namedEntity   `json:"items"`
		Data           json.RawMessage `json:"data"`
		ID             string          `json:"id"`
		AgentID        string          `json:"agent_id"`
		TeamID         string          `json:"team_id"`
		UserID         string          `json:"user_id"`
		UserKey        string          `json:"user_key"`
		DefaultUserKey string          `json:"default_user_key"`
		Name           string          `json:"name"`
		Username       string          `json:"username"`
		Description    string          `json:"description"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return nil
	}
	if len(envelope.Items) > 0 {
		return normalizeEntities(envelope.Items)
	}
	if len(envelope.Data) > 0 {
		var nested struct {
			Items []namedEntity `json:"items"`
		}
		if json.Unmarshal(envelope.Data, &nested) == nil && len(nested.Items) > 0 {
			return normalizeEntities(nested.Items)
		}
		var nestedItems []namedEntity
		if json.Unmarshal(envelope.Data, &nestedItems) == nil && len(nestedItems) > 0 {
			return normalizeEntities(nestedItems)
		}
		var nestedEntity namedEntity
		if json.Unmarshal(envelope.Data, &nestedEntity) == nil && firstNonEmpty(nestedEntity.ID, nestedEntity.AgentID, nestedEntity.TeamID, nestedEntity.UserID, nestedEntity.UserKey, nestedEntity.DefaultUserKey) != "" {
			nestedEntity.ID = firstNonEmpty(nestedEntity.ID, nestedEntity.AgentID, nestedEntity.TeamID, nestedEntity.UserID, nestedEntity.UserKey, nestedEntity.DefaultUserKey)
			return []namedEntity{nestedEntity}
		}
	}
	id := firstNonEmpty(envelope.ID, envelope.AgentID, envelope.TeamID, envelope.UserID, envelope.UserKey, envelope.DefaultUserKey)
	if id == "" {
		return nil
	}
	return []namedEntity{{ID: id, TeamID: envelope.TeamID, UserID: envelope.UserID, UserKey: envelope.UserKey, DefaultUserKey: envelope.DefaultUserKey, Name: envelope.Name, Username: envelope.Username, Description: envelope.Description}}
}

func normalizeEntities(items []namedEntity) []namedEntity {
	for index := range items {
		items[index].ID = firstNonEmpty(items[index].ID, items[index].AgentID, items[index].TeamID, items[index].UserID, items[index].UserKey, items[index].DefaultUserKey)
	}
	return items
}

func shortID(value string) string {
	if len(value) > 8 {
		return value[:8]
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
