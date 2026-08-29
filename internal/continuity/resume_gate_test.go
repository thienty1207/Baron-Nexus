package continuity

import (
	"testing"

	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/storage"
)

func TestResumeGateUsesLocalEvidenceAndStrongOverlap(t *testing.T) {
	base := ResumeGateInput{
		LocalStateReadable: true,
		LedgerReadable:     true,
		Repository:         RepositoryEvidence{GitHead: "head-current", DiffHash: "diff-current"},
		RequestedTask: ResumeTaskScope{
			TaskID:        "task-new",
			ChangedFiles:  []string{"internal/api/handler.go"},
			ModulePaths:   []string{"internal/api"},
			Dependencies:  []string{"github.com/example/api"},
			ExplicitScope: true,
		},
	}
	tests := []struct {
		name    string
		input   ResumeGateInput
		outcome ResumeOutcome
		matched string
	}{
		{
			name:    "local state is sufficient without unresolved work",
			input:   base,
			outcome: ResumeLocalSufficient,
		},
		{
			name: "strong source file overlap requires resume",
			input: withUnresolved(base, storage.TaskRecord{
				TaskID: "task-source", Status: contracts.TaskFailed,
				FileEvidence: []storage.TaskFileEvidence{{Path: "internal/api/handler.go", Strength: "strong"}},
			}),
			outcome: ResumeOverlapRequiresResume,
			matched: "task-source",
		},
		{
			name: "same module overlap requires resume",
			input: withUnresolved(base, storage.TaskRecord{
				TaskID: "task-module", Status: contracts.TaskInterrupted,
				ModulePaths: []string{"internal/api"},
			}),
			outcome: ResumeOverlapRequiresResume,
			matched: "task-module",
		},
		{
			name: "shared declared dependency requires resume",
			input: withUnresolved(base, storage.TaskRecord{
				TaskID: "task-dependency", Status: contracts.TaskBlocked,
				Dependencies: []string{"github.com/example/api"},
			}),
			outcome: ResumeOverlapRequiresResume,
			matched: "task-dependency",
		},
		{
			name: "weak-only file overlap allows independent work",
			input: withUnresolved(base, storage.TaskRecord{
				TaskID: "task-docs", Status: contracts.TaskFailed,
				FileEvidence: []storage.TaskFileEvidence{{Path: "README.md", Strength: "weak"}},
			}),
			outcome: ResumeUnrelatedWorkAllowed,
		},
		{
			name: "unrelated unresolved work allows independent work",
			input: withUnresolved(base, storage.TaskRecord{
				TaskID: "task-other", Status: contracts.TaskFailed,
				FileEvidence: []storage.TaskFileEvidence{{Path: "internal/worker/queue.go", Strength: "strong"}},
				ModulePaths:  []string{"internal/worker"},
			}),
			outcome: ResumeUnrelatedWorkAllowed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := EvaluateResumeGate(test.input)
			if decision.Outcome != test.outcome || decision.MatchedTaskID != test.matched {
				t.Fatalf("decision=%#v want outcome=%s task=%s", decision, test.outcome, test.matched)
			}
		})
	}
}

func TestResumeGateSeparatesMissingStateHistoricalRecallAndMissingScope(t *testing.T) {
	base := ResumeGateInput{
		LocalStateReadable: true,
		LedgerReadable:     true,
		Repository:         RepositoryEvidence{GitHead: "head", DiffHash: "diff"},
		RequestedTask:      ResumeTaskScope{ExplicitScope: true},
	}
	cases := []struct {
		name    string
		input   ResumeGateInput
		outcome ResumeOutcome
	}{
		{
			name:    "missing local state requires recovery",
			input:   ResumeGateInput{RequestedTask: ResumeTaskScope{ExplicitScope: true}},
			outcome: ResumeRemoteRecoveryRequired,
		},
		{
			name:    "explicit historical recall requires remote",
			input:   ResumeGateInput{LocalStateReadable: true, LedgerReadable: true, Repository: base.Repository, RequestedTask: base.RequestedTask, HistoricalRecallRequested: true},
			outcome: ResumeRemoteRecoveryRequired,
		},
		{
			name:    "unresolved work without structured scope is explicit warning",
			input:   withUnresolved(ResumeGateInput{LocalStateReadable: true, LedgerReadable: true, Repository: base.Repository}, storage.TaskRecord{TaskID: "task-unknown-scope", Status: contracts.TaskInterrupted}),
			outcome: ResumeInsufficientStructuredTaskScope,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if decision := EvaluateResumeGate(test.input); decision.Outcome != test.outcome {
				t.Fatalf("decision=%#v want outcome=%s", decision, test.outcome)
			}
		})
	}
}

func TestResumeFingerprintChangesWhenRecoveryInputsChange(t *testing.T) {
	input := ResumeGateInput{
		LocalStateReadable: true,
		LedgerReadable:     true,
		Repository:         RepositoryEvidence{GitHead: "head", DiffHash: "diff"},
		RequestedTask: ResumeTaskScope{
			TaskID: "task-a", Goal: "implement API", ChangedFiles: []string{"internal/api/handler.go"}, ExplicitScope: true,
		},
	}
	first := EvaluateResumeGate(input)
	second := EvaluateResumeGate(input)
	if first.RecoveryFingerprint == "" || first.RecoveryFingerprint != second.RecoveryFingerprint {
		t.Fatalf("stable fingerprint missing: first=%#v second=%#v", first, second)
	}
	input.RequestedTask.ChangedFiles = append(input.RequestedTask.ChangedFiles, "internal/api/routes.go")
	third := EvaluateResumeGate(input)
	if third.RecoveryFingerprint == first.RecoveryFingerprint {
		t.Fatalf("scope change did not change fingerprint: %s", third.RecoveryFingerprint)
	}
}

func withUnresolved(input ResumeGateInput, task storage.TaskRecord) ResumeGateInput {
	input.UnresolvedTasks = []storage.TaskRecord{task}
	return input
}
