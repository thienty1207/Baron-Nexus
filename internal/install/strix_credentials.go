package install

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
	"gopkg.in/yaml.v3"
)

// SecretBridge is the narrow boundary used to pass a validated provider key
// to another protected runtime, such as a WSL2 deployment. Implementations
// must not put the value in argv or log output.
type SecretBridge interface {
	Write(context.Context, []byte) error
}

type CredentialFanout struct {
	DSHPath        string
	TencentEnvPath string
	StrixEnvPath   string
	StrixProvider  ProviderConfig
	WSLBridge      SecretBridge
}

type CredentialGeneration struct {
	ID    string
	Paths []string

	targets []credentialTarget
}

type credentialTarget struct {
	target string
	stage  string
}

type credentialBackup struct {
	target string
	data   []byte
	mode   os.FileMode
	exists bool
}

// ProviderConfig is the non-secret provider configuration used when probing
// the managed Strix runtime. The API key is intentionally not part of it.
type ProviderConfig struct {
	Name    string
	BaseURL string
	Model   string
}

// RotateDeepSeek commits one validated key to every configured Baron-owned
// provider store. All local targets are staged first and restored if a later
// target or bridge fails; Codex auth is never an implicit target.
func RotateDeepSeek(ctx context.Context, fanout CredentialFanout, key string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	generation, err := fanout.Stage(ctx, key)
	if err != nil {
		return err
	}
	defer removeCredentialStages(generation.Paths)

	backups := make([]credentialBackup, 0, len(generation.targets))
	for _, target := range generation.targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		backup, backupErr := readCredentialBackup(target.target)
		if backupErr != nil {
			return backupErr
		}
		backups = append(backups, backup)
	}
	committed := 0
	for _, target := range generation.targets {
		if err := ctx.Err(); err != nil {
			return rollbackCredentialGeneration(backups, committed, err)
		}
		data, readErr := os.ReadFile(target.stage)
		if readErr != nil {
			return rollbackCredentialGeneration(backups, committed, fmt.Errorf("read staged credential generation: %w", readErr))
		}
		if writeErr := config.AtomicWriteFile(target.target, data, 0o600); writeErr != nil {
			return rollbackCredentialGeneration(backups, committed, fmt.Errorf("commit credential generation: %w", writeErr))
		}
		committed++
	}
	if fanout.WSLBridge != nil {
		if err := fanout.WSLBridge.Write(ctx, []byte(strings.TrimSpace(key))); err != nil {
			return rollbackCredentialGeneration(backups, committed, fmt.Errorf("write WSL credential bridge: %w", err))
		}
	}
	return nil
}

// Stage creates a protected same-directory generation for all configured
// targets. It never changes the live credential files.
func (b CredentialFanout) Stage(ctx context.Context, key string) (CredentialGeneration, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return CredentialGeneration{}, errors.New("DeepSeek API key is required")
	}
	if strings.ContainsAny(key, "\r\n") {
		return CredentialGeneration{}, errors.New("DeepSeek API key contains an invalid newline")
	}
	targets := b.targets()
	if len(targets) == 0 {
		return CredentialGeneration{}, errors.New("no Baron credential targets are configured")
	}
	id := newCredentialGenerationID()
	generation := CredentialGeneration{ID: id, targets: make([]credentialTarget, 0, len(targets))}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			removeCredentialStages(generation.Paths)
			return CredentialGeneration{}, err
		}
		if err := validateCredentialTarget(target); err != nil {
			removeCredentialStages(generation.Paths)
			return CredentialGeneration{}, err
		}
		data, err := credentialData(target, key, b.StrixProvider)
		if err != nil {
			removeCredentialStages(generation.Paths)
			return CredentialGeneration{}, err
		}
		stagePath := target + ".baron-credential-" + id + ".tmp"
		if err := validateCredentialTarget(stagePath); err != nil {
			removeCredentialStages(generation.Paths)
			return CredentialGeneration{}, err
		}
		if err := config.AtomicWriteFile(stagePath, data, 0o600); err != nil {
			removeCredentialStages(generation.Paths)
			return CredentialGeneration{}, fmt.Errorf("stage credential generation: %w", err)
		}
		generation.Paths = append(generation.Paths, stagePath)
		generation.targets = append(generation.targets, credentialTarget{target: target, stage: stagePath})
	}
	return generation, nil
}

// ValidateStrixProvider validates only the non-secret runtime/provider
// contract. The key itself remains in the protected Strix environment.
func ValidateStrixProvider(ctx context.Context, runtime StrixRuntime, provider ProviderConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(runtime.StrixPath) == "" || strings.TrimSpace(runtime.EnvironmentPath) == "" {
		return errors.New("managed Strix provider runtime is not configured")
	}
	if strings.TrimSpace(provider.Name) == "" || strings.TrimSpace(provider.Model) == "" {
		return errors.New("Strix provider name and model are required")
	}
	parsed, err := url.Parse(strings.TrimSpace(provider.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("Strix provider base URL is invalid")
	}
	if runtime.Runner != nil {
		if err := ProbeStrixRuntime(ctx, runtime); err != nil {
			return err
		}
	}
	return nil
}

func (b CredentialFanout) targets() []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, 3)
	for _, raw := range []string{b.DSHPath, b.TencentEnvPath, b.StrixEnvPath} {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		path, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	return result
}

