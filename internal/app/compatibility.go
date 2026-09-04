package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
	_ "modernc.org/sqlite"
)

const (
	compatibilityMaxFileBytes = 4 << 20
	compatibilityMaxRows      = 2048
)

// CompatibilityManifest is a bounded, secret-free summary used by the
// legacy upgrade gate. It proves that durable identities and evidence survive
// an update without treating the working tree as mutable installation state.
type CompatibilityManifest struct {
	GitHead                string            `json:"git_head,omitempty"`
	GitStatusHash          string            `json:"git_status_hash,omitempty"`
	SQLiteSchema           string            `json:"sqlite_schema,omitempty"`
	SQLiteRowIDs           []string          `json:"sqlite_row_ids,omitempty"`
	SQLiteCounts           map[string]int    `json:"sqlite_counts,omitempty"`
	TencentIDs             []string          `json:"tencent_ids,omitempty"`
	TencentStates          map[string]string `json:"tencent_states,omitempty"`
	DockerSentinels        map[string]string `json:"docker_sentinels,omitempty"`
	DSHHash                string            `json:"dsh_hash,omitempty"`
	CodexHash              string            `json:"codex_hash,omitempty"`
	HookCount              int               `json:"hook_count,omitempty"`
	CredentialFingerprints []string          `json:"credential_fingerprints,omitempty"`
	ComponentVersions      map[string]string `json:"component_versions,omitempty"`
}

// CompatibilityFixture identifies the state surfaces to inspect. Manifest
// paths are optional; when omitted, the legacy gate uses files under .baron.
type CompatibilityFixture struct {
	Root               string `json:"root"`
	GlobalConfig       string `json:"global_config"`
	SQLitePath         string `json:"sqlite_path"`
	BeforeManifestPath string `json:"before_manifest_path,omitempty"`
	AfterManifestPath  string `json:"after_manifest_path,omitempty"`
}

type CompatibilityResult struct {
	Passed  bool     `json:"passed"`
	Reasons []string `json:"reasons,omitempty"`
}

var compatibilityTables = []struct {
	name string
	ids  string
}{
	{name: "projects", ids: "project_id"},
	{name: "sessions", ids: "session_id"},
	{name: "events", ids: "event_id"},
	{name: "work_state", ids: "project_id"},
	{name: "sync_queue", ids: "queue_id"},
	{name: "memory_receipts", ids: "idempotency_key"},
	{name: "queue_receipts", ids: "receipt_id"},
	{name: "locks", ids: "lock_name"},
	{name: "handoff_receipts", ids: "receipt_id"},
	{name: "knowledge_registry", ids: "project_id"},
	{name: "tasks", ids: "project_id || '/' || task_id"},
	{name: "task_files", ids: "project_id || '/' || task_id || '/' || path"},
	{name: "task_modules", ids: "project_id || '/' || task_id || '/' || module_path"},
	{name: "task_dependencies", ids: "project_id || '/' || task_id || '/' || dependency"},
	{name: "task_verifications", ids: "verification_id"},
	{name: "active_tasks", ids: "project_id"},
	{name: "remote_recall_cache", ids: "project_id || '/' || session_id || '/' || fingerprint || '/' || query_hash"},
	{name: "pentest_jobs", ids: "job_id"},
	{name: "pentest_events", ids: "event_id"},
	{name: "pentest_findings", ids: "finding_id"},
	{name: "pentest_artifacts", ids: "artifact_id"},
}

