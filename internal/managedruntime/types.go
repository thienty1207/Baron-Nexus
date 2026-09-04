package managedruntime

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type ComponentID string

// InstallMethod identifies how a verified catalog artifact becomes a usable
// component inside a managed generation. Empty remains a legacy alias for
// archive so older programmatic plans remain readable; release catalogs must
// declare it explicitly.
type InstallMethod string

const (
	InstallMethodArchive InstallMethod = "archive"
	InstallMethodNPM     InstallMethod = "npm"
	InstallMethodPNPM    InstallMethod = "pnpm"
	InstallMethodUVTool  InstallMethod = "uv-tool"
)

const (
	ComponentUV      ComponentID = "uv"
	ComponentPython  ComponentID = "python"
	ComponentStrix   ComponentID = "strix"
	ComponentBun     ComponentID = "bun"
	ComponentGo      ComponentID = "go"
	ComponentNode    ComponentID = "node"
	ComponentNPM     ComponentID = "npm"
	ComponentPNPM    ComponentID = "pnpm"
	ComponentDSH     ComponentID = "dsh"
	ComponentCodex   ComponentID = "codex"
	ComponentTencent ComponentID = "tencent-memory"
)

const (
	maxPlanID         = 128
	maxComponentText  = 2048
	maxPathText       = 4096
	maxComponentCount = 64
)

type ComponentPlan struct {
	ID            ComponentID   `json:"id"`
	Version       string        `json:"version"`
	Source        string        `json:"source"`
	InstallMethod InstallMethod `json:"install_method,omitempty"`
	Package       string        `json:"package,omitempty"`
	EntryPoint    string        `json:"entry_point,omitempty"`
	URL           string        `json:"url,omitempty"`
	SHA256        string        `json:"sha256,omitempty"`
	Platform      string        `json:"platform"`
	Architecture  string        `json:"architecture"`
	Dependencies  []ComponentID `json:"dependencies,omitempty"`
}

type ResolutionPlan struct {
	ID                   string          `json:"id"`
	CreatedAt            time.Time       `json:"created_at"`
	Platform             string          `json:"platform"`
	Architecture         string          `json:"architecture"`
	Components           []ComponentPlan `json:"components"`
	CompatibilityVersion string          `json:"compatibility_version"`
}

type Receipt struct {
	Component          ComponentID   `json:"component"`
	Version            string        `json:"version"`
	Source             string        `json:"source"`
	InstallMethod      InstallMethod `json:"install_method,omitempty"`
	Package            string        `json:"package,omitempty"`
	EntryPoint         string        `json:"entry_point,omitempty"`
	Platform           string        `json:"platform,omitempty"`
	Architecture       string        `json:"architecture,omitempty"`
	InstallPath        string        `json:"install_path"`
	Executables        []string      `json:"executables,omitempty"`
	SHA256             string        `json:"sha256,omitempty"`
	Generation         string        `json:"generation"`
	BaronOwned         bool          `json:"baron_owned"`
	PreviousGeneration string        `json:"previous_generation,omitempty"`
	VerifiedAt         time.Time     `json:"verified_at"`
}

