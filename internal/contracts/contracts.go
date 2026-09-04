package contracts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// HookClient is Baron’s stable client identity. It intentionally does not
// expose upstream DSH or Codex payload types to the core.
type HookClient string

const (
	ClientDSH      HookClient = "dsh"
	ClientCodex    HookClient = "codex"
	ClientHumanCLI HookClient = "human_cli"
	ClientSystem   HookClient = "system"
)

// EventType is the canonical event vocabulary shared by both adapters.
type EventType string

const (
	EventSessionStarted     EventType = "session_started"
	EventUserPrompt         EventType = "user_prompt"
	EventAssistantFinal     EventType = "assistant_final"
	EventToolStarted        EventType = "tool_started"
	EventToolFinished       EventType = "tool_finished"
	EventFileChanged        EventType = "file_changed"
	EventTestStarted        EventType = "test_started"
	EventTestFinished       EventType = "test_finished"
	EventErrorObserved      EventType = "error_observed"
	EventDecisionRecorded   EventType = "decision_recorded"
	EventCheckpointUpdated  EventType = "checkpoint_updated"
	EventSessionCleanClose  EventType = "session_clean_closed"
	EventSessionInterrupted EventType = "session_interrupted"
	EventMemoryQueued       EventType = "memory_queued"
	EventMemoryDelivered    EventType = "memory_delivered"
	EventHandoffStarted     EventType = "handoff_started"
	EventHandoffCompleted   EventType = "handoff_completed"
	EventTaskStarted        EventType = "task_started"
	EventTaskUpdated        EventType = "task_updated"
	EventTaskFailed         EventType = "task_failed"
	EventTaskBlocked        EventType = "task_blocked"
	EventTaskVerified       EventType = "task_verified"
	EventTaskCompleted      EventType = "task_completed"
	EventTaskInterrupted    EventType = "task_interrupted"
)

// TaskStatus is independent from session lifecycle. A clean session end never
// implies that a task completed.
type TaskStatus string

const (
	TaskPlanned     TaskStatus = "planned"
	TaskInProgress  TaskStatus = "in_progress"
	TaskBlocked     TaskStatus = "blocked"
	TaskFailed      TaskStatus = "failed"
	TaskCompleted   TaskStatus = "completed"
	TaskInterrupted TaskStatus = "interrupted"
)

// VerificationKind describes the scope of a successful verification. A
// successful lower-scope check is evidence, not task completion by itself.
type VerificationKind string

const (
	VerificationUnit        VerificationKind = "unit"
	VerificationIntegration VerificationKind = "integration"
	VerificationBuild       VerificationKind = "build"
	VerificationAcceptance  VerificationKind = "acceptance"
	VerificationCompletion  VerificationKind = "completion"
)

func (k VerificationKind) Valid() bool {
	switch k {
	case VerificationUnit, VerificationIntegration, VerificationBuild,
		VerificationAcceptance, VerificationCompletion:
		return true
	default:
		return false
	}
}

// CompletionPolicy controls which verification kind can promote a task to
// completed. The default is the explicit completion verification kind.
type CompletionPolicy string

const (
	CompletionPolicyCompletion CompletionPolicy = "completion"
	CompletionPolicyAcceptance CompletionPolicy = "acceptance"
)

func (p CompletionPolicy) Valid() bool {
	return p == CompletionPolicyCompletion || p == CompletionPolicyAcceptance
}

func (p CompletionPolicy) Allows(kind VerificationKind) bool {
	switch p {
	case CompletionPolicyAcceptance:
		return kind == VerificationAcceptance || kind == VerificationCompletion
	case CompletionPolicyCompletion:
		return kind == VerificationCompletion
	default:
		return false
	}
}

func IsTaskEvent(event EventType) bool {
	switch event {
	case EventTaskStarted, EventTaskUpdated, EventTaskFailed, EventTaskBlocked,
		EventTaskVerified, EventTaskCompleted, EventTaskInterrupted:
		return true
	default:
		return false
	}
}

type SessionState string

const (
	SessionActive      SessionState = "active"
	SessionCleanClosed SessionState = "clean_closed"
	SessionInterrupted SessionState = "interrupted"
	SessionStale       SessionState = "stale"
	SessionRecovered   SessionState = "recovered"
)

// IsolationContext is the only shape adapters may use for Tencent calls.
type IsolationContext struct {
	ProjectID string `json:"project_id"`
	TeamID    string `json:"team_id"`
	AgentID   string `json:"agent_id"`
	UserID    string `json:"user_id,omitempty"`
	ServiceID string `json:"service_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

func (c IsolationContext) Validate() error {
	if strings.TrimSpace(c.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	if strings.TrimSpace(c.TeamID) == "" {
		return errors.New("team_id is required")
	}
	if strings.TrimSpace(c.AgentID) == "" {
		return errors.New("agent_id is required")
	}
	return nil
}

type MemoryRecord struct {
	ID             string            `json:"id,omitempty"`
	ProjectID      string            `json:"project_id"`
	SourceClient   HookClient        `json:"source_client"`
	SessionID      string            `json:"session_id,omitempty"`
	Kind           string            `json:"kind"`
	Content        string            `json:"content"`
	EvidenceRefs   []string          `json:"evidence_refs,omitempty"`
	ContentHash    string            `json:"content_hash"`
	CreatedAt      time.Time         `json:"created_at"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	HistoricalOnly bool              `json:"historical_only"`
}