// CaptureCompatibilityManifest reads only bounded identity and state
// metadata. It never runs migrations, sends data to Tencent, or serializes
// payloads, source contents, or credential values.
func CaptureCompatibilityManifest(ctx context.Context, fixture CompatibilityFixture) (CompatibilityManifest, error) {
	root, err := compatibilityRoot(fixture.Root)
	if err != nil {
		return CompatibilityManifest{}, err
	}
	manifest := CompatibilityManifest{
		SQLiteCounts:      map[string]int{},
		TencentStates:     map[string]string{},
		DockerSentinels:   map[string]string{},
		ComponentVersions: map[string]string{},
	}
	manifest.GitHead, _ = compatibilityGit(ctx, root, "rev-parse", "HEAD")
	status, statusErr := compatibilityGit(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
	if statusErr == nil {
		manifest.GitStatusHash = hashText(status)
	}

	globalPath := strings.TrimSpace(fixture.GlobalConfig)
	if globalPath == "" {
		globalPath, err = config.DefaultGlobalStatePath()
		if err != nil {
			return CompatibilityManifest{}, fmt.Errorf("resolve global Baron state: %w", err)
		}
	}
	global, err := config.LoadGlobalState(globalPath)
	if err != nil {
		return CompatibilityManifest{}, fmt.Errorf("read global Baron state: %w", err)
	}
	addGlobalTencentEvidence(&manifest, global)
	manifest.DSHHash, err = redactedOwnedSurfaceHash(global.DSHConfigPath, false)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return CompatibilityManifest{}, fmt.Errorf("read DSH compatibility surface: %w", err)
	}
	manifest.CodexHash, err = redactedOwnedSurfaceHash(global.CodexHooksPath, true)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return CompatibilityManifest{}, fmt.Errorf("read Codex compatibility surface: %w", err)
	}
	manifest.HookCount, err = countBaronHooks(global.CodexHooksPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return CompatibilityManifest{}, fmt.Errorf("read Baron hooks: %w", err)
	}
	manifest.CredentialFingerprints = credentialFingerprints(global)
	manifest.ComponentVersions, err = managedComponentVersions(global)
	if err != nil {
		return CompatibilityManifest{}, err
	}

	sqlitePath := strings.TrimSpace(fixture.SQLitePath)
	if sqlitePath == "" {
		sqlitePath = filepath.Join(root, ".baron", "runtime", "state.db")
	}
	sqliteEvidence, err := captureSQLiteEvidence(ctx, sqlitePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			manifest.SQLiteSchema = "missing"
		} else {
			return CompatibilityManifest{}, err
		}
	} else {
		manifest.SQLiteSchema = sqliteEvidence.schema
		manifest.SQLiteRowIDs = sqliteEvidence.rowIDs
		manifest.SQLiteCounts = sqliteEvidence.counts
		for key, value := range sqliteEvidence.tencentStates {
			manifest.TencentStates[key] = value
		}
		for _, id := range sqliteEvidence.tencentIDs {
			manifest.TencentIDs = append(manifest.TencentIDs, id)
		}
	}

	manifest.SQLiteRowIDs = uniqueSorted(manifest.SQLiteRowIDs)
	manifest.TencentIDs = uniqueSorted(manifest.TencentIDs)
	manifest.CredentialFingerprints = uniqueSorted(manifest.CredentialFingerprints)
	return manifest, nil
}

