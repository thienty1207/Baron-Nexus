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
	"unicode/utf16"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
)

type Config struct {
	Endpoint          string
	HubEndpoint       string
	KnowledgeEndpoint string
	UserKey           string
	AdminKey          string
	AuthToken         string
	Secrets           []string
	ServiceID         string
	HTTPClient        *http.Client
	Timeout           time.Duration
}

type Client struct {
	config Config
	http   *http.Client
}

const tencentConversationContentLimit = 8192

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

// withUserKey creates a client for caller-scoped mutations. The bootstrap
// admin key remains on the parent client for user management, while team
// creation/deletion must authenticate as the user named by owner_user_id.
func (c *Client) withUserKey(userKey string) *Client {
	return NewClient(Config{
		Endpoint:          c.config.Endpoint,
		HubEndpoint:       c.config.HubEndpoint,
		KnowledgeEndpoint: c.config.KnowledgeEndpoint,
		UserKey:           userKey,
		AuthToken:         c.config.AuthToken,
		Secrets:           c.config.Secrets,
		ServiceID:         c.config.ServiceID,
		HTTPClient:        c.http,
		Timeout:           c.config.Timeout,
	})
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
	if strings.TrimSpace(c.config.UserKey) == "" {
		return errors.New("Tencent user-key authentication requires a user key")
	}
	if err := c.doUnredacted(ctx, http.MethodPost, "/v3/meta/auth/verify", map[string]any{"user_key": c.config.UserKey}, &response); err != nil {
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
	record.Content = boundTencentConversationContent(record.Content)
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

func boundTencentConversationContent(value string) string {
	const suffix = "...[truncated]"
	if utf16Length(value) <= tencentConversationContentLimit {
		return value
	}
	budget := tencentConversationContentLimit - utf16Length(suffix)
	if budget <= 0 {
		return suffix
	}
	var builder strings.Builder
	used := 0
	for _, character := range value {
		width := utf16.RuneLen(character)
		if width < 0 {
			width = 1
		}
		if used+width > budget {
			break
		}
		builder.WriteRune(character)
		used += width
	}
	builder.WriteString(suffix)
	return builder.String()
}

func utf16Length(value string) int {
	length := 0
	for _, character := range value {
		width := utf16.RuneLen(character)
		if width < 0 {
			width = 1
		}
		length += width
	}
	return length
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
	path := strings.TrimSpace(query.ScenarioPath)
	if path == "" {
		// Tencent's scenario/read endpoint is path-addressed, not text-search
		// addressed. Generic recall queries must not send an empty path and turn
		// an otherwise healthy remote memory read into a false outage.
		return nil, nil
	}
	return c.readLayerWithBody(ctx, "/v3/scenario/read", isolation, query, map[string]any{"path": path})
}

func (c *Client) SearchConversations(ctx context.Context, isolation contracts.IsolationContext, query contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	return c.readLayer(ctx, "/v3/conversation/search", isolation, query)
}

func (c *Client) readLayer(ctx context.Context, path string, isolation contracts.IsolationContext, query contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	return c.readLayerWithBody(ctx, path, isolation, query, nil)
}

func (c *Client) readLayerWithBody(ctx context.Context, path string, isolation contracts.IsolationContext, query contracts.MemoryQuery, extra map[string]any) ([]contracts.MemoryRecord, error) {
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
	for key, value := range extra {
		body[key] = value
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
		"team_id":     isolation.TeamID, "owner_user_id": isolation.UserID,
		"user_id": isolation.UserID, "project_id": isolation.ProjectID,
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

// FindProjectAgent verifies an existing project binding without creating or
// mutating Tencent metadata. Restore/recovery uses this path so a stale or
// missing remote agent is reported for repair instead of silently producing a
// duplicate agent under the same project identity.
func (c *Client) FindProjectAgent(ctx context.Context, isolation contracts.IsolationContext) (contracts.ProjectBinding, error) {
	if strings.TrimSpace(isolation.ProjectID) == "" || strings.TrimSpace(isolation.TeamID) == "" {
		return contracts.ProjectBinding{}, errors.New("project_id and team_id are required to verify an agent")
	}
	if strings.TrimSpace(isolation.UserID) == "" {
		return contracts.ProjectBinding{}, errors.New("user_id is required to verify an agent")
	}
	body := map[string]any{"team_id": isolation.TeamID, "user_id": isolation.UserID, "project_id": isolation.ProjectID}
	var response json.RawMessage
	if err := c.do(ctx, http.MethodPost, "/v3/meta/agent/list", body, &response); err != nil {
		return contracts.ProjectBinding{}, err
	}
	for _, agent := range decodeAgents(response) {
		if agent.ProjectID == isolation.ProjectID || strings.Contains(agent.Description, "project_id="+isolation.ProjectID) {
			return contracts.ProjectBinding{
				ProjectID: isolation.ProjectID,
				TeamID:    isolation.TeamID,
				AgentID:   agent.ID,
				AgentName: agent.Name,
				UserID:    isolation.UserID,
			}, nil
		}
	}
	return contracts.ProjectBinding{}, fmt.Errorf("Tencent project agent for %s was not found; run baron setup to repair the binding", isolation.ProjectID)
}

func (c *Client) EnsureIdentity(ctx context.Context, spec contracts.IdentitySpec) (contracts.Identity, error) {
	identity, _, err := c.EnsureIdentityWithRollback(ctx, spec)
	return identity, err
}

// EnsureIdentityWithRollback provisions only the requested Baron user/key/team
// and returns a receipt that can compensate entities created by this call if a
// later verification or persistence step fails. Existing entities are never
// added to the receipt and therefore can never be deleted by Rollback.
func (c *Client) EnsureIdentityWithRollback(ctx context.Context, spec contracts.IdentitySpec) (contracts.Identity, *IdentityProvision, error) {
	return c.EnsureIdentityWithExistingUserKey(ctx, spec, "")
}

// EnsureIdentityWithExistingUserKey is the repair-safe form of identity
// provisioning. Tencent returns a user-key secret only when a key is created;
// list/get responses expose only key_id and a masked prefix. A caller that has
// already persisted the secret can provide it here so repeated init runs do
// not create an unnecessary replacement key.
func (c *Client) EnsureIdentityWithExistingUserKey(ctx context.Context, spec contracts.IdentitySpec, existingUserKey string) (contracts.Identity, *IdentityProvision, error) {
	if strings.TrimSpace(spec.UserName) == "" {
		spec.UserName = "baron"
	}
	if strings.TrimSpace(spec.TeamName) == "" {
		spec.TeamName = "baron-projects"
	}
	provision := &IdentityProvision{client: c}
	fail := func(err error) (contracts.Identity, *IdentityProvision, error) {
		if err == nil {
			return contracts.Identity{}, provision, nil
		}
		if rollbackErr := provision.Rollback(ctx); rollbackErr != nil {
			return contracts.Identity{}, provision, fmt.Errorf("%v; newly created Baron metadata rollback failed: %w", err, rollbackErr)
		}
		return contracts.Identity{}, provision, err
	}
	user, created, err := c.ensureNamedTracked(ctx, "/v3/meta/user/list", "/v3/meta/user/create", "username", spec.UserName)
	if err != nil {
		return fail(err)
	}
	userID := user.ID
	if created {
		if id := opaqueEntityID(user); id != "" {
			provision.add("user", id, userID, "")
		} else {
			return fail(errors.New("Tencent user create returned no opaque rollback ID"))
		}
	}
	userKey := strings.TrimSpace(existingUserKey)
	if userKey == "" {
		userKey = firstNonEmpty(user.UserKey, user.DefaultUserKey, user.KeyValue)
	}
	if userKey == "" {
		var keys json.RawMessage
		if err := c.do(ctx, http.MethodPost, "/v3/meta/user-key/list", map[string]any{"user_id": userID}, &keys); err != nil {
			return fail(err)
		}
		keyItems := decodeAgents(keys)
		for _, keyItem := range keyItems {
			userKey = firstNonEmpty(keyItem.UserKey, keyItem.KeyValue)
			if userKey != "" {
				break
			}
		}
	}
	if userKey == "" {
		var created json.RawMessage
		if err := c.do(ctx, http.MethodPost, "/v3/meta/user-key/create", map[string]any{"user_id": userID}, &created); err != nil {
			return fail(err)
		}
		items := decodeAgents(created)
		if len(items) > 0 {
			userKey = firstNonEmpty(items[0].UserKey, items[0].KeyValue, items[0].DefaultUserKey)
			if id := opaqueEntityID(items[0]); id != "" {
				provision.add("user-key", id, userID, "")
			}
		}
	}
	if userKey == "" {
		return fail(errors.New("Tencent user-key create returned no user key"))
	}
	ownerClient := c.withUserKey(userKey)
	team, created, err := ownerClient.ensureNamedTrackedSplit(ctx, "/v3/meta/team/list", "/v3/meta/team/create", "name", spec.TeamName, map[string]any{"user_id": userID}, map[string]any{"owner_user_id": userID})
	if err != nil {
		return fail(err)
	}
	if created {
		if id := opaqueEntityID(team); id != "" {
			provision.addWithClient("team", id, userID, team.ID, ownerClient)
		} else {
			return fail(errors.New("Tencent team create returned no opaque rollback ID"))
		}
	}
	return contracts.Identity{UserID: userID, UserKey: userKey, TeamID: team.ID, TeamName: spec.TeamName, ServiceID: firstNonEmpty(c.config.ServiceID, "default"), Endpoint: c.config.Endpoint, HubEndpoint: c.config.HubEndpoint}, provision, nil
}

type namedEntity struct {
	ID             string `json:"id"`
	KeyID          string `json:"key_id"`
	AgentID        string `json:"agent_id"`
	TeamID         string `json:"team_id"`
	UserID         string `json:"user_id"`
	UserKey        string `json:"user_key"`
	KeyValue       string `json:"key_value"`
	DefaultUserKey string `json:"default_user_key"`
	Name           string `json:"name"`
	Username       string `json:"username"`
	Description    string `json:"description"`
	ProjectID      string `json:"project_id"`
}

func (c *Client) ensureNamed(ctx context.Context, listPath, createPath, key, value string, extras ...map[string]any) (namedEntity, error) {
	entity, _, err := c.ensureNamedTracked(ctx, listPath, createPath, key, value, extras...)
	return entity, err
}

func (c *Client) ensureNamedTracked(ctx context.Context, listPath, createPath, key, value string, extras ...map[string]any) (namedEntity, bool, error) {
	mergedExtras := mergeNamedExtras(extras...)
	return c.ensureNamedTrackedSplit(ctx, listPath, createPath, key, value, mergedExtras, mergedExtras)
}

func (c *Client) ensureNamedTrackedSplit(ctx context.Context, listPath, createPath, key, value string, listExtras, createExtras map[string]any) (namedEntity, bool, error) {
	listBody := namedRequestBody(key, value, listExtras)
	var response json.RawMessage
	if err := c.do(ctx, http.MethodPost, listPath, listBody, &response); err != nil {
		return namedEntity{}, false, err
	}
	items := decodeAgents(response)
	for _, item := range items {
		if item.Name == value || item.Username == value || item.ID == value {
			return item, false, nil
		}
	}
	createBody := namedRequestBody(key, value, createExtras)
	var created json.RawMessage
	if err := c.do(ctx, http.MethodPost, createPath, createBody, &created); err != nil {
		return namedEntity{}, false, err
	}
	items = decodeAgents(created)
	if len(items) == 0 {
		return namedEntity{}, false, fmt.Errorf("Tencent %s returned no entity", createPath)
	}
	return items[0], true, nil
}

func mergeNamedExtras(extras ...map[string]any) map[string]any {
	merged := make(map[string]any)
	for _, extra := range extras {
		for key, value := range extra {
			merged[key] = value
		}
	}
	return merged
}

func namedRequestBody(key, value string, extras map[string]any) map[string]any {
	body := map[string]any{key: value}
	for extraKey, extraValue := range extras {
		body[extraKey] = extraValue
	}
	return body
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
	return c.doEndpoint(ctx, c.config.Endpoint, method, path, body, target)
}

// doUnredacted is reserved for the auth/verify request, whose protocol
// requires the user-key secret in the JSON body. The body is never logged and
// all returned errors still pass through secret redaction.
func (c *Client) doUnredacted(ctx context.Context, method, path string, body any, target any) error {
	return c.doEndpointWithRedaction(ctx, c.config.Endpoint, method, path, body, target, false)
}

func (c *Client) doEndpoint(ctx context.Context, endpoint, method, path string, body any, target any) error {
	return c.doEndpointWithRedaction(ctx, endpoint, method, path, body, target, true)
}

func (c *Client) doEndpointWithRedaction(ctx context.Context, endpoint, method, path string, body any, target any, redactBody bool) error {
	if strings.TrimSpace(endpoint) == "" {
		return errors.New("Tencent endpoint is not configured")
	}
	base, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil {
		return fmt.Errorf("parse Tencent endpoint: %s", config.Redact(err.Error(), c.secretValues()))
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	var reader io.Reader
	if body != nil {
		requestBody := body
		if redactBody {
			requestBody = redactTencentValue(body, c.secretValues())
		}
		data, err := json.Marshal(requestBody)
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
		return redactedWrappedError{message: fmt.Sprintf("Tencent request %s %s: %s", method, path, config.Redact(err.Error(), c.secretValues())), cause: err}
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

func redactTencentValue(value any, secrets []string) any {
	switch typed := value.(type) {
	case string:
		return config.Redact(typed, secrets)
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(typed, &decoded) == nil {
			data, _ := json.Marshal(redactTencentValue(decoded, secrets))
			return json.RawMessage(data)
		}
		return json.RawMessage(config.Redact(string(typed), secrets))
	case []byte:
		return []byte(config.Redact(string(typed), secrets))
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = redactTencentValue(item, secrets)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = redactTencentValue(item, secrets)
		}
		return result
	case []string:
		result := make([]string, len(typed))
		for index, item := range typed {
			result[index] = config.Redact(item, secrets)
		}
		return result
	default:
		return value
	}
}

// redactedWrappedError keeps errors.Is/As useful for bounded context
// cancellation while returning only the redacted text to callers/logs.
type redactedWrappedError struct {
	message string
	cause   error
}

func (e redactedWrappedError) Error() string { return e.message }
func (e redactedWrappedError) Unwrap() error { return e.cause }

func (c *Client) secretValues() []string {
	values := []string{c.config.UserKey, c.config.AdminKey, c.config.AuthToken}
	return append(values, c.config.Secrets...)
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
		Messages []contracts.MemoryRecord `json:"messages"`
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
	if len(items) == 0 {
		items = envelope.Messages
	}
	if len(items) == 0 && len(envelope.Data) > 0 {
		var nested struct {
			Items    []contracts.MemoryRecord `json:"items"`
			Records  []contracts.MemoryRecord `json:"records"`
			Memories []contracts.MemoryRecord `json:"memories"`
			Messages []contracts.MemoryRecord `json:"messages"`
		}
		if json.Unmarshal(envelope.Data, &nested) == nil {
			items = nested.Items
			if len(items) == 0 {
				items = nested.Records
			}
			if len(items) == 0 {
				items = nested.Memories
			}
			if len(items) == 0 {
				items = nested.Messages
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
		KeyID          string          `json:"key_id"`
		AgentID        string          `json:"agent_id"`
		TeamID         string          `json:"team_id"`
		UserID         string          `json:"user_id"`
		UserKey        string          `json:"user_key"`
		KeyValue       string          `json:"key_value"`
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
		if json.Unmarshal(envelope.Data, &nestedEntity) == nil && firstNonEmpty(nestedEntity.ID, nestedEntity.KeyID, nestedEntity.AgentID, nestedEntity.TeamID, nestedEntity.UserID, nestedEntity.UserKey, nestedEntity.KeyValue, nestedEntity.DefaultUserKey) != "" {
			nestedEntity.ID = firstNonEmpty(nestedEntity.ID, nestedEntity.KeyID, nestedEntity.AgentID, nestedEntity.TeamID, nestedEntity.UserID, nestedEntity.UserKey, nestedEntity.KeyValue, nestedEntity.DefaultUserKey)
			return []namedEntity{nestedEntity}
		}
	}
	id := firstNonEmpty(envelope.ID, envelope.KeyID, envelope.AgentID, envelope.TeamID, envelope.UserID, envelope.UserKey, envelope.KeyValue, envelope.DefaultUserKey)
	if id == "" {
		return nil
	}
	return []namedEntity{{ID: id, KeyID: envelope.KeyID, TeamID: envelope.TeamID, UserID: envelope.UserID, UserKey: envelope.UserKey, KeyValue: envelope.KeyValue, DefaultUserKey: envelope.DefaultUserKey, Name: envelope.Name, Username: envelope.Username, Description: envelope.Description}}
}

func normalizeEntities(items []namedEntity) []namedEntity {
	for index := range items {
		items[index].ID = firstNonEmpty(items[index].ID, items[index].KeyID, items[index].AgentID, items[index].TeamID, items[index].UserID, items[index].UserKey, items[index].KeyValue, items[index].DefaultUserKey)
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
