package managedruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type catalogFixtureClient struct {
	responses map[string][]byte
	calls     map[string]int
}

func (c *catalogFixtureClient) Get(_ context.Context, url string, _ int64) ([]byte, error) {
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[url]++
	data, ok := c.responses[url]
	if !ok {
		return nil, errors.New("metadata fixture not found")
	}
	return data, nil
}

func TestValidateMetadataRejectsMissingHashes(t *testing.T) {
	data := []byte(`{"version":"1.2.3","assets":[{"url":"https://example.invalid/tool","platform":"windows","architecture":"amd64"}]}`)
	if _, err := ValidateMetadata(data, 1024); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("missing hash was not rejected: %v", err)
	}
}

func TestResolverSelectsStableExactPlatformAndQueriesURLOnce(t *testing.T) {
	url := "https://catalog.invalid/strix.json"
	data, err := json.Marshal(map[string]any{"releases": []ReleaseMetadata{
		{Version: "1.9.0-rc.1", Stable: false, Assets: []Asset{{URL: "https://example.invalid/rc", SHA256: strings.Repeat("a", 64), Platform: "windows", Architecture: "amd64"}}},
		{Version: "1.8.0", Stable: true, Assets: []Asset{
			{URL: "https://example.invalid/linux", SHA256: strings.Repeat("b", 64), Platform: "linux", Architecture: "amd64"},
			{URL: "https://example.invalid/windows", SHA256: strings.Repeat("c", 64), Platform: "windows", Architecture: "amd64"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	client := &catalogFixtureClient{responses: map[string][]byte{url: data}}
	resolver := Resolver{
		Metadata: client, Platform: "windows", Architecture: "amd64",
		CatalogURLs: map[ComponentID]string{ComponentStrix: url, ComponentBun: url},
	}
	plan, err := resolver.Resolve(context.Background(), ResolverInput{Components: []ComponentID{ComponentStrix, ComponentBun}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Components) != 2 || plan.Components[0].Version != "1.8.0" || plan.Components[0].URL != "https://example.invalid/windows" {
		t.Fatalf("unexpected resolved plan: %#v", plan.Components)
	}
	if client.calls[url] != 1 {
		t.Fatalf("metadata URL was queried %d times, want once", client.calls[url])
	}
}

func TestResolveStrixPythonUsesTestedMatrix(t *testing.T) {
	pythonURL := "https://catalog.invalid/python.json"
	resolver := Resolver{
		Platform: "linux", Architecture: "amd64",
		TestedMatrix: CompatibilityMatrix{MinPythonMajor: 3, MinPythonMinor: 12, MaxPythonMajor: 3, MaxPythonMinor: 13},
		PythonReleases: []ReleaseMetadata{
			{Version: "3.14.0", Stable: true, Assets: []Asset{{URL: pythonURL + "/3.14", SHA256: strings.Repeat("a", 64), Platform: "linux", Architecture: "amd64"}}},
			{Version: "3.13.5", Stable: true, Assets: []Asset{{URL: pythonURL + "/3.13", SHA256: strings.Repeat("b", 64), Platform: "linux", Architecture: "amd64"}}},
			{Version: "3.12.9", Stable: true, Assets: []Asset{{URL: pythonURL + "/3.12", SHA256: strings.Repeat("c", 64), Platform: "linux", Architecture: "amd64"}}},
		},
	}
	choice, err := resolver.ResolveStrixPython(context.Background(), StrixRelease{Version: "0.4.0", RequiresPython: ">=3.12"})
	if err != nil {
		t.Fatal(err)
	}
	if choice.Version != "3.13.5" || choice.URL != pythonURL+"/3.13" {
		t.Fatalf("unexpected Strix Python choice: %#v", choice)
	}
}

func TestResolverRejectsPrereleaseEvenWhenCatalogMarksItStable(t *testing.T) {
	url := "https://catalog.invalid/prerelease.json"
	data, err := json.Marshal(map[string]any{"releases": []ReleaseMetadata{
		{Version: "2.0.0-rc.1", Stable: true, Assets: []Asset{{URL: "https://example.invalid/rc", SHA256: strings.Repeat("a", 64), Platform: "windows", Architecture: "amd64"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{
		Metadata: &catalogFixtureClient{responses: map[string][]byte{url: data}},
		Platform: "windows", Architecture: "amd64",
		CatalogURLs: map[ComponentID]string{ComponentStrix: url},
	}
	if _, err := resolver.Resolve(context.Background(), ResolverInput{Components: []ComponentID{ComponentStrix}}); err == nil || !strings.Contains(err.Error(), "no stable") {
		t.Fatalf("prerelease-only catalog was accepted: %v", err)
	}
}

func TestResolverAllowsOnlyExplicitlyVerifiedDSHPrerelease(t *testing.T) {
	url := "https://catalog.invalid/dsh-rc.json"
	data, err := json.Marshal(map[string]any{"releases": []ReleaseMetadata{
		{Component: ComponentDSH, Version: "0.1.1-rc.2", Stable: true, VerifiedPrerelease: true, InstallMethod: InstallMethodNPM, Package: "@deepseek-ai/dsh", EntryPoint: "dsh", Assets: []Asset{{URL: "https://example.invalid/dsh.tgz", SHA256: strings.Repeat("a", 64), Platform: "windows", Architecture: "amd64"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{
		Metadata: &catalogFixtureClient{responses: map[string][]byte{url: data}},
		Platform: "windows", Architecture: "amd64",
		CatalogURLs: map[ComponentID]string{ComponentDSH: url},
	}
	plan, err := resolver.Resolve(context.Background(), ResolverInput{Components: []ComponentID{ComponentDSH}})
	if err != nil {
		t.Fatalf("verified DSH prerelease was rejected: %v", err)
	}
	if len(plan.Components) != 1 || plan.Components[0].Version != "0.1.1-rc.2" {
		t.Fatalf("unexpected DSH prerelease plan: %#v", plan.Components)
	}
}

func TestValidateMetadataRejectsVerifiedPrereleaseOutsideDSH(t *testing.T) {
	data, err := json.Marshal(ReleaseMetadata{
		Component: ComponentStrix, Version: "1.6.1-rc.1", Stable: true, VerifiedPrerelease: true,
		InstallMethod: InstallMethodArchive,
		Assets:        []Asset{{URL: "https://example.invalid/strix", SHA256: strings.Repeat("a", 64), Platform: "linux", Architecture: "amd64"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateMetadata(data, 1<<20); err == nil || !strings.Contains(err.Error(), "verified prerelease") {
		t.Fatalf("unscoped verified prerelease was accepted: %v", err)
	}
}

func TestResolverHonorsComponentScopedCatalogEntries(t *testing.T) {
	url := "https://catalog.invalid/bundle.json"
	data, err := json.Marshal(map[string]any{"releases": []ReleaseMetadata{
		{Component: ComponentStrix, Version: "1.0.0", Stable: true, Assets: []Asset{{URL: "https://example.invalid/strix", SHA256: strings.Repeat("a", 64), Platform: "windows", Architecture: "amd64"}}},
		{Component: ComponentBun, Version: "9.0.0", Stable: true, Assets: []Asset{{URL: "https://example.invalid/bun", SHA256: strings.Repeat("b", 64), Platform: "windows", Architecture: "amd64"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{
		Metadata: &catalogFixtureClient{responses: map[string][]byte{url: data}},
		Platform: "windows", Architecture: "amd64", CatalogURLs: map[ComponentID]string{ComponentStrix: url, ComponentBun: url},
	}
	plan, err := resolver.Resolve(context.Background(), ResolverInput{Components: []ComponentID{ComponentStrix, ComponentBun}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Components[0].Version != "1.0.0" || plan.Components[0].URL != "https://example.invalid/strix" || plan.Components[1].Version != "9.0.0" || plan.Components[1].URL != "https://example.invalid/bun" {
		t.Fatalf("component-scoped releases were mixed: %#v", plan.Components)
	}
}

func TestResolverCopiesPackageInstallContractIntoPlan(t *testing.T) {
	url := "https://catalog.invalid/package.json"
	data, err := json.Marshal(map[string]any{"releases": []ReleaseMetadata{
		{Component: ComponentCodex, Version: "1.0.0", Stable: true, InstallMethod: InstallMethodNPM, Package: "@openai/codex", EntryPoint: "codex", Assets: []Asset{{URL: "https://example.invalid/codex.tgz", SHA256: strings.Repeat("a", 64), Platform: "windows", Architecture: "amd64"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{
		Metadata: &catalogFixtureClient{responses: map[string][]byte{url: data}},
		Platform: "windows", Architecture: "amd64", CatalogURLs: map[ComponentID]string{ComponentCodex: url},
	}
	plan, err := resolver.Resolve(context.Background(), ResolverInput{Components: []ComponentID{ComponentCodex}})
	if err != nil {
		t.Fatal(err)
	}
	component := plan.Components[0]
	if component.InstallMethod != InstallMethodNPM || component.Package != "@openai/codex" || component.EntryPoint != "codex" {
		t.Fatalf("resolver dropped package install contract: %#v", component)
	}
}

func TestResolverCanRequireACompleteBundleCatalog(t *testing.T) {
	url := "https://catalog.invalid/incomplete-bundle.json"
	data, err := json.Marshal(map[string]any{"releases": []ReleaseMetadata{
		{Component: ComponentBun, Version: "1.0.0", Stable: true, InstallMethod: InstallMethodArchive, Assets: []Asset{
			{URL: "https://example.invalid/bun-linux", SHA256: strings.Repeat("a", 64), Platform: "linux", Architecture: "amd64"},
			{URL: "https://example.invalid/bun-windows", SHA256: strings.Repeat("b", 64), Platform: "windows", Architecture: "amd64"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{
		Metadata: &catalogFixtureClient{responses: map[string][]byte{url: data}},
		Platform: "windows", Architecture: "amd64",
		CatalogURLs: map[ComponentID]string{ComponentBun: url},
	}
	_, err = resolver.Resolve(context.Background(), ResolverInput{
		Components:            []ComponentID{ComponentBun},
		RequireCompleteBundle: true,
	})
	if err == nil || !strings.Contains(err.Error(), "bundle catalog") {
		t.Fatalf("incomplete bundle catalog was accepted by strict resolver: %v", err)
	}
}

func TestResolverSelectsPythonForTheResolvedStrixConstraint(t *testing.T) {
	url := "https://catalog.invalid/strix-python.json"
	data, err := json.Marshal(map[string]any{"releases": []ReleaseMetadata{
		{Component: ComponentStrix, Version: "2.0.0", Stable: true, RequiresPython: "<3.13", Assets: []Asset{{URL: "https://example.invalid/strix", SHA256: strings.Repeat("a", 64), Platform: "linux", Architecture: "amd64"}}},
		{Component: ComponentPython, Version: "3.13.5", Stable: true, Assets: []Asset{{URL: "https://example.invalid/python-313", SHA256: strings.Repeat("b", 64), Platform: "linux", Architecture: "amd64"}}},
		{Component: ComponentPython, Version: "3.12.9", Stable: true, Assets: []Asset{{URL: "https://example.invalid/python-312", SHA256: strings.Repeat("c", 64), Platform: "linux", Architecture: "amd64"}}},
		{Component: ComponentUV, Version: "0.9.0", Stable: true, Assets: []Asset{{URL: "https://example.invalid/uv", SHA256: strings.Repeat("d", 64), Platform: "linux", Architecture: "amd64"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{
		Metadata: &catalogFixtureClient{responses: map[string][]byte{url: data}},
		Platform: "linux", Architecture: "amd64", CatalogURLs: map[ComponentID]string{ComponentStrix: url, ComponentPython: url, ComponentUV: url},
		TestedMatrix: CompatibilityMatrix{MinPythonMajor: 3, MinPythonMinor: 12, MaxPythonMajor: 3, MaxPythonMinor: 13},
	}
	plan, err := resolver.Resolve(context.Background(), ResolverInput{Components: []ComponentID{ComponentStrix, ComponentPython, ComponentUV}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Components[0].Version != "2.0.0" || plan.Components[1].Version != "3.12.9" {
		t.Fatalf("Strix/Python compatibility was ignored: %#v", plan.Components)
	}
}

func TestComponentDependenciesOrderPackageInstallersAfterManagedPackageManagers(t *testing.T) {
	for _, component := range []ComponentID{ComponentDSH, ComponentCodex} {
		dependencies := componentDependencies(component)
		if len(dependencies) != 1 || dependencies[0] != ComponentPNPM {
			t.Fatalf("dependencies for %s=%#v, want managed pnpm", component, dependencies)
		}
	}
	dependencies := componentDependencies(ComponentPNPM)
	if len(dependencies) != 1 || dependencies[0] != ComponentNode {
		t.Fatalf("dependencies for %s=%#v, want managed node", ComponentPNPM, dependencies)
	}
	dependencies = componentDependencies(ComponentNPM)
	if len(dependencies) != 1 || dependencies[0] != ComponentNode {
		t.Fatalf("dependencies for npm=%#v, want managed node", dependencies)
	}
}

func TestValidateMetadataRejectsOversizedDocumentAndMalformedVersion(t *testing.T) {
	if _, err := ValidateMetadata([]byte(strings.Repeat("x", 33)), 32); err == nil {
		t.Fatal("oversized metadata was accepted")
	}
	data := []byte(`{"version":"not-a-version","assets":[{"url":"https://example.invalid/tool","sha256":"` + strings.Repeat("a", 64) + `","platform":"windows","architecture":"amd64"}]}`)
	if _, err := ValidateMetadata(data, 1024); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("malformed version was not rejected: %v", err)
	}
}

func TestValidateBundleCatalogRequiresEveryManagedComponent(t *testing.T) {
	data, err := json.Marshal(map[string]any{"releases": []ReleaseMetadata{
		{Component: ComponentBun, Version: "1.0.0", Stable: true, Assets: []Asset{{URL: "https://example.invalid/bun", SHA256: strings.Repeat("a", 64), Platform: "linux", Architecture: "amd64"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateBundleCatalog(data, 1<<20); err == nil || !strings.Contains(err.Error(), "bundle catalog") {
		t.Fatalf("incomplete bundle catalog was accepted: %v", err)
	}
}

func TestValidateBundleCatalogRejectsUnscopedReleaseEntries(t *testing.T) {
	data, err := json.Marshal(map[string]any{"releases": []ReleaseMetadata{
		{Version: "1.0.0", Stable: true, Assets: []Asset{{URL: "https://example.invalid/runtime", SHA256: strings.Repeat("a", 64), Platform: "linux", Architecture: "amd64"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateBundleCatalog(data, 1<<20); err == nil || !strings.Contains(err.Error(), "component") {
		t.Fatalf("unscoped release entry was accepted: %v", err)
	}
}

func TestValidateBundleCatalogAcceptsCompleteStableScopedCatalog(t *testing.T) {
	data := completeBundleCatalogData("")
	releases, err := ValidateBundleCatalog(data, 1<<20)
	if err != nil {
		t.Fatalf("complete stable bundle catalog was rejected: %v", err)
	}
	if len(releases) != len(RequiredBundleComponents()) {
		t.Fatalf("validated release count=%d, want %d", len(releases), len(RequiredBundleComponents()))
	}
}

func TestValidateBundleCatalogRejectsUnstableRequiredComponent(t *testing.T) {
	data := completeBundleCatalogData(ComponentBun)
	if _, err := ValidateBundleCatalog(data, 1<<20); err == nil || !strings.Contains(err.Error(), "stable") {
		t.Fatalf("unstable required component was accepted: %v", err)
	}
}

func TestValidateBundleCatalogRejectsPackageBackedNPMBootstrap(t *testing.T) {
	data := completeBundleCatalogData("")
	var envelope struct {
		Releases []ReleaseMetadata `json:"releases"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	for index := range envelope.Releases {
		if envelope.Releases[index].Component == ComponentNPM {
			envelope.Releases[index].InstallMethod = InstallMethodNPM
			envelope.Releases[index].Package = "npm"
			envelope.Releases[index].EntryPoint = "npm"
		}
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateBundleCatalog(data, 1<<20); err == nil || !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("package-backed npm bootstrap was accepted by catalog validation: %v", err)
	}
}

func TestValidateBundleCatalogRequiresReleaseAssetsForSupportedPlatforms(t *testing.T) {
	data := completeBundleCatalogData("")
	var envelope struct {
		Releases []ReleaseMetadata `json:"releases"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	for index := range envelope.Releases {
		envelope.Releases[index].Assets = envelope.Releases[index].Assets[:1]
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateBundleCatalog(data, 1<<20); err == nil || !strings.Contains(err.Error(), "windows/amd64") {
		t.Fatalf("catalog without Windows assets was accepted: %v", err)
	}
}

func completeBundleCatalogData(unstable ComponentID) []byte {
	releases := make([]ReleaseMetadata, 0, len(RequiredBundleComponents()))
	for _, component := range RequiredBundleComponents() {
		release := ReleaseMetadata{
			Component:     component,
			Version:       "1.0.0",
			Stable:        component != unstable,
			InstallMethod: InstallMethodArchive,
			Assets: []Asset{
				{URL: "https://example.invalid/" + string(component) + "-linux", SHA256: strings.Repeat("a", 64), Platform: "linux", Architecture: "amd64"},
				{URL: "https://example.invalid/" + string(component) + "-windows", SHA256: strings.Repeat("b", 64), Platform: "windows", Architecture: "amd64"},
			},
		}
		switch component {
		case ComponentStrix:
			release.InstallMethod, release.Package, release.EntryPoint = InstallMethodUVTool, "strix-agent", "strix"
		case ComponentPNPM:
			release.InstallMethod, release.Package, release.EntryPoint = InstallMethodNPM, "pnpm", "pnpm"
		case ComponentDSH:
			release.InstallMethod, release.Package, release.EntryPoint = InstallMethodNPM, "@deepseek-ai/dsh", "dsh"
		case ComponentCodex:
			release.InstallMethod, release.Package, release.EntryPoint = InstallMethodNPM, "@openai/codex", "codex"
		}
		releases = append(releases, release)
	}
	data, _ := json.Marshal(map[string]any{"releases": releases})
	return data
}
