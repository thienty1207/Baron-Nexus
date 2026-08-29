package continuity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/baron-shared-brain/baron/internal/storage"
)

type ResumeOutcome string

const (
	ResumeLocalSufficient                 ResumeOutcome = "local_sufficient"
	ResumeRemoteRecoveryRequired          ResumeOutcome = "remote_recovery_required"
	ResumeOverlapRequiresResume           ResumeOutcome = "overlap_requires_resume"
	ResumeUnrelatedWorkAllowed            ResumeOutcome = "unrelated_work_allowed"
	ResumeInsufficientStructuredTaskScope ResumeOutcome = "insufficient_structured_task_scope"
)

type ResumeTaskScope struct {
	TaskID        string
	Goal          string
	ChangedFiles  []string
	ModulePaths   []string
	Dependencies  []string
	GitHead       string
	DiffHash      string
	FileEvidence  []storage.TaskFileEvidence
	ExplicitScope bool
}

type ResumeGateInput struct {
	LocalStateReadable        bool
	LedgerReadable            bool
	Repository                RepositoryEvidence
	UnresolvedTasks           []storage.TaskRecord
	RequestedTask             ResumeTaskScope
	HistoricalRecallRequested bool
}

type ResumeDecision struct {
	Outcome             ResumeOutcome
	MatchedTaskID       string
	Reason              string
	RecoveryFingerprint string
}

// EvaluateResumeGate is deliberately a pure local function. It uses only
// structured repository/task evidence and never consults an LLM or Tencent.
func EvaluateResumeGate(input ResumeGateInput) ResumeDecision {
	decision := ResumeDecision{RecoveryFingerprint: RecoveryFingerprint(input)}
	if !input.LocalStateReadable || !input.LedgerReadable || !repositoryReadable(input.Repository) {
		decision.Outcome = ResumeRemoteRecoveryRequired
		decision.Reason = "local continuity or repository evidence is missing"
		return decision
	}
	if input.HistoricalRecallRequested {
		decision.Outcome = ResumeRemoteRecoveryRequired
		decision.Reason = "historical recall was explicitly requested"
		return decision
	}
	if len(input.UnresolvedTasks) == 0 {
		decision.Outcome = ResumeLocalSufficient
		decision.Reason = "local task ledger has no unresolved work"
		return decision
	}
	if strings.TrimSpace(input.RequestedTask.TaskID) != "" {
		for _, task := range input.UnresolvedTasks {
			if task.TaskID == input.RequestedTask.TaskID {
				decision.Outcome = ResumeOverlapRequiresResume
				decision.MatchedTaskID = task.TaskID
				decision.Reason = "requested task matches unresolved task_id"
				return decision
			}
		}
	}
	if !hasStructuredScope(input.RequestedTask) {
		decision.Outcome = ResumeInsufficientStructuredTaskScope
		decision.Reason = "unresolved work exists but the requested task has no structured scope"
		return decision
	}
	for _, task := range input.UnresolvedTasks {
		strong, weakOnly := taskScopeOverlap(task, input.RequestedTask)
		if strong {
			decision.Outcome = ResumeOverlapRequiresResume
			decision.MatchedTaskID = task.TaskID
			decision.Reason = "unresolved task has strong file, module, dependency, or diff overlap"
			return decision
		}
		if weakOnly {
			// Weak evidence is intentionally recorded in the decision fingerprint,
			// but cannot block independent work by itself.
			continue
		}
	}
	decision.Outcome = ResumeUnrelatedWorkAllowed
	decision.Reason = "unresolved tasks have no strong overlap with the requested scope"
	return decision
}

