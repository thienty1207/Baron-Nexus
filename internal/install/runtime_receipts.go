package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baron-shared-brain/baron/internal/config"
	"github.com/baron-shared-brain/baron/internal/managedruntime"
)

func WriteManagedRuntimeReceipt(ctx context.Context, paths managedruntime.Paths, receipt managedruntime.Receipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := receipt.Validate(); err != nil {
		return err
	}
	generation, err := paths.Generation(receipt.Generation)
	if err != nil {
		return err
	}
	if err := validateReceiptInstallPath(paths, generation, receipt.InstallPath); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.Receipts, 0o700); err != nil {
		return err
	}
	component := strings.TrimSpace(string(receipt.Component))
	if component == "" || strings.ContainsAny(component, `/\\`) {
		return errors.New("managed runtime receipt component is not a safe path component")
	}
	path := filepath.Join(paths.Receipts, receipt.Generation+"-"+component+".json")
	if err := paths.ValidateOwned(path); err != nil {
		return err
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("encode managed runtime receipt: %w", err)
	}
	return config.AtomicWriteFile(path, append(data, '\n'), 0o600)
}

func ReadManagedRuntimeReceipts(ctx context.Context, paths managedruntime.Paths, generation string) ([]managedruntime.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := paths.Generation(generation); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(paths.Receipts)
	if errors.Is(err, os.ErrNotExist) {
		return []managedruntime.Receipt{}, nil
	}
	if err != nil {
		return nil, err
	}
	prefix := generation + "-"
	result := make([]managedruntime.Receipt, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(paths.Receipts, entry.Name())
		if err := paths.ValidateOwned(path); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var receipt managedruntime.Receipt
		if err := json.Unmarshal(data, &receipt); err != nil {
			return nil, fmt.Errorf("decode managed runtime receipt %s: %w", entry.Name(), err)
		}
		if receipt.Generation != generation {
			return nil, errors.New("managed runtime receipt generation mismatch")
		}
		if err := receipt.Validate(); err != nil {
			return nil, err
		}
		generationPath, err := paths.Generation(receipt.Generation)
		if err != nil {
			return nil, err
		}
		if err := validateReceiptInstallPath(paths, generationPath, receipt.InstallPath); err != nil {
			return nil, err
		}
		result = append(result, receipt)
	}
	return result, nil
}

func validateReceiptInstallPath(paths managedruntime.Paths, generation, installPath string) error {
	if err := paths.ValidateOwned(installPath); err != nil {
		return err
	}
	relative, err := filepath.Rel(generation, filepath.Clean(installPath))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("managed runtime receipt install path escapes its generation")
	}
	return nil
}
