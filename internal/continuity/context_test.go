package continuity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/baron-shared-brain/baron/internal/contracts"
)

type fakeMemory struct {
	records []contracts.MemoryRecord
	err     error
}

type fakeKnowledge struct {
	citations []KnowledgeCitation
	err       error
}

func (k fakeKnowledge) Retrieve(context.Context, contracts.IsolationContext, contracts.MemoryQuery) ([]KnowledgeCitation, error) {
	return k.citations, k.err
}

func (m fakeMemory) Health(context.Context) error { return m.err }
func (m fakeMemory) EnsureIdentity(context.Context, contracts.IdentitySpec) (contracts.Identity, error) {
	return contracts.Identity{}, nil
}
func (m fakeMemory) EnsureProjectAgent(context.Context, contracts.IsolationContext, string) (contracts.ProjectBinding, error) {
	return contracts.ProjectBinding{}, nil
}
func (m fakeMemory) Capture(context.Context, contracts.IsolationContext, contracts.MemoryRecord, string) (contracts.MemoryReceipt, error) {
	return contracts.MemoryReceipt{}, m.err
}
func (m fakeMemory) Search(_ context.Context, isolation contracts.IsolationContext, _ contracts.MemoryQuery) ([]contracts.MemoryRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []contracts.MemoryRecord
	for _, record := range m.records {
		if record.ProjectID == isolation.ProjectID {
			result = append(result, record)
		}
	}
	return result, nil
}

func TestContextPacketPrioritizesLocalStateAndDeduplicatesRemoteMemory(t *testing.T) {
	backend := fakeMemory{records: []contracts.MemoryRecord{
		{ProjectID: "prj-a-12345678", SourceClient: contracts.ClientCodex, Kind: "decision", Content: "Use refresh tokens", ContentHash: contracts.HashContent("Use refresh tokens")},
		{ProjectID: "prj-a-12345678", SourceClient: contracts.ClientDSH, Kind: "decision", Content: "Use refresh tokens", ContentHash: contracts.HashContent("Use refresh tokens")},
		{ProjectID: "prj-b-12345678", SourceClient: contracts.ClientDSH, Kind: "decision", Content: "B secret", ContentHash: contracts.HashContent("B secret")},
	}}
	state := WorkState{ProjectID: "prj-a-12345678", ProjectName: "A", Task: TaskState{Goal: "Implement auth", CurrentStep: "Run tests", NextAction: "rerun auth tests"}}
	packet, err := BuildContext(context.Background(), state, backend, contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team-a", AgentID: "agt-a", UserID: "usr-a"}, contracts.MemoryQuery{Text: "auth"}, 2048, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(packet.Text, "Use refresh tokens") != 1 || strings.Contains(packet.Text, "B secret") {
		t.Fatalf("context did not deduplicate/isolate: %s", packet.Text)
	}
	if !strings.Contains(packet.Text, "Implement auth") || !strings.Contains(packet.Text, "historical-reference-only") {
		t.Fatalf("local state or trust boundary missing: %s", packet.Text)
	}
}

func TestMemoryCaptureRedactsExactCredentialBeforePersistence(t *testing.T) {
	record := PrepareMemoryRecord(contracts.MemoryRecord{ProjectID: "prj-a-12345678", Content: "tool output sk-live-secret", SourceClient: contracts.ClientCodex}, []string{"sk-live-secret"})
	if strings.Contains(record.Content, "sk-live-secret") || !strings.Contains(record.Content, "[REDACTED]") {
		t.Fatalf("record secret survived: %#v", record)
	}
}

