package continuity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/storage"
)

type TaskState struct {
	TaskID                    string                     `json:"task_id"`
	Goal                      string                     `json:"goal"`
	Status                    contracts.TaskStatus       `json:"status"`
	LastSuccessfulStep        string                     `json:"last_successful_step"`
	CurrentStep               string                     `json:"current_step"`
	NextAction                string                     `json:"next_action"`
	CompletionVerified        bool                       `json:"completion_verified"`
	CompletionPolicy          contracts.CompletionPolicy `json:"completion_policy"`
	LatestVerificationEventID string                     `json:"latest_verification_event_id,omitempty"`
	LatestVerificationKind    contracts.VerificationKind `json:"latest_verification_kind,omitempty"`
	LatestVerificationScope   string                     `json:"latest_verification_scope,omitempty"`
	LatestErrorRef            string                     `json:"latest_error_ref,omitempty"`
}

type TestEvidence struct {
	Command    string `json:"command"`
	Status     string `json:"status"`
	Summary    string `json:"summary"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
}

type ErrorEvidence struct {
	Class      string   `json:"class"`
	Summary    string   `json:"summary"`
	Files      []string `json:"files,omitempty"`
	ObservedAt string   `json:"observed_at,omitempty"`
}

type RepositoryEvidence struct {
	GitHead       string   `json:"git_head"`
	Branch        string   `json:"branch"`
	ChangedFiles  []string `json:"changed_files"`
	StatusSummary string   `json:"status_summary"`
	DiffHash      string   `json:"diff_hash"`
	ObservedAt    string   `json:"observed_at"`
}

type WorkState struct {
	SchemaVersion int                    `json:"schema_version"`
	ProjectID     string                 `json:"project_id"`
	ProjectName   string                 `json:"project_name"`
	ActiveTaskID  string                 `json:"active_task_id,omitempty"`
	Task          TaskState              `json:"task"`
	Repository    RepositoryEvidence     `json:"repository"`
	LatestTest    TestEvidence           `json:"latest_test"`
	Errors        []ErrorEvidence        `json:"errors,omitempty"`
	LastClient    contracts.HookClient   `json:"last_client"`
	SessionState  contracts.SessionState `json:"session_state"`
	SessionID     string                 `json:"session_id,omitempty"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

type Engine struct {
	store          *storage.Store
	projectID      string
	projectName    string
	checkpointPath string
	clock          func() time.Time
}

func NewEngine(store *storage.Store, projectID, projectName, checkpointPath string) *Engine {
	return &Engine{store: store, projectID: projectID, projectName: projectName, checkpointPath: checkpointPath, clock: time.Now}
}

func (e *Engine) Save(ctx context.Context, state WorkState) error {
	return e.save(ctx, state, true)
}

// SaveLocal persists canonical SQLite state without materializing the
// human-readable checkpoint. High-frequency tool hooks use this path to avoid
// contending on the cross-process checkpoint file lock; session boundaries
// still call Save so the checkpoint is refreshed.
func (e *Engine) SaveLocal(ctx context.Context, state WorkState) error {
	return e.save(ctx, state, false)
}

func (e *Engine) save(ctx context.Context, state WorkState, materializeCheckpoint bool) error {
	if e == nil || e.store == nil {
		return errors.New("continuity engine has no storage")
	}
	if state.SchemaVersion == 0 {
		state.SchemaVersion = 1
	}
	if state.ProjectID == "" {
		state.ProjectID = e.projectID
	}
	if state.ProjectName == "" {
		state.ProjectName = e.projectName
	}
	persist := func() error {
		var data []byte
		err := e.store.UpdateWorkState(ctx, state.ProjectID, func(existingData []byte) ([]byte, error) {
			if len(existingData) > 0 {
				var existing WorkState
				if json.Unmarshal(existingData, &existing) == nil {
					state = mergeWorkState(existing, state)
				}
			}
			if state.UpdatedAt.IsZero() {
				state.UpdatedAt = e.clock().UTC()
			} else {
				state.UpdatedAt = state.UpdatedAt.UTC()
			}
			encoded, marshalErr := json.MarshalIndent(state, "", "  ")
			if marshalErr != nil {
				return nil, fmt.Errorf("encode work state: %w", marshalErr)
			}
			data = encoded
			return encoded, nil
		})
		if err != nil {
			return err
		}
		if !materializeCheckpoint {
			return nil
		}
		// SQLite is authoritative. The readable checkpoint is a materialized
		// view and is written under the same short-lived process lock.
		if err := config.AtomicWriteFile(e.checkpointPath, append(data, '\n'), 0o600); err != nil {
			return fmt.Errorf("materialize checkpoint: %w", err)
		}
		return nil
	}
	if materializeCheckpoint {
		return e.withCheckpointLock(ctx, persist)
	}
	return persist()
}

func mergeWorkState(existing, next WorkState) WorkState {
	merged := next
	if merged.ProjectID == "" {
		merged.ProjectID = existing.ProjectID
	}
	if merged.ProjectName == "" {
		merged.ProjectName = existing.ProjectName
	}
	if merged.Task.Goal == "" {
		merged.Task.Goal = existing.Task.Goal
	}
	if merged.Task.TaskID == "" {
		merged.Task.TaskID = existing.Task.TaskID
	}
	if merged.Task.LastSuccessfulStep == "" {
		merged.Task.LastSuccessfulStep = existing.Task.LastSuccessfulStep
	}
	if merged.Task.CurrentStep == "" {
		merged.Task.CurrentStep = existing.Task.CurrentStep
	}
	if merged.Task.NextAction == "" {
		merged.Task.NextAction = existing.Task.NextAction
	}
	if merged.Task.Status == "" || (merged.Task.Status == contracts.TaskInProgress && existing.Task.Status == contracts.TaskCompleted && !merged.Task.CompletionVerified) {
		merged.Task.Status = existing.Task.Status
	}
	if existing.Task.CompletionVerified {
		merged.Task.CompletionVerified = true
	}
	if merged.Task.CompletionPolicy == "" {
		merged.Task.CompletionPolicy = existing.Task.CompletionPolicy
	}
	if merged.Task.LatestVerificationEventID == "" {
		merged.Task.LatestVerificationEventID = existing.Task.LatestVerificationEventID
	}
	if merged.Task.LatestVerificationKind == "" {
		merged.Task.LatestVerificationKind = existing.Task.LatestVerificationKind
	}
	if merged.Task.LatestVerificationScope == "" {
		merged.Task.LatestVerificationScope = existing.Task.LatestVerificationScope
	}
	if merged.Task.LatestErrorRef == "" {
		merged.Task.LatestErrorRef = existing.Task.LatestErrorRef
	}
	if merged.LatestTest.Command == "" && merged.LatestTest.Summary == "" {
		merged.LatestTest = existing.LatestTest
	}
	if merged.Repository.GitHead == "" {
		merged.Repository.GitHead = existing.Repository.GitHead
	}
	if merged.Repository.Branch == "" {
		merged.Repository.Branch = existing.Repository.Branch
	}
	if merged.Repository.DiffHash == "" {
		merged.Repository.DiffHash = existing.Repository.DiffHash
	}
	if merged.Repository.StatusSummary == "" {
		merged.Repository.StatusSummary = existing.Repository.StatusSummary
	}
	merged.Repository.ChangedFiles = unionStrings(existing.Repository.ChangedFiles, merged.Repository.ChangedFiles, 500)
	merged.Errors = mergeErrors(existing.Errors, merged.Errors, 20)
	if merged.LastClient == "" {
		merged.LastClient = existing.LastClient
	}
	if merged.SessionID == "" {
		merged.SessionID = existing.SessionID
	}
	if merged.ActiveTaskID == "" {
		merged.ActiveTaskID = existing.ActiveTaskID
	}
	return merged
}

func unionStrings(existing, next []string, limit int) []string {
	seen := make(map[string]bool)
	merged := make([]string, 0, len(existing)+len(next))
	for _, values := range [][]string{existing, next} {
		for _, value := range values {
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			merged = append(merged, value)
			if len(merged) >= limit {
				return merged
			}
		}
	}
	return merged
}

func mergeErrors(existing, next []ErrorEvidence, limit int) []ErrorEvidence {
	merged := make([]ErrorEvidence, 0, len(existing)+len(next))
	seen := make(map[string]bool)
	for _, values := range [][]ErrorEvidence{existing, next} {
		for _, value := range values {
			key := value.Class + "\x00" + value.Summary
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, value)
			if len(merged) >= limit {
				return merged
			}
		}
	}
	return merged
}

