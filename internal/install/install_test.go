package install

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexProjectTrustedReadsPersistedProjectTrust(t *testing.T) {
	codexHome := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "Baron project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	config := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", projectRoot)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if !CodexProjectTrusted(projectRoot) {
		t.Fatal("persisted Codex project trust should be recognized")
	}
}

func TestCodexHookMergePreservesCustomConfigAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	original := []byte(`{"hooks":{"SessionStart":[{"command":"custom-hook"}]},"skills":["user-skill"],"other":{"keep":true}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MergeCodexHooks(path, "baron"); err != nil {
		t.Fatal(err)
	}
	if err := MergeCodexHooks(path, "baron"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config["skills"].([]any)[0] != "user-skill" || config["other"].(map[string]any)["keep"] != true {
		t.Fatalf("custom Codex config changed: %#v", config)
	}
	hooks := config["hooks"].(map[string]any)["SessionStart"].([]any)
	baronCount := 0
	for _, item := range hooks {
		entry := item.(map[string]any)
		if command, ok := entry["command"].(string); ok && strings.Contains(command, "baron hook codex") {
			baronCount++
		}
		if nested, ok := entry["hooks"].([]any); ok {
			for _, raw := range nested {
				command, _ := raw.(map[string]any)["command"].(string)
				if strings.Contains(command, "baron hook codex") {
					baronCount++
				}
			}
		}
	}
	if baronCount != 1 {
		t.Fatalf("Baron hook count=%d", baronCount)
	}
	if backups, _ := filepath.Glob(path + ".baron-backup-*"); len(backups) == 0 {
		t.Fatal("expected backup before editing user-owned Codex config")
	}
}

func TestCodexHookMergeEmitsOfficialNestedCommandShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if err := MergeCodexHooks(path, "baron"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	events := config["hooks"].(map[string]any)
	groups := events["SessionStart"].([]any)
	found := false
	for _, raw := range groups {
		group := raw.(map[string]any)
		commands, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		for _, commandRaw := range commands {
			command := commandRaw.(map[string]any)
			if command["type"] == "command" && command["command"] == "baron hook codex SessionStart" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("official nested Baron command hook missing: %#v", groups)
	}
}

func TestCodexHookMergeUsesCodexSupportedTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if err := MergeCodexHooks(path, "baron"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	events := config["hooks"].(map[string]any)
	groups := events["SessionEnd"].([]any)
	for _, raw := range groups {
		group := raw.(map[string]any)
		commands, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		for _, commandRaw := range commands {
			command := commandRaw.(map[string]any)
			if command["command"] != "baron hook codex SessionEnd" {
				continue
			}
			if timeout, ok := command["timeout"].(float64); !ok || timeout != 3 {
				t.Fatalf("Codex hook timeout=%v, want 3 seconds", command["timeout"])
			}
			return
		}
	}
	t.Fatal("Baron SessionEnd hook missing")
}

func TestCodexHookMergeRepairsExistingHookTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	original := []byte(`{"hooks":{"SessionEnd":[{"hooks":[{"type":"command","command":"baron hook codex SessionEnd","timeout":5}]}]}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MergeCodexHooks(path, "baron"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	events := config["hooks"].(map[string]any)
	groups := events["SessionEnd"].([]any)
	for _, raw := range groups {
		group := raw.(map[string]any)
		commands, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		for _, commandRaw := range commands {
			command := commandRaw.(map[string]any)
			if command["command"] != "baron hook codex SessionEnd" {
				continue
			}
			if timeout, ok := command["timeout"].(float64); !ok || timeout != 3 {
				t.Fatalf("existing Codex hook timeout=%v, want 3 seconds", command["timeout"])
			}
			return
		}
	}
	t.Fatal("existing Baron SessionEnd hook missing")
}

func TestCodexHookInspectionSeparatesConfigurationFromInteractiveTrust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if err := MergeCodexHooks(path, "baron"); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectCodexHooks(path, "baron")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != CodexHooksApprovalNeeded || !inspection.ApprovalRequired || len(inspection.MissingEvents) != 0 {
		t.Fatalf("hook inspection conflated config with trust: %#v", inspection)
	}
	if !strings.Contains(CodexHookApprovalInstruction, "approve") || !strings.Contains(CodexHookApprovalInstruction, "baron test") {
		t.Fatalf("approval instruction is not actionable: %s", CodexHookApprovalInstruction)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "sk-") {
		t.Fatalf("hook inspection touched unsafe config: err=%v", err)
	}
}

func TestInstallCodexUsesLatestOfficialPackageAndReportedVersion(t *testing.T) {
	fixture := &codexInstallFixture{commandFixture: &commandFixture{
		available: map[string]bool{"npm": true},
		outputs:   map[string]string{"codex --version": "codex 0.150.0"},
	}}
	source, err := InstallCodexWithSource(context.Background(), fixture, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if source != "npm:@openai/codex" {
		t.Fatalf("unexpected Codex install source: %s", source)
	}
	if len(fixture.calls) != 2 || fixture.calls[0] != "npm install --global @openai/codex@latest" || fixture.calls[1] != "codex --version" {
		t.Fatalf("Codex installer did not use the latest official path: %#v", fixture.calls)
	}
}

func TestInstallCodexWithVersionReportsLatestCommandVersion(t *testing.T) {
	fixture := &codexInstallFixture{commandFixture: &commandFixture{
		available: map[string]bool{"npm": true},
		outputs:   map[string]string{"codex --version": "codex-cli 0.150.0"},
	}}
	_, version, err := InstallCodexWithVersion(context.Background(), fixture, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if version != "0.150.0" {
		t.Fatalf("reported Codex version=%q, want 0.150.0", version)
	}
}

func TestInstallCodexRefreshesExistingBinaryToLatest(t *testing.T) {
	fixture := &commandFixture{
		available: map[string]bool{"codex": true, "npm": true},
		outputs:   map[string]string{"codex --version": "codex-cli 0.149.0"},
	}
	source, err := InstallCodexWithSource(context.Background(), fixture, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if source != "npm:@openai/codex" {
		t.Fatalf("unexpected refreshed Codex source: %s", source)
	}
	if len(fixture.calls) != 2 || fixture.calls[0] != "npm install --global @openai/codex@latest" || fixture.calls[1] != "codex --version" {
		t.Fatalf("existing Codex was not refreshed to latest: %#v", fixture.calls)
	}
}

func TestInstallCodexFallsBackToSudoForRootOwnedGlobalNpmPrefix(t *testing.T) {
	fixture := &npmGlobalPermissionFixture{
		commandFixture: &commandFixture{available: map[string]bool{"npm": true, "sudo": true}, outputs: map[string]string{}},
		installName:    "codex",
	}
	_, version, err := InstallCodexWithVersion(context.Background(), fixture, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if version != "0.150.0" || len(fixture.calls) != 3 || fixture.calls[1] != "sudo -n npm install --global @openai/codex@latest" {
		t.Fatalf("unexpected sudo npm fallback=%q calls=%#v", version, fixture.calls)
	}
}

func TestInstallCodexRejectsWrongVersionAfterNPMInstall(t *testing.T) {
	fixture := &codexInstallFixture{commandFixture: &commandFixture{
		available: map[string]bool{"npm": true},
		outputs:   map[string]string{"codex --version": "codex 0.148.0"},
	}}
	if _, err := InstallCodexWithSource(context.Background(), fixture, "0.149.0"); err == nil || !strings.Contains(err.Error(), "0.149.0") {
		t.Fatalf("wrong post-install Codex version was accepted: %v", err)
	}
}

func TestDSHBaselineMergePreservesUserPlugins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.json")
	if err := os.WriteFile(path, []byte(`{"plugins":["user-plugin"],"mcpServers":{"custom":{"command":"custom"}},"baron":{"user_extension":"keep"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := EnsureDSHBaseline(path, DSHOptions{AdapterCommand: "baron hook dsh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Components) != 4 {
		t.Fatalf("components=%#v", report.Components)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	plugins := config["plugins"].([]any)
	if plugins[0] != "user-plugin" {
		t.Fatalf("user plugin moved or changed: %#v", plugins)
	}
	if _, ok := config["mcpServers"].(map[string]any)["ddg-search"]; !ok {
		t.Fatal("mandatory ddg-search registration missing")
	}
	if _, ok := config["baron"]; !ok {
		t.Fatal("Baron-owned config fragment missing")
	}
	if config["baron"].(map[string]any)["user_extension"] != "keep" {
		t.Fatalf("unrelated Baron config was overwritten: %#v", config["baron"])
	}
}

func TestReceiptDoesNotPersistSecretAndLegacyCollisionIsDetected(t *testing.T) {
	root := t.TempDir()
	receiptPath := filepath.Join(root, "receipts", "dsh.json")
	if err := WriteReceipt(receiptPath, Receipt{Component: "dsh", Version: "0.1.0", Source: "npm", Secret: "sk-secret"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-secret") {
		t.Fatal("receipt persisted secret")
	}
	legacy := filepath.Join(root, "baron")
	if err := os.WriteFile(legacy, []byte("legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !LegacyCollision(legacy) {
		t.Fatal("legacy executable collision was not detected")
	}
}

func TestDSHProfilePatchPreservesUserRowsAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "profiles", "web", "cordis.patch.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("- insert:\n    - id: user-row\n      name: user-plugin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDSHProfilePatch(path); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDSHProfilePatch(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "id: user-row") || strings.Count(text, "baron-owned: ddg-search") != 1 || strings.Count(text, "serverName: 'ddg-search'") != 1 {
		t.Fatalf("profile patch merge was not preserved/idempotent: %s", text)
	}
	if backups, _ := filepath.Glob(path + ".baron-backup-*"); len(backups) != 1 {
		t.Fatalf("expected one backup before first edit, got %d", len(backups))
	}
}

func TestRemoveDSHProfilePatchPreservesContentAfterBaronBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cordis.patch.yml")
	if err := EnsureDSHProfilePatch(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("\nuser-after: true\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := RemoveDSHProfilePatch(path)
	if err != nil || !changed {
		t.Fatalf("remove changed=%v err=%v", changed, err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "baron-owned: ddg-search") || !strings.Contains(string(data), "user-after: true") {
		t.Fatalf("user content after Baron block was not preserved: %s", data)
	}
}

func TestDSHProfilePatchReplacesFreshEmptySequenceWithoutCreatingInvalidYAML(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "profiles", "web", "cordis.patch.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# DSH default patch header\n[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDSHProfilePatch(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "[]\n# baron-owned") || !strings.Contains(text, "# DSH default patch header") || !strings.Contains(text, "# baron-owned: ddg-search") {
		t.Fatalf("fresh DSH empty sequence was not normalized: %s", text)
	}
	if strings.Count(text, "- insert:") != 1 {
		t.Fatalf("unexpected DSH patch rows: %s", text)
	}
}

func TestEmbeddedDSHAdapterMaterializesPrivateBaronOwnedPackage(t *testing.T) {
	target := filepath.Join(t.TempDir(), "dsh-adapter")
	if err := InstallEmbeddedDSHAdapter(target); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"package.json", "index.js", "cordis.patch.yml"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Fatalf("embedded adapter file %s missing: %v", name, err)
		}
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("adapter target permissions are not private: info=%v err=%v", info, err)
	}
}
