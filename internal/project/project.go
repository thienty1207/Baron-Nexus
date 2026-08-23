package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/contracts"
	"github.com/baron-shared-brain/baron/internal/storage"
	"github.com/pelletier/go-toml/v2"
)

const (
	baronDirName   = ".baron"
	projectToml    = "project.toml"
	projectEnv     = ".env"
	checkpointName = "checkpoint.json"
)

var projectIDPattern = regexp.MustCompile(`^prj[-_][A-Za-z0-9][A-Za-z0-9_-]{7,}$`)

type Metadata struct {
	Version   int    `toml:"version"`
	ProjectID string `toml:"project_id"`
	Name      string `toml:"name"`
	CreatedAt string `toml:"created_at"`
}

type Project struct {
	Root      string
	Metadata  Metadata
	ProjectID string
	EnvPath   string
	Binding   contracts.ProjectBinding
	Identity  contracts.Identity
}

type SetupOptions struct {
	Identity  contracts.Identity
	Binding   contracts.ProjectBinding
	Provision func(context.Context, string, string) (contracts.ProjectBinding, error)
	Now       func() time.Time
	Random    io.Reader
}

func Resolve(path string) (Project, error) {
	root, err := resolveRoot(path)
	if err != nil {
		return Project{}, err
	}
	baronDir := filepath.Join(root, baronDirName)
	metadataPath := filepath.Join(baronDir, projectToml)
	envPath := filepath.Join(baronDir, projectEnv)
	if err := rejectOwnedSymlinks(baronDir, metadataPath, envPath); err != nil {
		return Project{}, err
	}
	metadata, err := readMetadata(metadataPath)
	if err != nil {
		return Project{}, err
	}
	env, err := config.ReadEnvFile(envPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Project{}, fmt.Errorf("read project environment: %w", err)
	}
	binding := contracts.ProjectBinding{
		ProjectID: metadata.ProjectID,
		TeamID:    env["BARON_TENCENT_TEAM_ID"],
		AgentID:   env["BARON_TENCENT_AGENT_ID"],
		UserID:    env["BARON_TENCENT_USER_ID"],
	}
	identity := contracts.Identity{
		UserID:      env["BARON_TENCENT_USER_ID"],
		UserKey:     env["BARON_TENCENT_USER_KEY"],
		TeamID:      env["BARON_TENCENT_TEAM_ID"],
		Endpoint:    env["BARON_TENCENT_ENDPOINT"],
		HubEndpoint: env["BARON_TENCENT_HUB_ENDPOINT"],
		ServiceID:   env["BARON_TENCENT_SERVICE_ID"],
	}
	return Project{Root: root, Metadata: metadata, ProjectID: metadata.ProjectID, EnvPath: envPath, Binding: binding, Identity: identity}, nil
}