func TestTencentFailureReturnsLocalContextWithoutBlocking(t *testing.T) {
	state := WorkState{ProjectID: "prj-a-12345678", Task: TaskState{Goal: "Continue offline"}}
	packet, err := BuildContext(context.Background(), state, fakeMemory{err: errors.New("network down")}, contracts.IsolationContext{ProjectID: "prj-a-12345678", TeamID: "team", AgentID: "agent", UserID: "user"}, contracts.MemoryQuery{Text: "offline"}, 1024, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(packet.Text, "Continue offline") || packet.RemoteError == "" {
		t.Fatalf("offline context lost local state: %#v", packet)
	}
}

func TestContextPacketHonorsStrictCharacterBound(t *testing.T) {
	state := WorkState{ProjectID: "prj-a-12345678", Task: TaskState{Goal: strings.Repeat("goal ", 100)}}
	packet, err := BuildContext(context.Background(), state, nil, contracts.IsolationContext{ProjectID: state.ProjectID, TeamID: "team", AgentID: "agent"}, contracts.MemoryQuery{}, 64, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(packet.Text) > 64 {
		t.Fatalf("context exceeded bound: len=%d text=%q", len(packet.Text), packet.Text)
	}
}

func TestContextPacketIncludesBoundedKnowledgeCitationsWithTrustLabels(t *testing.T) {
	state := WorkState{ProjectID: "prj-a-12345678", Task: TaskState{Goal: "update auth"}}
	packet, err := BuildContextWithKnowledge(context.Background(), state, nil, fakeKnowledge{citations: []KnowledgeCitation{{Source: "wiki", Reference: "docs/auth.md", Content: "refresh token flow", Trust: "historical-reference-only", Freshness: "commit-1"}}}, contracts.IsolationContext{ProjectID: state.ProjectID, TeamID: "team", AgentID: "agent", UserID: "user"}, contracts.MemoryQuery{Text: "auth"}, 1024, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(packet.Knowledge) != 1 || !strings.Contains(packet.Text, "Knowledge [wiki") || !strings.Contains(packet.Text, "docs/auth.md") || !strings.Contains(packet.Text, "historical-reference-only") {
		t.Fatalf("knowledge citation was not labeled in context: %#v", packet)
	}
}

func TestKnowledgeOutageKeepsLocalContextAndDoesNotClaimFreshness(t *testing.T) {
	state := WorkState{ProjectID: "prj-a-12345678", Task: TaskState{Goal: "continue offline"}}
	packet, err := BuildContextWithKnowledge(context.Background(), state, nil, fakeKnowledge{err: errors.New("service unavailable")}, contracts.IsolationContext{ProjectID: state.ProjectID, TeamID: "team", AgentID: "agent", UserID: "user"}, contracts.MemoryQuery{Text: "offline"}, 1024, nil)
	if err != nil {
		t.Fatal(err)
	}
	if packet.RemoteError == "" || !strings.Contains(packet.Text, "continue offline") || strings.Contains(packet.Text, "verified current") {
		t.Fatalf("knowledge outage violated fail-open trust contract: %#v", packet)
	}
}

func TestStaleKnowledgeStatusRemainsHistoricalOnly(t *testing.T) {
	state := WorkState{ProjectID: "prj-stale-12345678", Task: TaskState{Goal: "verify auth", CurrentStep: "run tests", NextAction: "inspect failure"}}
	packet, err := BuildContextWithKnowledge(context.Background(), state, nil, fakeKnowledge{citations: []KnowledgeCitation{
		{Source: "codegraph-status", Reference: "graph-1", Content: "CodeGraph status=pending", Trust: "historical-reference-only", Freshness: "stale"},
		{Source: "wiki-status", Reference: "wiki-1", Content: "Wiki status=ready", Trust: "historical-reference-only", Freshness: "stale"},
	}}, contracts.IsolationContext{ProjectID: state.ProjectID, TeamID: "team", AgentID: "agent", UserID: "user"}, contracts.MemoryQuery{Kinds: []string{"session_start"}}, 2048, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"trust=historical-reference-only", "freshness=stale", "Current repository/local continuity is authoritative."} {
		if !strings.Contains(packet.Text, want) {
			t.Fatalf("stale knowledge boundary missing %q: %s", want, packet.Text)
		}
	}
	if strings.Contains(packet.Text, "current verified source") {
		t.Fatalf("stale knowledge was presented as current evidence: %s", packet.Text)
	}
}
