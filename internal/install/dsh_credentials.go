package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	dshProviderKeyEnv   = "DEEPSEEK_API_KEY"
	dshHomeEnv          = "DSH_HOME"
	dshCredentialFile   = ".credentials.yaml"
	dshCredentialMode   = 0o600
	dshCredentialFormat = 1
)

// DSHCredentialPath resolves the official DSH credential file without
// creating it. The values argument is normally processEnvironment(); keeping
// it injectable makes path and precedence behavior deterministic in tests.
func DSHCredentialPath(values map[string]string) (string, error) {
	home, err := DSHHome(values)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, dshCredentialFile), nil
}

// DSHHome resolves the official DSH user directory without creating it.
func DSHHome(values map[string]string) (string, error) {
	home := strings.TrimSpace(values[dshHomeEnv])
	if home != "" {
		return filepath.Clean(home), nil
	}
	home = strings.TrimSpace(values["HOME"])
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", errors.New("resolve the DSH home directory")
		}
	}
	return filepath.Join(home, ".dsh"), nil
}

// ReadDSHProviderKey reads the provider key from the launching environment or
// the official DSH credentials file. It never logs or returns the key as part
// of an error. An absent credential is represented by an empty string.
func ReadDSHProviderKey(values map[string]string) (string, error) {
	if key := strings.TrimSpace(values[dshProviderKeyEnv]); key != "" {
		return key, nil
	}
	path, err := DSHCredentialPath(values)
	if err != nil {
		return "", err
	}
	document, exists, err := readDSHCredentialDocument(path)
	if err != nil || !exists {
		return "", err
	}
	return dshProviderKeyFromDocument(document)
}

// EnsureDSHProviderKey updates only refs.DEEPSEEK_API_KEY in the official DSH
// credential document. Unknown refs, mapping order, and YAML comments are
// retained through yaml.Node; the write is atomic and the resulting file is
// owner-readable only.
func EnsureDSHProviderKey(values map[string]string, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("DeepSeek API key is required")
	}
	if strings.ContainsAny(key, "\r\n") {
		return errors.New("DeepSeek API key contains an invalid newline")
	}
	path, err := DSHCredentialPath(values)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("refusing to edit DSH credentials through a symlink or non-regular file")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect DSH credentials: %w", statErr)
	}

	document, exists, err := readDSHCredentialDocument(path)
	if err != nil {
		return err
	}
	if !exists {
		document = newDSHCredentialDocument()
	}
	if err := ensureDSHCredentialVersion(&document); err != nil {
		return err
	}
	refs, err := ensureDSHRefsMapping(&document)
	if err != nil {
		return err
	}
	setYAMLMappingValue(refs, dshProviderKeyEnv, yamlStringNode(key))

	data, err := yaml.Marshal(&document)
	if err != nil {
		return fmt.Errorf("encode DSH credentials: %w", err)
	}
	if err := config.AtomicWriteFile(path, data, dshCredentialMode); err != nil {
		return fmt.Errorf("write DSH credentials: %w", err)
	}
	return nil
}

// RemoveDSHProviderKey removes only the DeepSeek provider key from the
// official credentials file. An absent key is a successful no-op.
func RemoveDSHProviderKey(values map[string]string) (bool, error) {
	path, err := DSHCredentialPath(values)
	if err != nil {
		return false, err
	}
	return RemoveDSHProviderKeyAt(path)
}

func RemoveDSHProviderKeyAt(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, errors.New("DSH credential path is required")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, errors.New("refusing to edit DSH credentials through a symlink or non-regular file")
	}
	document, exists, err := readDSHCredentialDocument(path)
	if err != nil || !exists {
		return false, err
	}
	root := document.Content[0]
	refs, ok := yamlMappingNode(root, "refs")
	if !ok || refs.Kind != yaml.MappingNode {
		return false, nil
	}
	removed := false
	content := refs.Content[:0]
	for index := 0; index+1 < len(refs.Content); index += 2 {
		if refs.Content[index].Value == dshProviderKeyEnv {
			removed = true
			continue
		}
		content = append(content, refs.Content[index], refs.Content[index+1])
	}
	if !removed {
		return false, nil
	}
	refs.Content = content
	if len(refs.Content) == 0 && dshCredentialRootOnly(root) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		return true, nil
	}
	data, err := yaml.Marshal(&document)
	if err != nil {
		return false, fmt.Errorf("encode DSH credentials: %w", err)
	}
	if err := config.AtomicWriteFile(path, data, dshCredentialMode); err != nil {
		return false, fmt.Errorf("write DSH credentials: %w", err)
	}
	return true, nil
}

func dshCredentialRootOnly(root *yaml.Node) bool {
	if root == nil || root.Kind != yaml.MappingNode {
		return false
	}
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value != "version" && root.Content[index].Value != "refs" {
			return false
		}
	}
	return true
}

func readDSHCredentialDocument(path string) (yaml.Node, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return yaml.Node{}, false, nil
	}
	if err != nil {
		return yaml.Node{}, false, fmt.Errorf("read DSH credentials: %w", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return yaml.Node{}, true, errors.New("decode DSH credentials YAML")
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return yaml.Node{}, true, errors.New("DSH credentials must be a YAML mapping")
	}
	return document, true, nil
}

func dshProviderKeyFromDocument(document yaml.Node) (string, error) {
	root := document.Content[0]
	version, ok := yamlMappingValue(root, "version")
	if !ok || strings.TrimSpace(version.Value) != "1" {
		return "", errors.New("unsupported DSH credentials version")
	}
	refs, ok := yamlMappingNode(root, "refs")
	if !ok || refs.Kind != yaml.MappingNode {
		return "", errors.New("DSH credentials refs mapping is missing")
	}
	key, ok := yamlMappingValue(refs, dshProviderKeyEnv)
	if !ok {
		return "", nil
	}
	return strings.TrimSpace(key.Value), nil
}

func newDSHCredentialDocument() yaml.Node {
	root := &yaml.Node{Kind: yaml.MappingNode}
	root.Content = append(root.Content,
		yamlStringNode("version"), yamlIntNode(dshCredentialFormat),
		yamlStringNode("refs"), &yaml.Node{Kind: yaml.MappingNode},
	)
	return yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
}

func ensureDSHCredentialVersion(document *yaml.Node) error {
	root := document.Content[0]
	if version, ok := yamlMappingValue(root, "version"); ok {
		if strings.TrimSpace(version.Value) != "1" {
			return errors.New("unsupported DSH credentials version")
		}
		return nil
	}
	setYAMLMappingValue(root, "version", yamlIntNode(dshCredentialFormat))
	return nil
}

func ensureDSHRefsMapping(document *yaml.Node) (*yaml.Node, error) {
	root := document.Content[0]
	if refs, ok := yamlMappingNode(root, "refs"); ok {
		if refs.Kind != yaml.MappingNode {
			return nil, errors.New("DSH credentials refs must be a YAML mapping")
		}
		return refs, nil
	}
	refs := &yaml.Node{Kind: yaml.MappingNode}
	setYAMLMappingValue(root, "refs", refs)
	return refs, nil
}

func yamlMappingValue(mapping *yaml.Node, key string) (*yaml.Node, bool) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, false
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1], true
		}
	}
	return nil, false
}

func yamlMappingNode(mapping *yaml.Node, key string) (*yaml.Node, bool) {
	value, ok := yamlMappingValue(mapping, key)
	return value, ok
}

func setYAMLMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content[index+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, yamlStringNode(key), value)
}

func yamlStringNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle}
}

func yamlIntNode(value int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", value)}
}