func Setup(ctx context.Context, path string, options SetupOptions) (Project, error) {
	root, err := resolveRoot(path)
	if err != nil {
		return Project{}, err
	}
	baronDir := filepath.Join(root, baronDirName)
	if err := rejectOwnedSymlinks(baronDir); err != nil {
		return Project{}, err
	}
	for _, dir := range []string{baronDir, filepath.Join(baronDir, "runtime"), filepath.Join(baronDir, "runtime", "logs"), filepath.Join(baronDir, "runtime", "locks")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Project{}, fmt.Errorf("create Baron directory %s: %w", dir, err)
		}
		_ = os.Chmod(dir, 0o700)
	}

	metadataPath := filepath.Join(baronDir, projectToml)
	envPath := filepath.Join(baronDir, projectEnv)
	if err := rejectOwnedSymlinks(metadataPath, envPath); err != nil {
		return Project{}, err
	}
	metadata, err := readMetadata(metadataPath)
	if errors.Is(err, os.ErrNotExist) {
		now := time.Now().UTC()
		if options.Now != nil {
			now = options.Now().UTC()
		}
		projectID, generateErr := generateProjectID(options.Random)
		if generateErr != nil {
			return Project{}, generateErr
		}
		metadata = Metadata{Version: 1, ProjectID: projectID, Name: displayName(root), CreatedAt: now.Format(time.RFC3339Nano)}
		if err := writeMetadata(metadataPath, metadata); err != nil {
			return Project{}, err
		}
	} else if err != nil {
		return Project{}, err
	}
	if err := metadata.Validate(); err != nil {
		return Project{}, err
	}

	binding := options.Binding
	if options.Provision != nil {
		binding, err = options.Provision(ctx, metadata.ProjectID, metadata.Name)
		if err != nil {
			return Project{}, err
		}
		if strings.TrimSpace(binding.TeamID) == "" || strings.TrimSpace(binding.AgentID) == "" || strings.TrimSpace(binding.UserID) == "" {
			return Project{}, errors.New("Tencent project provisioning returned an incomplete team/agent/user binding")
		}
	}
	binding.ProjectID = metadata.ProjectID
	env := map[string]string{}
	if existing, readErr := config.ReadEnvFile(envPath); readErr == nil {
		env = existing
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return Project{}, fmt.Errorf("read existing project environment: %w", readErr)
	}
	applyIdentity(env, options.Identity)
	applyBinding(env, binding)
	env["BARON_PROJECT_ID"] = metadata.ProjectID
	env["BARON_PROJECT_NAME"] = metadata.Name
	if err := config.WriteEnv(envPath, env); err != nil {
		return Project{}, fmt.Errorf("write project environment: %w", err)
	}
	if err := mergeGitignore(root); err != nil {
		return Project{}, err
	}
	stateStore, err := storage.Open(filepath.Join(baronDir, "runtime", "state.db"))
	if err != nil {
		return Project{}, fmt.Errorf("initialize local Baron state: %w", err)
	}
	if err := stateStore.RegisterProject(ctx, storage.ProjectRecord{ProjectID: metadata.ProjectID, Root: root, Name: metadata.Name}); err != nil {
		_ = stateStore.Close()
		return Project{}, err
	}
	if err := stateStore.Close(); err != nil {
		return Project{}, fmt.Errorf("close local Baron state: %w", err)
	}

	resolvedBinding := contracts.ProjectBinding{
		ProjectID: metadata.ProjectID,
		TeamID:    env["BARON_TENCENT_TEAM_ID"],
		AgentID:   env["BARON_TENCENT_AGENT_ID"],
		UserID:    env["BARON_TENCENT_USER_ID"],
	}
	return Project{
		Root: root, Metadata: metadata, ProjectID: metadata.ProjectID,
		EnvPath: envPath, Binding: resolvedBinding, Identity: identityFromEnv(env),
	}, nil
}

func (p Project) IsolationContext() contracts.IsolationContext {
	return contracts.IsolationContext{
		ProjectID: p.ProjectID,
		TeamID:    p.Binding.TeamID,
		AgentID:   p.Binding.AgentID,
		UserID:    p.Binding.UserID,
		ServiceID: p.Identity.ServiceID,
	}
}

func ValidateBinding(p Project, expected contracts.ProjectBinding) error {
	if expected.ProjectID != "" && expected.ProjectID != p.ProjectID {
		return fmt.Errorf("project ID binding mismatch: env=%s expected=%s", p.ProjectID, expected.ProjectID)
	}
	if p.Binding.TeamID != expected.TeamID || p.Binding.AgentID != expected.AgentID || (expected.UserID != "" && p.Binding.UserID != expected.UserID) {
		return fmt.Errorf("project Tencent binding integrity failure for %s", p.ProjectID)
	}
	return nil
}

func (m Metadata) Validate() error {
	if m.Version != 1 {
		return fmt.Errorf("unsupported Baron project.toml version %d", m.Version)
	}
	if !projectIDPattern.MatchString(m.ProjectID) {
		return fmt.Errorf("invalid Baron project_id %q", m.ProjectID)
	}
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.CreatedAt) == "" {
		return errors.New("project.toml is missing name or created_at")
	}
	if _, err := time.Parse(time.RFC3339Nano, m.CreatedAt); err != nil {
		return fmt.Errorf("invalid project created_at: %w", err)
	}
	return nil
}

func resolveRoot(path string) (string, error) {
	if path == "" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project path is not a directory: %s", abs)
	}
	if err := rejectDangerousRoot(abs); err != nil {
		return "", err
	}

	for current := abs; ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(current, baronDirName, projectToml)); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	if gitRoot := gitTopLevel(abs); gitRoot != "" {
		return gitRoot, nil
	}
	return abs, nil
}

