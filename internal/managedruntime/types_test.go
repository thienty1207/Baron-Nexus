package managedruntime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestReceiptJSONNeverContainsSecretBearingFields(t *testing.T) {
	receipt := Receipt{
		Component:   ComponentStrix,
		Version:     "1.2.3",
		Source:      "https://example.invalid/strix",
		InstallPath: `C:\Users\test\.config\baron\runtime`,
		Generation:  "generation-1",
		SHA256:      strings.Repeat("a", 64),
		VerifiedAt:  time.Unix(1, 0).UTC(),
		BaronOwned:  true,
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"api_key", "token", "secret", "password"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("receipt contains secret-bearing field %q: %s", forbidden, text)
		}
	}
}

func TestResolutionPlanRoundTripsDeterministically(t *testing.T) {
	plan := ResolutionPlan{
		ID: "plan-1", CreatedAt: time.Unix(1, 0).UTC(), Platform: "windows", Architecture: "amd64",
		CompatibilityVersion: "2026-09-03",
		Components:           []ComponentPlan{{ID: ComponentUV, Version: "0.9.0", Source: "catalog", URL: "https://example.invalid/uv", SHA256: strings.Repeat("b", 64), Platform: "windows", Architecture: "amd64"}},
	}
	first, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ResolutionPlan
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("plan JSON is not deterministic:\n%s\n%s", first, second)
	}
}

func TestResolutionPlanRequiresPackageCoordinateForPackageInstall(t *testing.T) {
	plan := ResolutionPlan{
		ID: "plan-package", CreatedAt: time.Unix(1, 0).UTC(), Platform: "windows", Architecture: "amd64",
		Components: []ComponentPlan{{
			ID: ComponentCodex, Version: "1.0.0", Source: "catalog", URL: "https://example.invalid/codex.tgz",
			SHA256: strings.Repeat("a", 64), Platform: "windows", Architecture: "amd64",
			InstallMethod: InstallMethodNPM,
		}},
	}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "package") {
		t.Fatalf("package install without a coordinate was accepted: %v", err)
	}
}

func TestResolutionPlanRejectsPackageBackedNPMBootstrap(t *testing.T) {
	plan := ResolutionPlan{
		ID: "plan-npm-bootstrap", CreatedAt: time.Unix(1, 0).UTC(), Platform: "windows", Architecture: "amd64",
		Components: []ComponentPlan{{
			ID: ComponentNPM, Version: "1.0.0", Source: "catalog", URL: "https://example.invalid/npm.tgz",
			SHA256: strings.Repeat("a", 64), Platform: "windows", Architecture: "amd64",
			InstallMethod: InstallMethodNPM, Package: "npm", EntryPoint: "npm",
		}},
	}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("package-backed npm bootstrap was accepted: %v", err)
	}
}

func TestResolutionPlanRejectsUnknownInstallMethod(t *testing.T) {
	plan := ResolutionPlan{
		ID: "plan-method", CreatedAt: time.Unix(1, 0).UTC(), Platform: "windows", Architecture: "amd64",
		Components: []ComponentPlan{{
			ID: ComponentBun, Version: "1.0.0", Source: "catalog", URL: "https://example.invalid/bun.tar.gz",
			SHA256: strings.Repeat("a", 64), Platform: "windows", Architecture: "amd64",
			InstallMethod: InstallMethod("shell-command"),
		}},
	}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "install method") {
		t.Fatalf("unknown install method was accepted: %v", err)
	}
}
