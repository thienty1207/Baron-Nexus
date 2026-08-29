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