func (e *Engine) Load(ctx context.Context) (WorkState, error) {
	data, err := e.store.GetWorkState(ctx, e.projectID)
	if err != nil {
		if checkpoint, checkpointErr := e.readCheckpoint(); checkpointErr == nil {
			return checkpoint, nil
		} else if errors.Is(err, sql.ErrNoRows) && errors.Is(checkpointErr, os.ErrNotExist) {
			return WorkState{}, sql.ErrNoRows
		}
		return WorkState{}, fmt.Errorf("load local work state: %w", err)
	}
	var state WorkState
	if err := json.Unmarshal(data, &state); err != nil {
		if checkpoint, checkpointErr := e.readCheckpoint(); checkpointErr == nil {
			return checkpoint, nil
		}
		return WorkState{}, fmt.Errorf("decode checkpoint: %w", err)
	}
	return state, nil
}

func (e *Engine) readCheckpoint() (WorkState, error) {
	data, err := os.ReadFile(e.checkpointPath)
	if err != nil {
		return WorkState{}, err
	}
	var state WorkState
	if err := json.Unmarshal(data, &state); err != nil {
		return WorkState{}, fmt.Errorf("decode checkpoint fallback: %w", err)
	}
	return state, nil
}

func ClassifySession(state contracts.SessionState, lastSeen, now time.Time, staleAfter time.Duration) contracts.SessionState {
	if state != contracts.SessionActive {
		return state
	}
	if staleAfter <= 0 {
		staleAfter = 2 * time.Minute
	}
	if now.Sub(lastSeen) >= staleAfter {
		return contracts.SessionInterrupted
	}
	return contracts.SessionActive
}

