package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baron-shared-brain/baron/internal/managedruntime"
)

func TestManagedRuntimeReceiptStoreRoundTripsOwnedGeneration(t *testing.T) {
	paths, err := managedruntime.ResolvePaths(filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	receipt := managedruntime.Receipt{
		Component: managedruntime.ComponentGo, Version: "1.27.0", Source: "catalog",
		InstallPath: filepath.Join(paths.Root, "generations", "generation-1", "go"), Generation: "generation-1",
		SHA256: strings.Repeat("a", 64), BaronOwned: true, VerifiedAt: time.Unix(1, 0).UTC(),
	}
	if err := WriteManagedRuntimeReceipt(context.Background(), paths, receipt); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManagedRuntimeReceipts(context.Background(), paths, "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Component != managedruntime.ComponentGo {
		t.Fatalf("unexpected managed receipts: %#v", got)
	}
}

func TestManagedRuntimeReceiptStoreRejectsForeignGeneration(t *testing.T) {
	paths, err := managedruntime.ResolvePaths(filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	receipt := managedruntime.Receipt{
		Component: managedruntime.ComponentGo, Version: "1.27.0", Source: "catalog",
		InstallPath: filepath.Join(paths.Root, "external", "go"), Generation: "foreign",
		SHA256: strings.Repeat("a", 64), BaronOwned: true, VerifiedAt: time.Unix(1, 0).UTC(),
	}
	if err := WriteManagedRuntimeReceipt(context.Background(), paths, receipt); err == nil {
		t.Fatal("foreign receipt was accepted")
	}
	if _, err := os.Stat(paths.Receipts); !os.IsNotExist(err) {
		t.Fatalf("foreign receipt created managed state: %v", err)
	}
}
