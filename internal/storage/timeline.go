package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
)

// TimelineEvent is a safe, bounded projection of one journal event. The raw
// payload is intentionally not exposed by the local diagnostic surface.
type TimelineEvent struct {
	EventID           string                     `json:"event_id"`
	ProjectID         string                     `json:"project_id"`
	SessionID         string                     `json:"session_id,omitempty"`
	Client            contracts.HookClient       `json:"client"`
	Type              contracts.EventType        `json:"type"`
	OccurredAt        time.Time                  `json:"occurred_at"`
	TaskID            string                     `json:"task_id,omitempty"`
	Status            contracts.TaskStatus       `json:"status,omitempty"`
	Command           string                     `json:"command,omitempty"`
	VerificationKind  contracts.VerificationKind `json:"verification_kind,omitempty"`
	VerificationScope string                     `json:"verification_scope,omitempty"`
	ExitCode          *int                       `json:"exit_code,omitempty"`
	Summary           string                     `json:"summary,omitempty"`
}

// ConversationMessage is a safe projection of a user prompt or assistant
// response from the local event ledger. It deliberately excludes the raw
// hook payload so callers cannot accidentally replay tool arguments or
// credentials as chat history.
type ConversationMessage struct {
	EventID    string
	ProjectID  string
	SessionID  string
	Client     contracts.HookClient
	Type       contracts.EventType
	OccurredAt time.Time
	Role       string
	Content    string
}

// ListTimeline returns chronological local event metadata. It is bounded both
// by row count and by field size so status tooling cannot accidentally dump a
// complete hook payload or tool output into a terminal.
func (s *Store) ListTimeline(ctx context.Context, projectID string, limit int) ([]TimelineEvent, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("timeline project ID is required")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT event_id, project_id, session_id, client, event_type, occurred_at, payload
		FROM (
			SELECT event_id, project_id, session_id, client, event_type, occurred_at, payload, created_at
			FROM events WHERE project_id=?
			ORDER BY occurred_at DESC, created_at DESC, event_id DESC LIMIT ?
		)
		ORDER BY occurred_at ASC, created_at ASC, event_id ASC`, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("list timeline: %w", err)
	}
	defer rows.Close()
	result := make([]TimelineEvent, 0, limit)
	for rows.Next() {
		var event TimelineEvent
		var eventType, occurredAt string
		var payload []byte
		if err := rows.Scan(&event.EventID, &event.ProjectID, &event.SessionID, &event.Client, &eventType, &occurredAt, &payload); err != nil {
			return nil, fmt.Errorf("scan timeline event: %w", err)
		}
		event.Type = contracts.EventType(eventType)
		event.OccurredAt = parseTime(occurredAt)
		parseTimelinePayload(&event, payload)
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read timeline: %w", err)
	}
	return result, nil
}

// ListConversation returns the most recent bounded conversation turns in
// chronological order. Session start callers can exclude their current
// session to recover the previous session without feeding the just-arrived
// prompt back into the model.
func (s *Store) ListConversation(ctx context.Context, projectID, excludeSessionID string, limit int) ([]ConversationMessage, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("conversation project ID is required")
	}
	if limit <= 0 || limit > 100 {
		limit = 24
	}
	scanLimit := limit * 4
	if scanLimit > 400 {
		scanLimit = 400
	}
	query := `SELECT event_id, project_id, session_id, client, event_type, occurred_at, created_at, payload
		FROM (
			SELECT event_id, project_id, session_id, client, event_type, occurred_at, created_at, payload
			FROM events
			WHERE project_id=? AND event_type IN ('user_prompt', 'assistant_final', 'checkpoint_updated')`
	args := []any{projectID}
	if strings.TrimSpace(excludeSessionID) != "" {
		query += ` AND session_id<>?`
		args = append(args, excludeSessionID)
	}
	query += ` ORDER BY occurred_at DESC, created_at DESC, event_id DESC LIMIT ?
		)
		ORDER BY occurred_at ASC, created_at ASC, event_id ASC`
	args = append(args, scanLimit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list conversation: %w", err)
	}
	defer rows.Close()
	result := make([]ConversationMessage, 0, limit)
	previousRole, previousContent := "", ""
	for rows.Next() {
		var message ConversationMessage
		var eventType, occurredAt, createdAt string
		var payload []byte
		if err := rows.Scan(&message.EventID, &message.ProjectID, &message.SessionID, &message.Client, &eventType, &occurredAt, &createdAt, &payload); err != nil {
			return nil, fmt.Errorf("scan conversation event: %w", err)
		}
		message.Type = contracts.EventType(eventType)
		message.OccurredAt = parseTime(occurredAt)
		message.Role, message.Content = parseConversationPayload(message.Type, payload)
		if message.Content == "" {
			continue
		}
		// DSH emits the current user turn on every pre-step. Adjacent identical
		// user turns are one conversation turn, not a reason to evict older
		// history from the bounded projection.
		if message.Role == "user" && previousRole == "user" && message.Content == previousContent {
			continue
		}
		if len(result) == limit {
			// The SQL window is bounded for safety, but it is ordered
			// chronologically for callers. Keep the newest projection rows
			// instead of returning the oldest part of that window.
			result = append(result[1:], message)
		} else {
			result = append(result, message)
		}
		previousRole, previousContent = message.Role, message.Content
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read conversation: %w", err)
	}
	return result, nil
}

// CountSessionEvents is used by adapters that emit a generic checkpoint for
// every model step. It lets them inject recovered conversation only on the
// first step of a session instead of repeating the same context on every
// tool call.
func (s *Store) CountSessionEvents(ctx context.Context, projectID, sessionID string, eventType contracts.EventType) (int, error) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(string(eventType)) == "" {
		return 0, fmt.Errorf("project ID, session ID, and event type are required")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE project_id=? AND session_id=? AND event_type=?`, projectID, sessionID, eventType).Scan(&count); err != nil {
		return 0, fmt.Errorf("count session events: %w", err)
	}
	return count, nil
}