func InspectRepository(ctx context.Context, root string) (RepositoryEvidence, error) {
	evidence := RepositoryEvidence{ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), ChangedFiles: []string{}}
	command := func(args ...string) (string, error) {
		commandCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(commandCtx, "git", append([]string{"-C", root}, args...)...)
		output, err := cmd.Output()
		if err != nil {
			return "", err
		}
		return string(output), nil
	}
	head, err := command("rev-parse", "HEAD")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "exit status") {
			evidence.StatusSummary = "not a git repository or no commit"
			return evidence, nil
		}
		return evidence, err
	}
	evidence.GitHead = strings.TrimSpace(head)
	branch, _ := command("rev-parse", "--abbrev-ref", "HEAD")
	evidence.Branch = strings.TrimSpace(branch)
	status, _ := command("status", "--short")
	evidence.StatusSummary = bounded(status, 64*1024)
	for _, line := range strings.Split(strings.TrimRight(status, "\r\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) > 3 {
			line = line[3:]
		}
		evidence.ChangedFiles = append(evidence.ChangedFiles, bounded(strings.TrimSpace(line), 1024))
		if len(evidence.ChangedFiles) == 500 {
			break
		}
	}
	diff, _ := command("diff", "--no-ext-diff", "--no-color", "--unified=0")
	sum := sha256.Sum256([]byte(bounded(diff, 256*1024)))
	evidence.DiffHash = hex.EncodeToString(sum[:])
	return evidence, nil
}

func bounded(value string, max int) string {
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

// CheckpointPath returns the conventional readable snapshot path for a root.
func CheckpointPath(root string) string {
	return filepath.Join(root, ".baron", "checkpoint.json")
}