func RecoveryFingerprint(input ResumeGateInput) string {
	parts := []string{
		fmt.Sprintf("historical=%t", input.HistoricalRecallRequested),
		input.Repository.GitHead,
		input.Repository.DiffHash,
		input.Repository.Branch,
		input.RequestedTask.TaskID,
		input.RequestedTask.Goal,
		strings.Join(sortedNormalized(input.RequestedTask.ChangedFiles), ","),
		strings.Join(sortedNormalized(input.RequestedTask.ModulePaths), ","),
		strings.Join(sortedNormalized(input.RequestedTask.Dependencies), ","),
	}
	ids := make([]string, 0, len(input.UnresolvedTasks))
	for _, task := range input.UnresolvedTasks {
		ids = append(ids, strings.Join([]string{
			task.TaskID, string(task.Status), task.Goal, task.CurrentStep, task.NextAction,
			task.GitHead, task.DiffHash, strings.Join(sortedNormalized(task.ChangedFiles), ","),
			strings.Join(sortedNormalized(task.ModulePaths), ","), strings.Join(sortedNormalized(task.Dependencies), ","),
			task.UpdatedAt.UTC().String(),
		}, "|"))
	}
	sort.Strings(ids)
	parts = append(parts, ids...)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func repositoryReadable(repository RepositoryEvidence) bool {
	return strings.TrimSpace(repository.GitHead) != "" && strings.TrimSpace(repository.DiffHash) != ""
}

func hasStructuredScope(scope ResumeTaskScope) bool {
	return scope.ExplicitScope || strings.TrimSpace(scope.Goal) != "" || len(scope.ChangedFiles) > 0 ||
		len(scope.ModulePaths) > 0 || len(scope.Dependencies) > 0 || strings.TrimSpace(scope.GitHead) != "" ||
		strings.TrimSpace(scope.DiffHash) != ""
}

func taskScopeOverlap(task storage.TaskRecord, requested ResumeTaskScope) (strong, weakOnly bool) {
	taskStrongFiles := make(map[string]bool)
	taskWeakFiles := make(map[string]bool)
	if len(task.FileEvidence) > 0 {
		for _, file := range task.FileEvidence {
			addFileEvidence(taskStrongFiles, taskWeakFiles, file.Path, file.Strength)
		}
	} else {
		for _, file := range task.ChangedFiles {
			addFileEvidence(taskStrongFiles, taskWeakFiles, file, storage.TaskFileStrength(file))
		}
	}
	requestedStrongFiles := make(map[string]bool)
	requestedWeakFiles := make(map[string]bool)
	if len(requested.FileEvidence) > 0 {
		for _, file := range requested.FileEvidence {
			addFileEvidence(requestedStrongFiles, requestedWeakFiles, file.Path, file.Strength)
		}
	} else {
		for _, file := range requested.ChangedFiles {
			addFileEvidence(requestedStrongFiles, requestedWeakFiles, file, storage.TaskFileStrength(file))
		}
	}
	for file := range requestedStrongFiles {
		if taskStrongFiles[file] {
			return true, false
		}
	}
	if sharedValues(task.ModulePaths, requested.ModulePaths) || sharedValues(task.Dependencies, requested.Dependencies) {
		return true, false
	}
	if task.GitHead != "" && requested.GitHead != "" && task.GitHead == requested.GitHead &&
		task.DiffHash != "" && requested.DiffHash != "" && task.DiffHash == requested.DiffHash {
		return true, false
	}
	for file := range requestedWeakFiles {
		if taskWeakFiles[file] && !taskStrongFiles[file] {
			return false, true
		}
	}
	return false, false
}

func addFileEvidence(strong, weak map[string]bool, path, strength string) {
	path = normalizePath(path)
	if path == "" {
		return
	}
	if strings.EqualFold(strings.TrimSpace(strength), "weak") || storage.TaskFileStrength(path) == "weak" {
		weak[path] = true
		return
	}
	strong[path] = true
}

func sharedValues(left, right []string) bool {
	seen := make(map[string]bool, len(left))
	for _, value := range left {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			seen[value] = true
		}
	}
	for _, value := range right {
		if seen[strings.ToLower(strings.TrimSpace(value))] {
			return true
		}
	}
	return false
}

func normalizePath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	return strings.TrimPrefix(path, "./")
}

func sortedNormalized(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizePath(value)
		if value != "" {
			result = append(result, strings.ToLower(value))
		}
	}
	sort.Strings(result)
	return result
}

// BuildLocalResumeContext renders only bounded local state. It is used when
// the Resume Gate says Tencent is unnecessary or when unresolved work must be
// shown before any historical reference.
func BuildLocalResumeContext(local WorkState, tasks []storage.TaskRecord, decision ResumeDecision, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 5000
	}
	var builder strings.Builder
	builder.WriteString("<baron-local-resume trust=\"local-authoritative\">\n")
	if decision.Outcome == ResumeRemoteRecoveryRequired {
		builder.WriteString("SQLite/Git evidence is current authority; any Tencent result is historical reference only.\n")
	} else {
		builder.WriteString("Tencent was not queried; SQLite/Git evidence is current authority.\n")
	}
	if local.ActiveTaskID != "" {
		builder.WriteString("Active task: ")
		builder.WriteString(html.EscapeString(local.ActiveTaskID))
		builder.WriteByte('\n')
	}
	if decision.Outcome != "" {
		builder.WriteString("Resume decision: ")
		builder.WriteString(html.EscapeString(string(decision.Outcome)))
		builder.WriteString("; ")
		builder.WriteString(html.EscapeString(bounded(decision.Reason, 512)))
		builder.WriteByte('\n')
	}
	if len(tasks) == 0 && local.Task.Goal != "" {
		builder.WriteString(fmt.Sprintf("Current local task: goal=%s; status=%s; step=%s; next=%s\n",
			html.EscapeString(bounded(local.Task.Goal, 1024)), html.EscapeString(string(local.Task.Status)),
			html.EscapeString(bounded(local.Task.CurrentStep, 512)), html.EscapeString(bounded(local.Task.NextAction, 512))))
	}
	if len(tasks) > 0 {
		builder.WriteString("Unresolved local tasks:\n")
		for _, task := range tasks {
			line := fmt.Sprintf("- %s status=%s goal=%s step=%s next=%s files=%s verify=%s/%s\n",
				html.EscapeString(task.TaskID), html.EscapeString(string(task.Status)),
				html.EscapeString(bounded(task.Goal, 768)), html.EscapeString(bounded(task.CurrentStep, 512)),
				html.EscapeString(bounded(task.NextAction, 512)), html.EscapeString(strings.Join(sortedNormalized(task.ChangedFiles), ",")),
				html.EscapeString(string(task.LatestVerificationKind)), html.EscapeString(bounded(task.LatestVerificationScope, 256)))
			if builder.Len()+len(line) > maxChars-100 {
				break
			}
			builder.WriteString(line)
		}
	}
	builder.WriteString("</baron-local-resume>")
	return truncate(builder.String(), maxChars)
}