// GenerationManifest is the immutable activation inventory for one staged
// generation. Individual receipts remain the ownership records; this manifest
// makes completeness verifiable without trusting directory enumeration alone.
type GenerationManifest struct {
	PlanID               string     `json:"plan_id"`
	Generation           string     `json:"generation"`
	Platform             string     `json:"platform"`
	Architecture         string     `json:"architecture"`
	CompatibilityVersion string     `json:"compatibility_version,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	Components           []Receipt  `json:"components"`
	Launchers            []Launcher `json:"launchers,omitempty"`
}

type Paths struct {
	Root              string `json:"root"`
	Generations       string `json:"generations"`
	Cache             string `json:"cache"`
	Credentials       string `json:"credentials"`
	Receipts          string `json:"receipts"`
	Bin               string `json:"bin"`
	LauncherDirectory string `json:"launcher_directory,omitempty"`
	Current           string `json:"current"`
	Previous          string `json:"previous"`
	Operations        string `json:"operations"`
}

func (p ResolutionPlan) Validate() error {
	if strings.TrimSpace(p.ID) == "" || len(p.ID) > maxPlanID || strings.ContainsAny(p.ID, `/\\`) || p.ID == "." || p.ID == ".." {
		return errors.New("managed runtime plan ID is required and bounded")
	}
	if p.CreatedAt.IsZero() {
		return errors.New("managed runtime plan creation time is required")
	}
	if strings.TrimSpace(p.Platform) == "" || strings.TrimSpace(p.Architecture) == "" {
		return errors.New("managed runtime plan platform and architecture are required")
	}
	if len(p.Components) == 0 || len(p.Components) > maxComponentCount {
		return errors.New("managed runtime plan must contain a bounded component list")
	}
	seen := make(map[ComponentID]struct{}, len(p.Components))
	for _, component := range p.Components {
		if _, ok := seen[component.ID]; ok || strings.TrimSpace(string(component.ID)) == "" {
			return fmt.Errorf("managed runtime plan has duplicate or empty component %q", component.ID)
		}
		seen[component.ID] = struct{}{}
		if err := validateComponentPlan(component); err != nil {
			return err
		}
	}
	return nil
}

func validateComponentPlan(component ComponentPlan) error {
	for name, value := range map[string]string{
		"component ID": string(component.ID), "version": component.Version,
		"source": component.Source, "URL": component.URL, "SHA-256": component.SHA256,
		"platform": component.Platform, "architecture": component.Architecture,
	} {
		if len(value) > maxComponentText {
			return fmt.Errorf("managed runtime %s is too long", name)
		}
	}
	if len(component.Dependencies) > maxComponentCount {
		return errors.New("managed runtime component dependency list is too large")
	}
	if strings.ContainsAny(string(component.ID), `/\\`) || component.ID == "." || component.ID == ".." {
		return fmt.Errorf("managed runtime component ID is not a safe path component: %q", component.ID)
	}
	if err := validateInstallMethod(component.InstallMethod); err != nil {
		return err
	}
	method := component.effectiveInstallMethod()
	if err := validateComponentInstallMethod(component.ID, method); err != nil {
		return err
	}
	if method == InstallMethodNPM || method == InstallMethodPNPM || method == InstallMethodUVTool {
		if err := validatePackageCoordinate(component.Package); err != nil {
			return fmt.Errorf("managed runtime %s package: %w", component.ID, err)
		}
	}
	if strings.TrimSpace(component.EntryPoint) != "" {
		if err := validateEntryPoint(component.EntryPoint); err != nil {
			return fmt.Errorf("managed runtime %s entry point: %w", component.ID, err)
		}
	}
	if strings.TrimSpace(component.URL) != "" {
		if len(component.SHA256) != 64 {
			return fmt.Errorf("managed runtime %s requires a SHA-256 checksum", component.ID)
		}
		if _, err := hex.DecodeString(component.SHA256); err != nil {
			return fmt.Errorf("managed runtime %s has an invalid SHA-256 checksum", component.ID)
		}
	}
	return nil
}

func validateComponentInstallMethod(component ComponentID, method InstallMethod) error {
	if component == ComponentNPM && method != InstallMethodArchive {
		return errors.New("managed npm bootstrap must use archive install")
	}
	return nil
}

func (p ComponentPlan) effectiveInstallMethod() InstallMethod {
	if strings.TrimSpace(string(p.InstallMethod)) == "" {
		return InstallMethodArchive
	}
	return p.InstallMethod
}

// EffectiveInstallMethod returns the method used by the staging manager. An
// empty value is intentionally normalized for compatibility with pre-bundle
// callers that constructed plans in memory.
func (p ComponentPlan) EffectiveInstallMethod() InstallMethod {
	return p.effectiveInstallMethod()
}

func validateInstallMethod(method InstallMethod) error {
	switch method {
	case "", InstallMethodArchive, InstallMethodNPM, InstallMethodPNPM, InstallMethodUVTool:
		return nil
	default:
		return fmt.Errorf("managed runtime install method %q is unsupported", method)
	}
}

func validatePackageCoordinate(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("package coordinate is required")
	}
	if len(value) > maxComponentText || strings.ContainsAny(value, " \t\r\n\\") || strings.HasPrefix(value, "-") || strings.Contains(value, "..") {
		return errors.New("package coordinate is invalid")
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			continue
		}
		switch char {
		case '@', '/', '-', '_', '.', '~':
		default:
			return errors.New("package coordinate contains an unsupported character")
		}
	}
	return nil
}

func validateEntryPoint(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxComponentText || value == "." || value == ".." || strings.ContainsAny(value, "/\\\\ \t\r\n") {
		return errors.New("entry point must be one executable name")
	}
	return nil
}

func (r Receipt) Validate() error {
	if strings.TrimSpace(string(r.Component)) == "" || strings.TrimSpace(r.Version) == "" || strings.TrimSpace(r.Source) == "" {
		return errors.New("managed runtime receipt component, version, and source are required")
	}
	if !r.BaronOwned {
		return errors.New("managed runtime receipt must identify Baron-owned content")
	}
	if err := validateInstallMethod(r.InstallMethod); err != nil {
		return err
	}
	method := r.InstallMethod
	if strings.TrimSpace(string(method)) == "" {
		method = InstallMethodArchive
	}
	if method == InstallMethodNPM || method == InstallMethodPNPM || method == InstallMethodUVTool {
		if err := validatePackageCoordinate(r.Package); err != nil {
			return fmt.Errorf("managed runtime receipt package: %w", err)
		}
	}
	if strings.TrimSpace(r.EntryPoint) != "" {
		if err := validateEntryPoint(r.EntryPoint); err != nil {
			return fmt.Errorf("managed runtime receipt entry point: %w", err)
		}
	}
	for name, value := range map[string]string{
		"component": string(r.Component), "version": r.Version, "source": r.Source,
		"install path": r.InstallPath, "generation": r.Generation, "sha256": r.SHA256,
	} {
		if len(value) > maxPathText {
			return fmt.Errorf("managed runtime receipt %s is too long", name)
		}
	}
	if strings.TrimSpace(r.InstallPath) == "" || strings.TrimSpace(r.Generation) == "" {
		return errors.New("managed runtime receipt install path and generation are required")
	}
	if len(r.Executables) > maxComponentCount {
		return errors.New("managed runtime receipt executable list is too large")
	}
	for _, executable := range r.Executables {
		if len(executable) > maxPathText {
			return errors.New("managed runtime receipt executable path is too long")
		}
	}
	return nil
}

func (m GenerationManifest) Validate() error {
	if strings.TrimSpace(m.PlanID) == "" || len(m.PlanID) > maxPlanID || strings.ContainsAny(m.PlanID, `/\\`) || m.PlanID == "." || m.PlanID == ".." {
		return errors.New("managed runtime generation manifest plan ID is required and bounded")
	}
	if strings.TrimSpace(m.Generation) == "" || len(m.Generation) > maxPlanID || strings.ContainsAny(m.Generation, `/\\`) || m.Generation == "." || m.Generation == ".." {
		return errors.New("managed runtime generation manifest generation is required and bounded")
	}
	if strings.TrimSpace(m.Platform) == "" || strings.TrimSpace(m.Architecture) == "" || m.CreatedAt.IsZero() {
		return errors.New("managed runtime generation manifest identity is required")
	}
	if len(m.Components) == 0 || len(m.Components) > maxComponentCount {
		return errors.New("managed runtime generation manifest must contain a bounded component list")
	}
	seen := make(map[ComponentID]struct{}, len(m.Components))
	for _, receipt := range m.Components {
		if _, ok := seen[receipt.Component]; ok {
			return fmt.Errorf("managed runtime generation manifest contains duplicate component %q", receipt.Component)
		}
		seen[receipt.Component] = struct{}{}
		if receipt.Generation != m.Generation {
			return fmt.Errorf("managed runtime generation manifest receipt %s has a generation mismatch", receipt.Component)
		}
		if err := receipt.Validate(); err != nil {
			return err
		}
	}
	launcherNames := make(map[string]struct{}, len(m.Launchers))
	launcherPaths := make(map[string]struct{}, len(m.Launchers))
	for _, launcher := range m.Launchers {
		name, err := validateLauncherName(launcher.Name)
		if err != nil {
			return err
		}
		if err := validateLauncherClientIdentity(launcher.ClientIdentity); err != nil {
			return err
		}
		if _, err := normalizeLauncherManagedPath(launcher.ManagedPath); err != nil {
			return err
		}
		if _, ok := launcherNames[name]; ok {
			return fmt.Errorf("managed runtime generation manifest contains duplicate launcher %q", name)
		}
		launcherNames[name] = struct{}{}
		path := strings.TrimSpace(launcher.Path)
		target := strings.TrimSpace(launcher.Target)
		if path == "" || target == "" || len(path) > maxPathText || len(target) > maxPathText || !strings.Contains(filepath.Base(path), name) {
			return fmt.Errorf("managed runtime generation manifest launcher %q is invalid", name)
		}
		if _, ok := launcherPaths[path]; ok {
			return fmt.Errorf("managed runtime generation manifest contains duplicate launcher path %q", path)
		}
		launcherPaths[path] = struct{}{}
	}
	return nil
}

func (r Receipt) MarshalJSON() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	type receipt Receipt
	return json.Marshal(receipt(r))
}

func (p ResolutionPlan) MarshalJSON() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	type plan ResolutionPlan
	return json.Marshal(plan(p))
}

func (m GenerationManifest) MarshalJSON() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	type manifest GenerationManifest
	return json.Marshal(manifest(m))
}
