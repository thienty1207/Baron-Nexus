package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

func TestListTimelineReturnsBoundedSafeChronologicalMetadata(t *testing.T) {
	store := openTaskTestStore(t)
	ctx := context.Background()
	projectID := "prj-timeline-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: t.TempDir(), Name: "timeline"}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	events := []Event{
		{EventID: "timeline-start", ProjectID: projectID, SessionID: "session-a", Client: contracts.ClientCodex, Type: contracts.EventTaskStarted, OccurredAt: base, IdempotencyKey: "timeline-start", Payload: json.RawMessage(`{"task_id":"task-a","goal":"build timeline","status":"in_progress"}`)},
		{EventID: "timeline-tool", ProjectID: projectID, SessionID: "session-a", Client: contracts.ClientCodex, Type: contracts.EventToolFinished, OccurredAt: base.Add(time.Minute), IdempotencyKey: "timeline-tool", Payload: json.RawMessage(`{"task_id":"task-a","command":"go test ./...","summary":"token=sk-timeline-secret","raw_output":"must not appear in timeline"}`)},
		{EventID: "timeline-verify", ProjectID: projectID, SessionID: "session-a", Client: contracts.ClientCodex, Type: contracts.EventTaskVerified, OccurredAt: base.Add(2 * time.Minute), IdempotencyKey: "timeline-verify", Payload: json.RawMessage(`{"task_id":"task-a","verification_ref":"timeline-tool","verification_kind":"unit","verification_scope":"internal/storage","git_head":"head-a","diff_hash":"diff-a","exit_code":0,"summary":"unit tests passed"}`)},
	}
	for _, event := range events {
		if inserted, err := store.InsertEvent(ctx, event); err != nil || !inserted {
			t.Fatalf("insert %s: inserted=%v err=%v", event.EventID, inserted, err)
		}
	}

	entries, err := store.ListTimeline(ctx, projectID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("timeline length=%d, want 2", len(entries))
	}
	if entries[0].EventID != "timeline-tool" || entries[1].EventID != "timeline-verify" {
		t.Fatalf("timeline order=%q, %q", entries[0].EventID, entries[1].EventID)
	}
	if entries[1].TaskID != "task-a" || entries[1].VerificationKind != contracts.VerificationUnit {
		t.Fatalf("task metadata missing: %#v", entries[1])
	}
	if strings.Contains(entries[0].Summary, "sk-timeline-secret") || strings.Contains(entries[0].Summary, "raw_output") {
		t.Fatalf("timeline exposed unsafe payload data: %#v", entries[0])
	}

	entries, err = store.ListTimeline(ctx, projectID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].EventID != "timeline-start" || entries[2].VerificationKind != contracts.VerificationUnit || entries[2].ExitCode == nil || *entries[2].ExitCode != 0 {
		t.Fatalf("verification metadata missing: %#v", entries)
	}
}

