package storage

import (
	"context"
	"encoding/json"
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