// CompareCompatibilityManifest rejects only durable state loss. Git head and
// status are intentionally not compared byte-for-byte because an update may
// rewrite Baron-owned integration files in the working tree.
func CompareCompatibilityManifest(before, after CompatibilityManifest) CompatibilityResult {
	reasons := make([]string, 0)
	if schemaVersion(after.SQLiteSchema) < schemaVersion(before.SQLiteSchema) {
		reasons = append(reasons, fmt.Sprintf("SQLite schema regressed from %s to %s", before.SQLiteSchema, after.SQLiteSchema))
	} else if schemaVersion(before.SQLiteSchema) == 0 && schemaVersion(after.SQLiteSchema) == 0 && before.SQLiteSchema != after.SQLiteSchema {
		reasons = append(reasons, fmt.Sprintf("SQLite schema changed from %s to %s without a versioned migration", before.SQLiteSchema, after.SQLiteSchema))
	}
	for _, id := range missingValues(before.SQLiteRowIDs, after.SQLiteRowIDs) {
		reasons = append(reasons, "SQLite row IDs lost: "+id)
	}
	for table, count := range before.SQLiteCounts {
		if after.SQLiteCounts[table] < count {
			reasons = append(reasons, fmt.Sprintf("SQLite counts lost for table %s: %d -> %d", table, count, after.SQLiteCounts[table]))
		}
	}
	for _, id := range missingValues(before.TencentIDs, after.TencentIDs) {
		reasons = append(reasons, "Tencent identity or asset ID lost: "+id)
	}
	for key, value := range before.TencentStates {
		got, ok := after.TencentStates[key]
		if !ok || strings.TrimSpace(got) == "" {
			reasons = append(reasons, "Tencent state lost: "+key)
		}
		_ = value // Non-empty state transitions such as pending -> ready are valid.
	}
	for key, value := range before.DockerSentinels {
		if got, ok := after.DockerSentinels[key]; !ok || got != value {
			reasons = append(reasons, "Docker sentinel changed: "+key)
		}
	}
	if before.DSHHash != "" && before.DSHHash != after.DSHHash {
		reasons = append(reasons, "DSH user-owned configuration changed")
	}
	if before.CodexHash != "" && before.CodexHash != after.CodexHash {
		reasons = append(reasons, "Codex user-owned hooks changed")
	}
	if after.HookCount < before.HookCount {
		reasons = append(reasons, fmt.Sprintf("hook count regressed: %d -> %d", before.HookCount, after.HookCount))
	}
	for _, fingerprint := range missingValues(before.CredentialFingerprints, after.CredentialFingerprints) {
		reasons = append(reasons, "credential shape lost: "+fingerprint)
	}
	for component := range before.ComponentVersions {
		if strings.TrimSpace(after.ComponentVersions[component]) == "" {
			reasons = append(reasons, "managed component receipt lost: "+component)
		}
	}
	sort.Strings(reasons)
	return CompatibilityResult{Passed: len(reasons) == 0, Reasons: uniqueSorted(reasons)}
}

// RunLegacyUpgradeGate compares an explicit before/after manifest pair. When
// only the fixture is supplied, the after manifest is captured read-only and
// the baseline is read from .baron/compatibility-before.json.
func RunLegacyUpgradeGate(ctx context.Context, fixture CompatibilityFixture) CompatibilityResult {
	beforePath := strings.TrimSpace(fixture.BeforeManifestPath)
	if beforePath == "" {
		root, err := compatibilityRoot(fixture.Root)
		if err != nil {
			return CompatibilityResult{Reasons: []string{err.Error()}}
		}
		beforePath = filepath.Join(root, ".baron", "compatibility-before.json")
	}
	before, err := readCompatibilityManifest(beforePath)
	if err != nil {
		return CompatibilityResult{Reasons: []string{"compatibility baseline unavailable: " + safeCompatibilityError(err)}}
	}
	afterPath := strings.TrimSpace(fixture.AfterManifestPath)
	var after CompatibilityManifest
	if afterPath != "" {
		after, err = readCompatibilityManifest(afterPath)
	} else {
		after, err = CaptureCompatibilityManifest(ctx, fixture)
	}
	if err != nil {
		return CompatibilityResult{Reasons: []string{"compatibility post-upgrade state unavailable: " + safeCompatibilityError(err)}}
	}
	return CompareCompatibilityManifest(before, after)
}

type sqliteCompatibilityEvidence struct {
	schema        string
	rowIDs        []string
	counts        map[string]int
	tencentIDs    []string
	tencentStates map[string]string
}