func gitTopLevel(path string) string {
	command := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel")
	out, err := command.Output()
	if err != nil {
		return ""
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved
	}
	return root
}

func rejectDangerousRoot(root string) error {
	clean := filepath.Clean(root)
	if clean == filepath.VolumeName(clean)+string(filepath.Separator) {
		return errors.New("refusing to initialize filesystem root")
	}
	if home, err := os.UserHomeDir(); err == nil && clean == filepath.Clean(home) {
		return errors.New("refusing to initialize the home directory")
	}
	if global, err := config.GlobalConfigDir("baron"); err == nil && clean == filepath.Clean(global) {
		return errors.New("refusing to initialize Baron global config directory")
	}
	return nil
}

func readMetadata(path string) (Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, err
	}
	var metadata Metadata
	if err := toml.Unmarshal(data, &metadata); err != nil {
		return Metadata{}, fmt.Errorf("decode project.toml: %w", err)
	}
	if err := metadata.Validate(); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func writeMetadata(path string, metadata Metadata) error {
	data, err := toml.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode project.toml: %w", err)
	}
	return config.AtomicWriteFile(path, data, 0o644)
}

func generateProjectID(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	bytes := make([]byte, 16)
	if _, err := io.ReadFull(random, bytes); err != nil {
		return "", fmt.Errorf("generate project identity: %w", err)
	}
	return "prj-" + hex.EncodeToString(bytes), nil
}

func displayName(root string) string {
	name := filepath.Base(root)
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '/' || r == '\\' {
			return '-'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." {
		return "project"
	}
	return name
}

func applyIdentity(env map[string]string, identity contracts.Identity) {
	if identity.UserKey != "" {
		env["BARON_TENCENT_USER_KEY"] = identity.UserKey
	}
	if identity.UserID != "" {
		env["BARON_TENCENT_USER_ID"] = identity.UserID
	}
	if identity.TeamID != "" {
		env["BARON_TENCENT_TEAM_ID"] = identity.TeamID
	}
	if identity.Endpoint != "" {
		env["BARON_TENCENT_ENDPOINT"] = identity.Endpoint
	}
	if identity.HubEndpoint != "" {
		env["BARON_TENCENT_HUB_ENDPOINT"] = identity.HubEndpoint
	}
	if identity.ServiceID != "" {
		env["BARON_TENCENT_SERVICE_ID"] = identity.ServiceID
	}
}

func applyBinding(env map[string]string, binding contracts.ProjectBinding) {
	if binding.TeamID != "" {
		env["BARON_TENCENT_TEAM_ID"] = binding.TeamID
	}
	if binding.AgentID != "" {
		env["BARON_TENCENT_AGENT_ID"] = binding.AgentID
	}
	if binding.UserID != "" {
		env["BARON_TENCENT_USER_ID"] = binding.UserID
	}
}

func identityFromEnv(env map[string]string) contracts.Identity {
	return contracts.Identity{
		UserID: env["BARON_TENCENT_USER_ID"], UserKey: env["BARON_TENCENT_USER_KEY"],
		TeamID: env["BARON_TENCENT_TEAM_ID"], Endpoint: env["BARON_TENCENT_ENDPOINT"],
		HubEndpoint: env["BARON_TENCENT_HUB_ENDPOINT"], ServiceID: env["BARON_TENCENT_SERVICE_ID"],
	}
}

const baronGitignore = ".baron/.env\n.baron/checkpoint.json\n.baron/runtime/\n.baron/*.bak\n.baron/*.tmp\n"

func mergeGitignore(root string) error {
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	text := string(data)
	for _, rule := range strings.Split(strings.TrimSpace(baronGitignore), "\n") {
		if !containsGitignoreRule(text, rule) {
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			text += rule + "\n"
		}
	}
	if string(data) == text {
		return nil
	}
	return config.AtomicWriteFile(path, []byte(text), 0o644)
}

func containsGitignoreRule(text, rule string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == rule {
			return true
		}
	}
	return false
}

func rejectOwnedSymlinks(paths ...string) error {
	for _, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing Baron write through symlink: %s", path)
		}
	}
	return nil
}