func parseTimelinePayload(event *TimelineEvent, payload []byte) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(payload, &raw) != nil {
		return
	}
	event.TaskID = timelineString(raw, "task_id", 256)
	status := timelineString(raw, "status", 64)
	if status == "" {
		status = timelineString(raw, "task_status", 64)
	}
	event.Status = contracts.TaskStatus(status)
	event.Command = timelineString(raw, "command", 256)
	event.VerificationKind = contracts.VerificationKind(timelineString(raw, "verification_kind", 64))
	event.VerificationScope = timelineString(raw, "verification_scope", 256)
	if value, ok := raw["exit_code"]; ok {
		var exitCode int
		if json.Unmarshal(value, &exitCode) == nil {
			event.ExitCode = &exitCode
		}
	}
	event.Summary = timelineString(raw, "summary", 512)
}

func parseConversationPayload(eventType contracts.EventType, payload []byte) (string, string) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(payload, &raw) != nil {
		return "", ""
	}
	role := ""
	keys := []string{}
	switch eventType {
	case contracts.EventUserPrompt:
		role = "user"
		keys = []string{"prompt", "text", "message", "content"}
	case contracts.EventAssistantFinal:
		role = "assistant"
		keys = []string{"response", "last_assistant_message", "summary", "text", "message", "content"}
	case contracts.EventCheckpointUpdated:
		// DSH reports the current user turn through checkpoint_updated. Do not
		// treat generic tool summaries as conversation turns.
		role = "user"
		keys = []string{"prompt"}
	default:
		return "", ""
	}
	for _, key := range keys {
		if value := boundedConversationText(conversationJSONText(raw[key]), 8192); value != "" {
			return role, value
		}
	}
	return "", ""
}

func conversationJSONText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return conversationValueText(value)
}

func conversationValueText(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			if part := conversationValueText(item); part != "" {
				parts = append(parts, part)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	case map[string]any:
		for _, key := range []string{"text", "output", "value", "message", "content"} {
			if part := conversationValueText(value[key]); part != "" {
				return part
			}
		}
	}
	return ""
}

func boundedConversationText(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(value) <= max {
		return value
	}
	const suffix = "...[truncated]"
	if max <= len(suffix) {
		return value[:max]
	}
	return value[:max-len(suffix)] + suffix
}

func timelineString(raw map[string]json.RawMessage, key string, max int) string {
	value, ok := raw[key]
	if !ok {
		return ""
	}
	var result string
	if json.Unmarshal(value, &result) != nil {
		return ""
	}
	return boundedTimelineText(config.Redact(strings.TrimSpace(result), nil), max)
}

func boundedTimelineText(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(value) <= max {
		return value
	}
	const suffix = "...[truncated]"
	if max <= len(suffix) {
		return value[:max]
	}
	return value[:max-len(suffix)] + suffix
}