func captureSQLiteEvidence(ctx context.Context, path string) (sqliteCompatibilityEvidence, error) {
	if err := rejectCompatibilitySymlink(path); err != nil {
		return sqliteCompatibilityEvidence{}, err
	}
	dsn := path + "?mode=ro&_pragma=busy_timeout%3d1000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return sqliteCompatibilityEvidence{}, fmt.Errorf("open SQLite read-only: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return sqliteCompatibilityEvidence{}, fmt.Errorf("read SQLite read-only: %w", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT name, COALESCE(sql, '') FROM sqlite_master WHERE type IN ('table', 'index') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return sqliteCompatibilityEvidence{}, fmt.Errorf("read SQLite schema: %w", err)
	}
	var schema strings.Builder
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			_ = rows.Close()
			return sqliteCompatibilityEvidence{}, err
		}
		schema.WriteString(name)
		schema.WriteByte('\x00')
		schema.WriteString(definition)
		schema.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return sqliteCompatibilityEvidence{}, err
	}
	_ = rows.Close()
	version := 0
	_ = db.QueryRowContext(ctx, `SELECT schema_version FROM schema_meta LIMIT 1`).Scan(&version)
	schemaHash := hashText(schema.String())
	evidence := sqliteCompatibilityEvidence{
		schema:        fmt.Sprintf("schema-%d-%s", version, schemaHash[:12]),
		counts:        map[string]int{},
		tencentStates: map[string]string{},
	}
	for _, table := range compatibilityTables {
		count, countErr := sqliteCount(ctx, db, table.name)
		if errors.Is(countErr, errSQLiteTableMissing) {
			continue
		}
		if countErr != nil {
			return sqliteCompatibilityEvidence{}, countErr
		}
		evidence.counts[table.name] = count
		ids, idsErr := sqliteIDs(ctx, db, table.name, table.ids)
		if errors.Is(idsErr, errSQLiteTableMissing) {
			continue
		}
		if idsErr != nil {
			return sqliteCompatibilityEvidence{}, idsErr
		}
		for _, id := range ids {
			evidence.rowIDs = append(evidence.rowIDs, table.name+"/"+id)
		}
		if table.name == "knowledge_registry" {
			if err := sqliteKnowledgeEvidence(ctx, db, &evidence); err != nil {
				return sqliteCompatibilityEvidence{}, err
			}
		}
	}
	return evidence, nil
}

var errSQLiteTableMissing = errors.New("SQLite table missing")

func sqliteCount(ctx context.Context, db *sql.DB, table string) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteSQLiteIdentifier(table)).Scan(&count); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return 0, errSQLiteTableMissing
		}
		return 0, fmt.Errorf("count SQLite table %s: %w", table, err)
	}
	return count, nil
}

func sqliteIDs(ctx context.Context, db *sql.DB, table, expression string) ([]string, error) {
	query := `SELECT ` + expression + ` FROM ` + quoteSQLiteIdentifier(table) + ` ORDER BY 1 LIMIT ` + strconv.Itoa(compatibilityMaxRows)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return nil, errSQLiteTableMissing
		}
		return nil, fmt.Errorf("read SQLite IDs for %s: %w", table, err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id sql.NullString
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan SQLite ID for %s: %w", table, err)
		}
		if id.Valid && strings.TrimSpace(id.String) != "" {
			ids = append(ids, boundedCompatibilityText(config.Redact(id.String, nil)))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func sqliteKnowledgeEvidence(ctx context.Context, db *sql.DB, evidence *sqliteCompatibilityEvidence) error {
	rows, err := db.QueryContext(ctx, `SELECT project_id, wiki_id, code_graph_id, wiki_status, code_graph_status, wiki_ingest_status, code_graph_sync_status FROM knowledge_registry LIMIT `+strconv.Itoa(compatibilityMaxRows))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return nil
		}
		return fmt.Errorf("read Tencent knowledge state: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var projectID, wikiID, graphID, wikiStatus, graphStatus, ingestStatus, graphSyncStatus string
		if err := rows.Scan(&projectID, &wikiID, &graphID, &wikiStatus, &graphStatus, &ingestStatus, &graphSyncStatus); err != nil {
			return err
		}
		if wikiID != "" {
			evidence.tencentIDs = append(evidence.tencentIDs, "wiki:"+boundedCompatibilityText(wikiID))
		}
		if graphID != "" {
			evidence.tencentIDs = append(evidence.tencentIDs, "code_graph:"+boundedCompatibilityText(graphID))
		}
		prefix := "project:" + boundedCompatibilityText(projectID) + ":"
		for key, value := range map[string]string{
			"wiki_status":            wikiStatus,
			"code_graph_status":      graphStatus,
			"wiki_ingest_status":     ingestStatus,
			"code_graph_sync_status": graphSyncStatus,
		} {
			if strings.TrimSpace(value) != "" {
				evidence.tencentStates[prefix+key] = boundedCompatibilityText(value)
			}
		}
	}
	return rows.Err()
}