func (r *MemoryRecord) Normalize() {
	r.Content = strings.TrimSpace(r.Content)
	if r.ContentHash == "" {
		r.ContentHash = HashContent(r.Content)
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if r.SourceClient == "" {
		r.SourceClient = ClientSystem
	}
	if r.Metadata == nil {
		r.Metadata = map[string]string{}
	}
	r.HistoricalOnly = true
}

type MemoryQuery struct {
	Text         string
	Limit        int
	Since        time.Time
	Kinds        []string
	Files        []string
	Symbols      []string
	ScenarioPath string
}

type MemoryReceipt struct {
	RequestID      string    `json:"request_id"`
	ContentHash    string    `json:"content_hash"`
	DeliveredAt    time.Time `json:"delivered_at"`
	IdempotencyKey string    `json:"idempotency_key"`
}

// MemoryBackend is deliberately small. Tencent-specific metadata and HTTP
// details remain behind internal/memory/tencent.
type MemoryBackend interface {
	Health(context.Context) error
	EnsureIdentity(context.Context, IdentitySpec) (Identity, error)
	EnsureProjectAgent(context.Context, IsolationContext, string) (ProjectBinding, error)
	Capture(context.Context, IsolationContext, MemoryRecord, string) (MemoryReceipt, error)
	Search(context.Context, IsolationContext, MemoryQuery) ([]MemoryRecord, error)
}

// LayeredMemoryBackend is optional. Tencent v3 exposes profile/core and
// conversation layers in addition to atomic search; callers can use these
// methods when available without coupling the core to Tencent response types.
type LayeredMemoryBackend interface {
	MemoryBackend
	ReadCore(context.Context, IsolationContext, MemoryQuery) ([]MemoryRecord, error)
	ReadScenario(context.Context, IsolationContext, MemoryQuery) ([]MemoryRecord, error)
	SearchConversations(context.Context, IsolationContext, MemoryQuery) ([]MemoryRecord, error)
}

type IdentitySpec struct {
	UserName string
	TeamName string
}

type Identity struct {
	UserID            string
	UserKey           string
	TeamID            string
	TeamName          string
	Endpoint          string
	HubEndpoint       string
	KnowledgeEndpoint string
	ServiceID         string
}

type ProjectBinding struct {
	ProjectID string
	TeamID    string
	AgentID   string
	AgentName string
	UserID    string
}

func HashContent(content string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(content), " ")))
	return hex.EncodeToString(sum[:])
}

type AcceptanceContract struct {
	SchemaVersion   int                 `json:"schema_version"`
	Project         string              `json:"project"`
	Source          string              `json:"source"`
	GoBaseline      GoBaseline          `json:"go_baseline"`
	Contracts       []ContractEntry     `json:"contracts"`
	PhaseTests      map[string][]string `json:"phase_tests"`
	FinalAcceptance []string            `json:"final_acceptance"`
}

type GoBaseline struct {
	Language     string `json:"language"`
	Toolchain    string `json:"toolchain"`
	CGO          bool   `json:"cgo"`
	RustOrCargo  bool   `json:"rust_or_cargo_allowed"`
	SQLiteDriver string `json:"sqlite_driver"`
}

type ContractEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func LoadAcceptanceContract(path string) (AcceptanceContract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AcceptanceContract{}, err
	}
	var contract AcceptanceContract
	if err := json.Unmarshal(data, &contract); err != nil {
		return AcceptanceContract{}, fmt.Errorf("decode acceptance contract: %w", err)
	}
	if err := contract.Validate(); err != nil {
		return AcceptanceContract{}, err
	}
	return contract, nil
}

func (c AcceptanceContract) Validate() error {
	seen := make(map[string]bool, len(c.Contracts))
	for _, item := range c.Contracts {
		if item.ID == "" || seen[item.ID] {
			return fmt.Errorf("contract ID %q is missing or duplicated", item.ID)
		}
		seen[item.ID] = true
	}
	for index := 1; index <= 15; index++ {
		id := fmt.Sprintf("R%d", index)
		if !seen[id] {
			return fmt.Errorf("missing contract %s", id)
		}
	}
	finalSeen := make(map[string]bool, len(c.FinalAcceptance))
	for _, id := range c.FinalAcceptance {
		if finalSeen[id] {
			return fmt.Errorf("final acceptance ID %q is duplicated", id)
		}
		finalSeen[id] = true
	}
	for index := 1; index <= 24; index++ {
		id := fmt.Sprintf("F%02d", index)
		if !finalSeen[id] {
			return fmt.Errorf("missing final acceptance %s", id)
		}
	}
	return nil
}

func SortedEventTypes() []EventType {
	values := []EventType{
		EventSessionStarted, EventUserPrompt, EventAssistantFinal,
		EventToolStarted, EventToolFinished, EventFileChanged,
		EventTestStarted, EventTestFinished, EventErrorObserved,
		EventDecisionRecorded, EventCheckpointUpdated, EventSessionCleanClose,
		EventSessionInterrupted, EventMemoryQueued, EventMemoryDelivered,
		EventHandoffStarted, EventHandoffCompleted, EventTaskStarted,
		EventTaskUpdated, EventTaskFailed, EventTaskBlocked, EventTaskVerified,
		EventTaskCompleted, EventTaskInterrupted,
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}
