package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendLogRedactsAndRotatesBoundedRuntimeLogs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "hook.log")
	if err := AppendLog(path, "token=sk-secret\n", []string{"sk-secret"}, 24); err != nil {
		t.Fatal(err)
	}
	if err := AppendLog(path, strings.Repeat("x", 40), nil, 24); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-secret") {
		t.Fatal("runtime log persisted secret")
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated log: %v", err)
	}
}

func TestAppendLogBoundsSingleOversizedMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.log")
	if err := AppendLog(path, strings.Repeat("x", 1000), nil, 64); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 64 {
		t.Fatalf("oversized log message was not bounded: %d", info.Size())
	}
}