func addGlobalTencentEvidence(manifest *CompatibilityManifest, global config.GlobalState) {
	for projectID, binding := range global.ProjectBindings {
		if strings.TrimSpace(projectID) == "" {
			continue
		}
		manifest.TencentIDs = append(manifest.TencentIDs, "project:"+boundedCompatibilityText(projectID))
		for kind, value := range map[string]string{
			"team":  binding.TeamID,
			"agent": binding.AgentID,
			"user":  binding.UserID,
		} {
			if strings.TrimSpace(value) != "" {
				manifest.TencentIDs = append(manifest.TencentIDs, kind+":"+boundedCompatibilityText(value))
			}
		}
	}
}

func credentialFingerprints(global config.GlobalState) []string {
	paths := []struct {
		label string
		path  string
	}{
		{label: "dsh-config", path: global.DSHConfigPath},
		{label: "dsh-home", path: filepath.Join(global.DSHHomePath, ".credentials.yaml")},
		{label: "codex-hooks", path: global.CodexHooksPath},
		{label: "managed-strix", path: filepath.Join(managedCredentialRoot(global), "strix.env")},
	}
	result := make([]string, 0, len(paths))
	for _, item := range paths {
		fingerprint, err := credentialFingerprint(item.label, item.path)
		if err == nil && fingerprint != "" {
			result = append(result, fingerprint)
		}
	}
	return result
}

func credentialFingerprint(label, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	data, err := readBoundedFile(path)
	if err != nil {
		return "", err
	}
	keys := make([]string, 0)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key := line
		if index := strings.IndexAny(key, "=:"); index >= 0 {
			key = key[:index]
		}
		key = strings.ToUpper(strings.TrimSpace(key))
		if key == "" || len(key) > 128 {
			continue
		}
		if strings.Contains(key, "KEY") || strings.Contains(key, "TOKEN") || strings.Contains(key, "SECRET") || strings.Contains(key, "PASSWORD") || strings.Contains(key, "CREDENTIAL") {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return label + ":keys=0", nil
	}
	keys = uniqueSorted(keys)
	return fmt.Sprintf("%s:keys=%d:shape=%s", label, len(keys), hashText(strings.Join(keys, "\n"))[:12]), nil
}

func managedCredentialRoot(global config.GlobalState) string {
	if global.ManagedRuntime == nil {
		return ""
	}
	return filepath.Join(global.ManagedRuntime.Root, "credentials")
}

func managedComponentVersions(global config.GlobalState) (map[string]string, error) {
	result := map[string]string{}
	if global.ManagedRuntime == nil {
		return result, nil
	}
	for _, receiptPath := range global.ManagedRuntime.Receipts {
		data, err := readBoundedFile(receiptPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read managed runtime receipt: %w", err)
		}
		var receipt struct {
			Component string `json:"component"`
			Version   string `json:"version"`
		}
		if err := json.Unmarshal(data, &receipt); err != nil {
			return nil, fmt.Errorf("decode managed runtime receipt: %w", err)
		}
		if strings.TrimSpace(receipt.Component) != "" && strings.TrimSpace(receipt.Version) != "" {
			result[boundedCompatibilityText(receipt.Component)] = boundedCompatibilityText(receipt.Version)
		}
	}
	return result, nil
}