func credentialData(target, key string, provider ProviderConfig) ([]byte, error) {
	switch {
	case strings.HasSuffix(filepath.ToSlash(target), "/.credentials.yaml"):
		return renderDSHCredential(target, key)
	case strings.EqualFold(filepath.Base(target), "strix.env"):
		return renderStrixCredential(target, key, provider)
	default:
		return renderTencentCredential(target, key)
	}
}

func renderDSHCredential(path, key string) ([]byte, error) {
	document, exists, err := readDSHCredentialDocument(path)
	if err != nil {
		return nil, err
	}
	if !exists {
		document = newDSHCredentialDocument()
	}
	if err := ensureDSHCredentialVersion(&document); err != nil {
		return nil, err
	}
	refs, err := ensureDSHRefsMapping(&document)
	if err != nil {
		return nil, err
	}
	setYAMLMappingValue(refs, dshProviderKeyEnv, yamlStringNode(key))
	data, err := yaml.Marshal(&document)
	if err != nil {
		return nil, fmt.Errorf("encode DSH credentials: %w", err)
	}
	return data, nil
}

func renderTencentCredential(path, key string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data = nil
	} else if err != nil {
		return nil, fmt.Errorf("read Tencent credentials: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, name := range []string{"MEMORY_LLM_API_KEY", "PROXY_UPSTREAM_API_KEY"} {
		lines = setSimpleEnv(lines, name, key)
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

const (
	defaultStrixDeepSeekModel = "deepseek/deepseek-chat"
	defaultStrixDeepSeekBase  = "https://api.deepseek.com/v1"
)

func renderStrixCredential(path, key string, provider ProviderConfig) ([]byte, error) {
	values := map[string]string{}
	if existing, err := config.ReadEnvFile(path); err == nil {
		values = existing
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read Strix credentials: %w", err)
	}
	values["STRIX_PROVIDER"] = firstNonEmpty(provider.Name, values["STRIX_PROVIDER"], "deepseek")
	model := firstNonEmpty(provider.Model, values["STRIX_LLM"], values["DEEPSEEK_MODEL"], defaultStrixDeepSeekModel)
	if !strings.Contains(model, "/") && strings.EqualFold(values["STRIX_PROVIDER"], "deepseek") {
		model = "deepseek/" + model
	}
	base := firstNonEmpty(provider.BaseURL, values["LLM_API_BASE"], values["OPENAI_API_BASE"], values["OPENAI_BASE_URL"], values["DEEPSEEK_BASE_URL"], defaultStrixDeepSeekBase)
	values["STRIX_LLM"] = model
	values["LLM_API_BASE"] = base
	// Strix's supported provider boundary is LLM_API_KEY. Keep the legacy
	// DeepSeek name in the protected file for compatibility with older helpers,
	// but only LLM_API_KEY is injected into the child process.
	values["LLM_API_KEY"] = key
	values["DEEPSEEK_API_KEY"] = key
	tempPath := path + ".render"
	defer os.Remove(tempPath)
	if err := config.WriteEnv(tempPath, values); err != nil {
		return nil, fmt.Errorf("encode Strix credentials: %w", err)
	}
	data, err := os.ReadFile(tempPath)
	if err != nil {
		return nil, fmt.Errorf("read encoded Strix credentials: %w", err)
	}
	return data, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func validateCredentialTarget(path string) error {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) == filepath.VolumeName(path)+string(os.PathSeparator) {
		return errors.New("credential target must be an absolute non-root path")
	}
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("credential target is not a safe regular file: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect credential target: %w", err)
	}
	parent := filepath.Dir(path)
	for {
		info, err := os.Lstat(parent)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("credential target parent is unsafe: %s", parent)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect credential target parent: %w", err)
		}
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}
	return nil
}

func readCredentialBackup(path string) (credentialBackup, error) {
	backup := credentialBackup{target: path, mode: 0o600}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return backup, nil
	}
	if err != nil {
		return backup, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return backup, errors.New("refusing to back up an unsafe credential target")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return backup, err
	}
	backup.data, backup.mode, backup.exists = data, info.Mode().Perm(), true
	return backup, nil
}

func rollbackCredentialGeneration(backups []credentialBackup, committed int, cause error) error {
	var rollbackErr error
	if committed > len(backups) {
		committed = len(backups)
	}
	for index := committed - 1; index >= 0; index-- {
		backup := backups[index]
		if err := validateCredentialTarget(backup.target); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
			continue
		}
		if !backup.exists {
			if err := os.Remove(backup.target); err != nil && !errors.Is(err, os.ErrNotExist) {
				rollbackErr = errors.Join(rollbackErr, err)
			}
			continue
		}
		if err := config.AtomicWriteFile(backup.target, backup.data, backup.mode); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	if rollbackErr != nil {
		return fmt.Errorf("%w; credential rollback failed: %v", cause, rollbackErr)
	}
	return fmt.Errorf("%w; credential generation rolled back", cause)
}

func removeCredentialStages(paths []string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

func newCredentialGenerationID() string {
	var random [6]byte
	_, _ = rand.Read(random[:])
	return fmt.Sprintf("%d-%s", time.Now().UTC().UnixNano(), hex.EncodeToString(random[:]))
}
