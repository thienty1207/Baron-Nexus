package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/baron-shared-brain/baron/internal/config"
)

type BackupManifest struct {
	Version                   int          `json:"version"`
	CreatedAt                 time.Time    `json:"created_at"`
	SecretsExcluded           bool         `json:"secrets_excluded"`
	KnowledgeRegistryIncluded bool         `json:"knowledge_registry_included"`
	SyncQueueIncluded         bool         `json:"sync_queue_included"`
	TencentDeploymentExcluded bool         `json:"tencent_deployment_excluded"`
	Files                     []BackupFile `json:"files"`
}

type BackupFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func (a *App) Backup(ctx context.Context, destination string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	return a.BackupProject(ctx, root, destination)
}

func (a *App) BackupProject(ctx context.Context, root, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if destination == "" {
		return errors.New("backup destination is required")
	}
	stage, err := os.MkdirTemp("", "baron-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.MkdirAll(filepath.Join(stage, "project"), 0o700); err != nil {
		return err
	}
	files := make(map[string]string)
	for _, relative := range []string{".baron/project.toml", ".baron/checkpoint.json", ".baron/runtime/state.db", ".baron/runtime/state.db-wal", ".baron/runtime/state.db-shm"} {
		source := filepath.Join(root, filepath.FromSlash(relative))
		if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		destinationPath := filepath.Join(stage, "project", filepath.FromSlash(relative))
		if err := copyFile(source, destinationPath); err != nil {
			return err
		}
		files[filepath.ToSlash(filepath.Join("project", relative))] = destinationPath
	}
	global, _, err := a.loadGlobal()
	if err != nil {
		return err
	}
	global.Identity.UserKey = ""
	globalPath := filepath.Join(stage, "global.json")
	data, err := json.MarshalIndent(global, "", "  ")
	if err != nil {
		return err
	}
	if err := config.AtomicWriteFile(globalPath, append(data, '\n'), 0o600); err != nil {
		return err
	}
	files["global.json"] = globalPath
	stateDatabaseIncluded := false
	for path := range files {
		if path == "project/.baron/runtime/state.db" {
			stateDatabaseIncluded = true
			break
		}
	}
	manifest := BackupManifest{
		Version:                   2,
		CreatedAt:                 time.Now().UTC(),
		SecretsExcluded:           true,
		KnowledgeRegistryIncluded: stateDatabaseIncluded,
		SyncQueueIncluded:         stateDatabaseIncluded,
		TencentDeploymentExcluded: true,
	}
	keys := make([]string, 0, len(files))
	for path := range files {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	for _, path := range keys {
		info, err := os.Stat(files[path])
		if err != nil {
			return err
		}
		digest, err := fileHash(files[path])
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, BackupFile{Path: path, Size: info.Size(), SHA256: digest})
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(stage, "manifest.json")
	if err := config.AtomicWriteFile(manifestPath, append(manifestData, '\n'), 0o600); err != nil {
		return err
	}
	if err := writeTarGz(destination, map[string]string{"manifest.json": manifestPath, "global.json": globalPath, "project": filepath.Join(stage, "project")}); err != nil {
		return err
	}
	if err := verifyArchive(destination); err != nil {
		return fmt.Errorf("verify backup archive: %w", err)
	}
	return nil
}

func (a *App) Restore(ctx context.Context, archive string) error {
	target, err := config.GlobalConfigDir("baron")
	if err != nil {
		return err
	}
	return a.RestoreArchive(ctx, archive, target)
}

// RestoreOptions makes replacement of an existing Baron state explicit. The
// default remains conflict-safe and never mutates an existing target.
type RestoreOptions struct {
	ReplaceExisting bool
}

func (a *App) RestoreArchive(ctx context.Context, archive, target string) error {
	return a.RestoreArchiveWithOptions(ctx, archive, target, RestoreOptions{})
}

func (a *App) RestoreArchiveWithOptions(ctx context.Context, archive, target string, options RestoreOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, manifest, err := readArchive(archive)
	if err != nil {
		return err
	}
	backupTarget := ""
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("restore conflict: target is not a safe directory: %s", target)
		}
		if !options.ReplaceExisting {
			return fmt.Errorf("restore conflict: target already exists: %s; rerun with --replace-existing only after choosing the explicit safe replacement mode", target)
		}
		backupTarget = target + ".baron-restore-backup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
		if _, backupErr := os.Lstat(backupTarget); backupErr == nil {
			return fmt.Errorf("restore conflict: safe backup target already exists: %s", backupTarget)
		} else if !errors.Is(backupErr, os.ErrNotExist) {
			return backupErr
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stage, err := os.MkdirTemp("", "baron-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for path, data := range entries {
		if path == "manifest.json" {
			continue
		}
		clean, err := safeArchivePath(path)
		if err != nil {
			return err
		}
		if err := writeStageFile(filepath.Join(stage, clean), data); err != nil {
			return err
		}
	}
	// Tencent deployment data is intentionally not copied into a portable
	// archive because it contains protected runtime credentials and Docker
	// volume state. Restore the managed deployment first, then verify the
	// restored user/team/project bindings while the archive is still staged.
	// This prevents a local project state from being installed while it points
	// at an unverified or cross-account Tencent namespace.
	stagedGlobalPath := filepath.Join(stage, "global.json")
	if _, statErr := os.Stat(stagedGlobalPath); statErr == nil {
		stagedGlobal, loadErr := config.LoadGlobalState(stagedGlobalPath)
		if loadErr != nil {
			return fmt.Errorf("decode staged Baron global state before Tencent restore: %w", loadErr)
		}
		stagedGlobal, restoreErr := a.restoreTencentBeforeBinding(ctx, stage, stagedGlobal)
		if restoreErr != nil {
			return fmt.Errorf("restore Tencent deployment before project binding validation: %w", restoreErr)
		}
		if err := config.SaveGlobalState(stagedGlobalPath, stagedGlobal); err != nil {
			return fmt.Errorf("persist verified Tencent identity in staged restore: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if backupTarget != "" {
		if err := os.Rename(target, backupTarget); err != nil {
			return fmt.Errorf("stage existing Baron state for safe restore: %w", err)
		}
	}
	if err := os.Rename(stage, target); err != nil {
		if backupTarget != "" {
			_ = os.Rename(backupTarget, target)
		}
		return fmt.Errorf("install restored state: %w", err)
	}
	_ = manifest
	return nil
}

func verifyArchive(path string) error {
	_, _, err := readArchive(path)
	return err
}

func readArchive(path string) (map[string][]byte, BackupManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, BackupManifest{}, err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, BackupManifest{}, err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	entries := map[string][]byte{}
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, BackupManifest{}, err
		}
		if header.Size > 64*1024*1024 {
			return nil, BackupManifest{}, errors.New("backup entry is too large")
		}
		data, err := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
		if err != nil || int64(len(data)) != header.Size {
			return nil, BackupManifest{}, errors.New("backup entry is truncated")
		}
		entries[header.Name] = data
	}
	manifestData, ok := entries["manifest.json"]
	if !ok {
		return nil, BackupManifest{}, errors.New("backup manifest is missing")
	}
	var manifest BackupManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, BackupManifest{}, err
	}
	if (manifest.Version != 1 && manifest.Version != 2) || !manifest.SecretsExcluded {
		return nil, BackupManifest{}, errors.New("unsupported or unsafe backup manifest")
	}
	if manifest.Version >= 2 && !manifest.TencentDeploymentExcluded {
		return nil, BackupManifest{}, errors.New("backup manifest does not prove Tencent deployment data was excluded")
	}
	allowed := map[string]bool{"manifest.json": true}
	for _, file := range manifest.Files {
		if file.Path == "" || filepath.Base(filepath.FromSlash(file.Path)) == ".env" {
			return nil, BackupManifest{}, errors.New("backup manifest includes a forbidden secret file")
		}
		if _, err := safeArchivePath(file.Path); err != nil {
			return nil, BackupManifest{}, err
		}
		allowed[file.Path] = true
		data, ok := entries[file.Path]
		if !ok {
			return nil, BackupManifest{}, fmt.Errorf("backup file missing: %s", file.Path)
		}
		if int64(len(data)) != file.Size {
			return nil, BackupManifest{}, fmt.Errorf("backup size mismatch: %s", file.Path)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != file.SHA256 {
			return nil, BackupManifest{}, fmt.Errorf("backup checksum mismatch: %s", file.Path)
		}
	}
	for path := range entries {
		if !allowed[path] {
			return nil, BackupManifest{}, fmt.Errorf("backup contains unmanifested entry: %s", path)
		}
	}
	return entries, manifest, nil
}

func writeTarGz(path string, sources map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	keys := make([]string, 0, len(sources))
	for key := range sources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, archivePath := range keys {
		source := sources[archivePath]
		info, err := os.Stat(source)
		if err != nil {
			return err
		}
		if info.IsDir() {
			var files []string
			err := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if !info.IsDir() {
					files = append(files, path)
				}
				return nil
			})
			if err != nil {
				return err
			}
			sort.Strings(files)
			for _, filePath := range files {
				name, _ := filepath.Rel(filepath.Dir(source), filePath)
				if err := addTarFile(tarWriter, filepath.ToSlash(name), filePath); err != nil {
					return err
				}
			}
			continue
		}
		if err := addTarFile(tarWriter, archivePath, source); err != nil {
			return err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	return gzipWriter.Close()
}

func addTarFile(writer *tar.Writer, name, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Unix(0, 0).UTC()}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}

func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return writeStageFile(destination, data)
}

func writeStageFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func safeArchivePath(path string) (string, error) {
	if strings.ContainsRune(path, '\x00') || strings.Contains(path, "\\") {
		return "", fmt.Errorf("unsafe backup path: %s", path)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe backup path: %s", path)
	}
	return clean, nil
}