func redactedOwnedSurfaceHash(path string, codexHooks bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	data, err := readBoundedFile(path)
	if err != nil {
		return "", err
	}
	if codexHooks {
		var value any
		if json.Unmarshal(data, &value) == nil {
			value = stripBaronSurface(value)
			data, _ = json.Marshal(value)
		}
	} else {
		lines := make([]string, 0)
		for _, line := range strings.Split(string(data), "\n") {
			lowered := strings.ToLower(line)
			if strings.Contains(lowered, "baron-owned") || strings.Contains(lowered, "baron-dsh-adapter") {
				continue
			}
			lines = append(lines, line)
		}
		data = []byte(strings.Join(lines, "\n"))
	}
	return hashText(config.Redact(string(data), nil)), nil
}

func stripBaronSurface(value any) any {
	switch item := value.(type) {
	case []any:
		result := make([]any, 0, len(item))
		for _, child := range item {
			if entry, ok := child.(map[string]any); ok {
				if command, ok := entry["command"].(string); ok && strings.Contains(strings.ToLower(command), "baron hook codex") {
					continue
				}
			}
			result = append(result, stripBaronSurface(child))
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(item))
		for key, child := range item {
			result[key] = stripBaronSurface(child)
		}
		return result
	default:
		return value
	}
}

func countBaronHooks(path string) (int, error) {
	if strings.TrimSpace(path) == "" {
		return 0, nil
	}
	data, err := readBoundedFile(path)
	if err != nil {
		return 0, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return 0, fmt.Errorf("decode hooks JSON: %w", err)
	}
	return countBaronHookValues(value), nil
}

func countBaronHookValues(value any) int {
	switch item := value.(type) {
	case []any:
		count := 0
		for _, child := range item {
			count += countBaronHookValues(child)
		}
		return count
	case map[string]any:
		count := 0
		if command, ok := item["command"].(string); ok && strings.Contains(strings.ToLower(command), "baron hook codex") {
			count++
		}
		for _, child := range item {
			count += countBaronHookValues(child)
		}
		return count
	default:
		return 0
	}
}

func compatibilityGit(ctx context.Context, root string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return boundedCompatibilityText(string(output)), nil
}

func readCompatibilityManifest(path string) (CompatibilityManifest, error) {
	data, err := readBoundedFile(path)
	if err != nil {
		return CompatibilityManifest{}, err
	}
	var manifest CompatibilityManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return CompatibilityManifest{}, fmt.Errorf("decode compatibility manifest: %w", err)
	}
	return manifest, nil
}

func compatibilityRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("compatibility project root is required")
	}
	root, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("compatibility project root is not a directory")
	}
	return filepath.Clean(root), nil
}

func rejectCompatibilitySymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("compatibility state path is a symlink")
	}
	return nil
}

func readBoundedFile(path string) ([]byte, error) {
	if err := rejectCompatibilitySymlink(path); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, compatibilityMaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > compatibilityMaxFileBytes {
		return nil, errors.New("compatibility file exceeds the safety limit")
	}
	return data, nil
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func schemaVersion(value string) int {
	value = strings.TrimSpace(value)
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			continue
		}
		end := index
		for end < len(value) && value[end] >= '0' && value[end] <= '9' {
			end++
		}
		parsed, err := strconv.Atoi(value[index:end])
		if err == nil {
			return parsed
		}
		index = end
	}
	return 0
}

func missingValues(before, after []string) []string {
	set := make(map[string]struct{}, len(after))
	for _, value := range after {
		set[value] = struct{}{}
	}
	missing := make([]string, 0)
	for _, value := range before {
		if _, ok := set[value]; !ok {
			missing = append(missing, value)
		}
	}
	return uniqueSorted(missing)
}

func uniqueSorted(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	if len(result) == 0 {
		return result
	}
	write := 1
	for _, value := range result[1:] {
		if value == result[write-1] {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}

func boundedCompatibilityText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		return value[:512]
	}
	return value
}

func safeCompatibilityError(err error) string {
	if err == nil {
		return ""
	}
	return config.Redact(err.Error(), nil)
}