func TestListTimelineBoundsSummaryAndLimit(t *testing.T) {
	store := openTaskTestStore(t)
	ctx := context.Background()
	projectID := "prj-timeline-bound-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: t.TempDir(), Name: "timeline bound"}); err != nil {
		t.Fatal(err)
	}
	longSummary := strings.Repeat("x", 2048)
	payload, err := json.Marshal(map[string]any{"summary": longSummary})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertEvent(ctx, Event{
		EventID: "timeline-long", ProjectID: projectID, Client: contracts.ClientCodex, Type: contracts.EventAssistantFinal,
		OccurredAt: time.Now().UTC(), IdempotencyKey: "timeline-long", Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := store.ListTimeline(ctx, projectID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || len(entries[0].Summary) > 512 {
		t.Fatalf("timeline summary was not bounded: len=%d entries=%#v", len(entries[0].Summary), entries)
	}
}

func TestListConversationProjectsOnlyBoundedChatTurns(t *testing.T) {
	store := openTaskTestStore(t)
	ctx := context.Background()
	projectID := "prj-conversation-projection-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: t.TempDir(), Name: "conversation"}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	events := []Event{
		{EventID: "conversation-user", ProjectID: projectID, SessionID: "old-session", Client: contracts.ClientCodex, Type: contracts.EventUserPrompt, OccurredAt: base, IdempotencyKey: "conversation-user", Payload: json.RawMessage(`{"prompt":"What did we discuss?"}`)},
		{EventID: "conversation-answer", ProjectID: projectID, SessionID: "old-session", Client: contracts.ClientCodex, Type: contracts.EventAssistantFinal, OccurredAt: base.Add(time.Second), IdempotencyKey: "conversation-answer", Payload: json.RawMessage(`{"prompt":"What did we discuss?","content":[{"type":"text","text":"The SQLite recovery flow."}]}`)},
		{EventID: "conversation-checkpoint", ProjectID: projectID, SessionID: "old-session", Client: contracts.ClientDSH, Type: contracts.EventCheckpointUpdated, OccurredAt: base.Add(2 * time.Second), IdempotencyKey: "conversation-checkpoint", Payload: json.RawMessage(`{"prompt":"What did we discuss?","summary":"tool output must not become chat"}`)},
		{EventID: "conversation-checkpoint-repeat", ProjectID: projectID, SessionID: "old-session", Client: contracts.ClientDSH, Type: contracts.EventCheckpointUpdated, OccurredAt: base.Add(2500 * time.Millisecond), IdempotencyKey: "conversation-checkpoint-repeat", Payload: json.RawMessage(`{"prompt":"What did we discuss?","summary":"another tool step"}`)},
		{EventID: "conversation-current", ProjectID: projectID, SessionID: "new-session", Client: contracts.ClientCodex, Type: contracts.EventUserPrompt, OccurredAt: base.Add(3 * time.Second), IdempotencyKey: "conversation-current", Payload: json.RawMessage(`{"prompt":"Do not include this turn"}`)},
		{EventID: "conversation-tool", ProjectID: projectID, SessionID: "old-session", Client: contracts.ClientCodex, Type: contracts.EventToolFinished, OccurredAt: base.Add(4 * time.Second), IdempotencyKey: "conversation-tool", Payload: json.RawMessage(`{"summary":"tool event is not conversation"}`)},
	}
	for _, event := range events {
		if inserted, err := store.InsertEvent(ctx, event); err != nil || !inserted {
			t.Fatalf("insert %s: inserted=%v err=%v", event.EventID, inserted, err)
		}
	}

	messages, err := store.ListConversation(ctx, projectID, "new-session", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || messages[0].Role != "user" || messages[0].Content != "What did we discuss?" || messages[1].Role != "assistant" || messages[1].Content != "The SQLite recovery flow." || messages[2].Type != contracts.EventCheckpointUpdated {
		t.Fatalf("conversation projection=%#v", messages)
	}
	for _, message := range messages {
		if strings.Contains(message.Content, "Do not include this turn") || strings.Contains(message.Content, "tool output must not become chat") {
			t.Fatalf("conversation projection leaked excluded/current data: %#v", messages)
		}
	}
}

func TestListConversationBoundsContent(t *testing.T) {
	store := openTaskTestStore(t)
	ctx := context.Background()
	projectID := "prj-conversation-bound-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: t.TempDir(), Name: "conversation bound"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertEvent(ctx, Event{
		EventID: "conversation-long", ProjectID: projectID, SessionID: "session", Client: contracts.ClientCodex,
		Type: contracts.EventUserPrompt, OccurredAt: time.Now().UTC(), IdempotencyKey: "conversation-long",
		Payload: json.RawMessage(`{"prompt":"` + strings.Repeat("x", 9000) + `"}`),
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := store.ListConversation(ctx, projectID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || len(messages[0].Content) > 8192 {
		t.Fatalf("conversation content was not bounded: %#v", messages)
	}
}

func TestListConversationReturnsNewestRowsAfterBoundedScan(t *testing.T) {
	store := openTaskTestStore(t)
	ctx := context.Background()
	projectID := "prj-conversation-newest-12345678"
	if err := store.RegisterProject(ctx, ProjectRecord{ProjectID: projectID, Root: t.TempDir(), Name: "conversation newest"}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC)
	for index := 0; index < 30; index++ {
		id := fmt.Sprintf("conversation-many-%02d", index)
		if inserted, err := store.InsertEvent(ctx, Event{
			EventID: id, ProjectID: projectID, SessionID: "old-session", Client: contracts.ClientCodex,
			Type: contracts.EventUserPrompt, OccurredAt: base.Add(time.Duration(index) * time.Second),
			IdempotencyKey: id, Payload: json.RawMessage(fmt.Sprintf(`{"prompt":"turn-%02d"}`, index)),
		}); err != nil || !inserted {
			t.Fatalf("insert %s: inserted=%v err=%v", id, inserted, err)
		}
	}

	messages, err := store.ListConversation(ctx, projectID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 10 || messages[0].Content != "turn-20" || messages[9].Content != "turn-29" {
		t.Fatalf("conversation projection did not retain newest rows: %#v", messages)
	}
}
