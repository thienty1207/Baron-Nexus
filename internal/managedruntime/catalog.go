package managedruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMetadataLimit int64 = 8 << 20
	maxAssetBytes        int64 = 8 << 30
)

type Asset struct {
	Name         string `json:"name,omitempty"`
	URL          string `json:"url"`
	SHA256       string `json:"sha256"`
	Platform     string `json:"platform"`
	Architecture string `json:"architecture"`
	Size         int64  `json:"size,omitempty"`
}

type PlatformTarget struct {
	Platform     string
	Architecture string
}

type ReleaseMetadata struct {
	Component          ComponentID   `json:"component,omitempty"`
	Version            string        `json:"version"`
	Stable             bool          `json:"stable,omitempty"`
	VerifiedPrerelease bool          `json:"verified_prerelease,omitempty"`
	RequiresPython     string        `json:"requires_python,omitempty"`
	InstallMethod      InstallMethod `json:"install_method"`
	Package            string        `json:"package,omitempty"`
	EntryPoint         string        `json:"entry_point,omitempty"`
	Assets             []Asset       `json:"assets"`
}

type MetadataClient interface {
	Get(context.Context, string, int64) ([]byte, error)
}

type HTTPMetadataClient struct {
	HTTP          *http.Client
	AllowInsecure bool
}

func (c HTTPMetadataClient) Get(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(c.AllowInsecure && parsed.Scheme == "http")) {
		return nil, errors.New("managed runtime metadata URL must use HTTPS")
	}
	if maxBytes <= 0 {
		maxBytes = defaultMetadataLimit
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create metadata request: %w", err)
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("download metadata: HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read metadata: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("managed runtime metadata exceeds the size limit")
	}
	return data, nil
}

type Resolver struct {
	HTTP           *http.Client
	Metadata       MetadataClient
	Platform       string
	Architecture   string
	TestedMatrix   CompatibilityMatrix
	CatalogURLs    map[ComponentID]string
	PythonReleases []ReleaseMetadata
	MetadataCache  *MetadataCache
	Now            func() time.Time
}

type MetadataCache struct {
	mu      sync.Mutex
	Entries map[string][]byte
}

type ResolverInput struct {
	Platform             string
	Architecture         string
	Components           []ComponentID
	MetadataURLs         map[ComponentID]string
	CompatibilityVersion string
	Offline              bool
	// RequireCompleteBundle makes resolution fail closed unless the selected
	// metadata document covers every managed component and supported target.
	// Component-scoped resolution keeps the default false value for callers
	// that intentionally resolve a smaller, independently published catalog.
	RequireCompleteBundle bool
}

type CompatibilityMatrix struct {
	MinPythonMajor int
	MinPythonMinor int
	MaxPythonMajor int
	MaxPythonMinor int
}

type StrixRelease struct {
	Version        string
	RequiresPython string
}

type PythonChoice struct {
	Version string
	URL     string
	SHA256  string
}

func (r Resolver) Resolve(ctx context.Context, input ResolverInput) (ResolutionPlan, error) {
	if err := ctx.Err(); err != nil {
		return ResolutionPlan{}, err
	}
	platform := strings.TrimSpace(input.Platform)
	if platform == "" {
		platform = strings.TrimSpace(r.Platform)
	}
	if platform == "" {
		platform = runtime.GOOS
	}
	architecture := strings.TrimSpace(input.Architecture)
	if architecture == "" {
		architecture = strings.TrimSpace(r.Architecture)
	}
	if architecture == "" {
		architecture = runtime.GOARCH
	}
	components := append([]ComponentID(nil), input.Components...)
	if len(components) == 0 {
		for component := range r.CatalogURLs {
			components = append(components, component)
		}
		sort.Slice(components, func(i, j int) bool { return components[i] < components[j] })
	}
	if len(components) == 0 {
		return ResolutionPlan{}, errors.New("managed runtime resolver has no components")
	}
	metadataCache := map[string][]ReleaseMetadata{}
	loadReleases := func(component ComponentID) ([]ReleaseMetadata, error) {
		metadataURL := strings.TrimSpace(input.MetadataURLs[component])
		if metadataURL == "" {
			metadataURL = strings.TrimSpace(r.CatalogURLs[component])
		}
		if metadataURL == "" {
			return nil, fmt.Errorf("no metadata URL configured for component %q", component)
		}
		releases, ok := metadataCache[metadataURL]
		if ok {
			return releases, nil
		}
		data, err := r.loadMetadata(ctx, metadataURL, input.Offline)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", component, err)
		}
		if input.RequireCompleteBundle {
			if _, err := ValidateBundleCatalog(data, defaultMetadataLimit); err != nil {
				return nil, fmt.Errorf("resolve %s: %w", component, err)
			}
		}
		releases, err = decodeCatalog(data)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", component, err)
		}
		metadataCache[metadataURL] = releases
		return releases, nil
	}
	requested := make(map[ComponentID]struct{}, len(components))
	for _, component := range components {
		requested[component] = struct{}{}
	}
	var strixRelease ReleaseMetadata
	var strixAsset Asset
	strixResolved := false
	if _, ok := requested[ComponentStrix]; ok {
		releases, err := loadReleases(ComponentStrix)
		if err != nil {
			return ResolutionPlan{}, err
		}
		strixRelease, strixAsset, err = selectRelease(releases, ComponentStrix, platform, architecture)
		if err != nil {
			return ResolutionPlan{}, err
		}
		strixResolved = true
	}
	var pythonRelease ReleaseMetadata
	var pythonAsset Asset
	pythonResolved := false
	if _, ok := requested[ComponentPython]; ok {
		releases, err := loadReleases(ComponentPython)
		if err != nil {
			return ResolutionPlan{}, err
		}
		var constraints pythonConstraints
		if strixResolved && strings.TrimSpace(strixRelease.RequiresPython) != "" {
			constraints, err = parsePythonConstraint(strixRelease.RequiresPython)
			if err != nil {
				return ResolutionPlan{}, err
			}
		}
		pythonRelease, pythonAsset, err = selectPythonRelease(releases, platform, architecture, r.TestedMatrix, constraints)
		if err != nil {
			return ResolutionPlan{}, err
		}
		pythonResolved = true
	} else if strixResolved && strings.TrimSpace(strixRelease.RequiresPython) != "" {
		return ResolutionPlan{}, errors.New("Strix resolution requires a Python component in the managed plan")
	}
	plans := make([]ComponentPlan, 0, len(components))
	for _, component := range components {
		if err := ctx.Err(); err != nil {
			return ResolutionPlan{}, err
		}
		var release ReleaseMetadata
		var asset Asset
		var err error
		switch {
		case component == ComponentStrix && strixResolved:
			release, asset = strixRelease, strixAsset
		case component == ComponentPython && pythonResolved:
			release, asset = pythonRelease, pythonAsset
		default:
			releases, loadErr := loadReleases(component)
			if loadErr != nil {
				return ResolutionPlan{}, loadErr
			}
			release, asset, err = selectRelease(releases, component, platform, architecture)
			if err != nil {
				return ResolutionPlan{}, err
			}
		}
		plans = append(plans, ComponentPlan{
			ID:            component,
			Version:       release.Version,
			Source:        "catalog",
			InstallMethod: release.InstallMethod,
			Package:       release.Package,
			EntryPoint:    release.EntryPoint,
			URL:           asset.URL,
			SHA256:        asset.SHA256,
			Platform:      platform,
			Architecture:  architecture,
			Dependencies:  componentDependencies(component),
		})
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	compatibilityVersion := strings.TrimSpace(input.CompatibilityVersion)
	plan := ResolutionPlan{
		CreatedAt:            now,
		Platform:             platform,
		Architecture:         architecture,
		Components:           plans,
		CompatibilityVersion: compatibilityVersion,
	}
	plan.ID = planID(plan)
	if err := plan.Validate(); err != nil {
		return ResolutionPlan{}, err
	}
	return plan, nil
}

func (r Resolver) ResolveStrixPython(ctx context.Context, strix StrixRelease) (PythonChoice, error) {
	if err := ctx.Err(); err != nil {
		return PythonChoice{}, err
	}
	constraint, err := parsePythonConstraint(strix.RequiresPython)
	if err != nil {
		return PythonChoice{}, err
	}
	var best ReleaseMetadata
	var bestVersion semanticVersion
	var bestAsset Asset
	found := false
	for _, release := range r.PythonReleases {
		version, err := parseSemanticVersion(release.Version)
		if err != nil || !releaseIsStable(release, version) || !pythonInMatrix(version, r.TestedMatrix) || !constraint.matches(version) {
			continue
		}
		asset, ok := matchingAsset(release.Assets, r.Platform, r.Architecture)
		if !ok {
			continue
		}
		if !found || bestVersion.less(version) {
			best, bestVersion, bestAsset, found = release, version, asset, true
		}
	}
	if !found {
		return PythonChoice{}, errors.New("no Strix-compatible Python release is available in the tested matrix")
	}
	return PythonChoice{Version: best.Version, URL: bestAsset.URL, SHA256: bestAsset.SHA256}, nil
}

func ValidateMetadata(data []byte, maxBytes int64) (ReleaseMetadata, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMetadataLimit
	}
	if int64(len(data)) > maxBytes {
		return ReleaseMetadata{}, errors.New("managed runtime metadata exceeds the size limit")
	}
	releases, err := decodeCatalog(data)
	if err != nil {
		return ReleaseMetadata{}, err
	}
	return releases[0], nil
}

var requiredBundleComponents = []ComponentID{
	ComponentUV,
	ComponentPython,
	ComponentStrix,
	ComponentBun,
	ComponentGo,
	ComponentNode,
	ComponentNPM,
	ComponentPNPM,
	ComponentDSH,
	ComponentCodex,
}

var requiredBundlePlatforms = []PlatformTarget{
	{Platform: "linux", Architecture: "amd64"},
	{Platform: "windows", Architecture: "amd64"},
}

// RequiredBundleComponents returns the component set that a full Baron install
// must be able to resolve from one release catalog. The returned slice is a
// copy so callers cannot mutate the resolver's policy accidentally.
func RequiredBundleComponents() []ComponentID {
	return append([]ComponentID(nil), requiredBundleComponents...)
}

// RequiredBundlePlatforms returns the release-artifact platform matrix that a
// full catalog must cover. Runtime resolution still selects one exact target
// for the current host.
func RequiredBundlePlatforms() []PlatformTarget {
	return append([]PlatformTarget(nil), requiredBundlePlatforms...)
}

// ValidateBundleCatalog validates the release catalog used by the full install
// and update paths. Every release must be explicitly scoped to a managed
// component. A prerelease cannot satisfy the stable-component requirement
// unless it is the explicitly reviewed DSH vendor exception described by
// VerifiedPrerelease.
func ValidateBundleCatalog(data []byte, maxBytes int64) ([]ReleaseMetadata, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMetadataLimit
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("managed runtime bundle catalog exceeds the size limit")
	}
	releases, err := decodeCatalog(data)
	if err != nil {
		return nil, err
	}
	required := make(map[ComponentID]struct{}, len(requiredBundleComponents))
	for _, component := range requiredBundleComponents {
		required[component] = struct{}{}
	}
	stable := make(map[ComponentID]map[PlatformTarget]struct{}, len(requiredBundleComponents))
	for _, release := range releases {
		component := ComponentID(strings.TrimSpace(string(release.Component)))
		if component == "" {
			return nil, errors.New("managed runtime bundle catalog release component scope is required")
		}
		if _, ok := required[component]; !ok {
			return nil, fmt.Errorf("managed runtime bundle catalog release component %q is not supported", component)
		}
		if strings.TrimSpace(string(release.InstallMethod)) == "" {
			return nil, fmt.Errorf("managed runtime bundle catalog release %s must declare an install method", component)
		}
		if methodErr := validateInstallMethod(release.InstallMethod); methodErr != nil {
			return nil, methodErr
		}
		if installErr := validateComponentInstallMethod(component, release.InstallMethod); installErr != nil {
			return nil, installErr
		}
		if method := release.InstallMethod; method == InstallMethodNPM || method == InstallMethodUVTool {
			if packageErr := validatePackageCoordinate(release.Package); packageErr != nil {
				return nil, fmt.Errorf("managed runtime bundle catalog release %s package: %w", component, packageErr)
			}
		}
		if strings.TrimSpace(release.EntryPoint) != "" {
			if entryErr := validateEntryPoint(release.EntryPoint); entryErr != nil {
				return nil, fmt.Errorf("managed runtime bundle catalog release %s entry point: %w", component, entryErr)
			}
		}
		version, versionErr := parseSemanticVersion(release.Version)
		if versionErr == nil && releaseIsSelectable(release, version) {
			if stable[component] == nil {
				stable[component] = make(map[PlatformTarget]struct{}, len(requiredBundlePlatforms))
			}
			for _, target := range requiredBundlePlatforms {
				if _, ok := matchingAsset(release.Assets, target.Platform, target.Architecture); ok {
					stable[component][target] = struct{}{}
				}
			}
		}
	}
	for _, component := range requiredBundleComponents {
		for _, target := range requiredBundlePlatforms {
			if _, ok := stable[component][target]; !ok {
				return nil, fmt.Errorf("managed runtime bundle catalog is missing a stable %s/%s asset for component %q", target.Platform, target.Architecture, component)
			}
		}
	}
	return releases, nil
}

func (r Resolver) loadMetadata(ctx context.Context, metadataURL string, offline bool) ([]byte, error) {
	if r.MetadataCache != nil {
		r.MetadataCache.mu.Lock()
		cached := append([]byte(nil), r.MetadataCache.Entries[metadataURL]...)
		r.MetadataCache.mu.Unlock()
		if len(cached) > 0 {
			return cached, nil
		}
	}
	if offline {
		return nil, errors.New("metadata is not cached for offline resolution")
	}
	client := r.Metadata
	if client == nil {
		client = HTTPMetadataClient{HTTP: r.HTTP}
	}
	data, err := client.Get(ctx, metadataURL, defaultMetadataLimit)
	if err != nil {
		return nil, err
	}
	if r.MetadataCache != nil {
		r.MetadataCache.mu.Lock()
		if r.MetadataCache.Entries == nil {
			r.MetadataCache.Entries = map[string][]byte{}
		}
		r.MetadataCache.Entries[metadataURL] = append([]byte(nil), data...)
		r.MetadataCache.mu.Unlock()
	}
	return data, nil
}

func decodeCatalog(data []byte) ([]ReleaseMetadata, error) {
	var envelope struct {
		Releases []ReleaseMetadata `json:"releases"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.Releases != nil {
		if len(envelope.Releases) == 0 {
			return nil, errors.New("managed runtime metadata contains no releases")
		}
		for _, release := range envelope.Releases {
			if err := validateRelease(release); err != nil {
				return nil, err
			}
		}
		return envelope.Releases, nil
	}
	var release ReleaseMetadata
	if err := json.Unmarshal(data, &release); err != nil {
		return nil, fmt.Errorf("decode managed runtime metadata: %w", err)
	}
	if err := validateRelease(release); err != nil {
		return nil, err
	}
	return []ReleaseMetadata{release}, nil
}

func validateRelease(release ReleaseMetadata) error {
	version, err := parseSemanticVersion(release.Version)
	if err != nil {
		return fmt.Errorf("managed runtime metadata version: %w", err)
	}
	if len(release.Assets) == 0 {
		return errors.New("managed runtime metadata release has no assets")
	}
	if release.VerifiedPrerelease && (release.Component != ComponentDSH || !release.Stable || version.pre == "") {
		return errors.New("managed runtime metadata verified prerelease is allowed only for an explicitly stable-marked DSH prerelease")
	}
	for _, asset := range release.Assets {
		if err := validateAsset(asset); err != nil {
			return err
		}
	}
	_ = version
	return nil
}

func validateAsset(asset Asset) error {
	parsed, err := url.Parse(asset.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("managed runtime asset URL is invalid")
	}
	if len(asset.SHA256) != sha256.Size*2 {
		return errors.New("managed runtime asset sha256 is required")
	}
	if _, err := hex.DecodeString(asset.SHA256); err != nil {
		return errors.New("managed runtime asset sha256 is invalid")
	}
	if strings.TrimSpace(asset.Platform) == "" || strings.TrimSpace(asset.Architecture) == "" {
		return errors.New("managed runtime asset platform and architecture are required")
	}
	if asset.Size < 0 || asset.Size > maxAssetBytes {
		return errors.New("managed runtime asset size is outside the allowed limit")
	}
	return nil
}

func selectRelease(releases []ReleaseMetadata, component ComponentID, platform, architecture string) (ReleaseMetadata, Asset, error) {
	var best ReleaseMetadata
	var bestVersion semanticVersion
	var bestAsset Asset
	found := false
	for _, release := range releases {
		if release.Component != "" && !strings.EqualFold(string(release.Component), string(component)) {
			continue
		}
		version, err := parseSemanticVersion(release.Version)
		if err != nil || !releaseIsSelectable(release, version) {
			continue
		}
		asset, ok := matchingAsset(release.Assets, platform, architecture)
		if !ok {
			continue
		}
		if !found || bestVersion.less(version) {
			best, bestVersion, bestAsset, found = release, version, asset, true
		}
	}
	if !found {
		return ReleaseMetadata{}, Asset{}, fmt.Errorf("no stable %s asset matches %s/%s", component, platform, architecture)
	}
	return best, bestAsset, nil
}

func selectPythonRelease(releases []ReleaseMetadata, platform, architecture string, matrix CompatibilityMatrix, constraints pythonConstraints) (ReleaseMetadata, Asset, error) {
	var best ReleaseMetadata
	var bestVersion semanticVersion
	var bestAsset Asset
	found := false
	for _, release := range releases {
		if release.Component != "" && !strings.EqualFold(string(release.Component), string(ComponentPython)) {
			continue
		}
		version, err := parseSemanticVersion(release.Version)
		if err != nil || !releaseIsStable(release, version) || !pythonInMatrix(version, matrix) || !constraints.matches(version) {
			continue
		}
		asset, ok := matchingAsset(release.Assets, platform, architecture)
		if !ok {
			continue
		}
		if !found || bestVersion.less(version) {
			best, bestVersion, bestAsset, found = release, version, asset, true
		}
	}
	if !found {
		return ReleaseMetadata{}, Asset{}, fmt.Errorf("no stable Python asset matches %s/%s and the tested Strix compatibility constraints", platform, architecture)
	}
	return best, bestAsset, nil
}

func matchingAsset(assets []Asset, platform, architecture string) (Asset, bool) {
	for _, asset := range assets {
		if strings.EqualFold(strings.TrimSpace(asset.Platform), strings.TrimSpace(platform)) && strings.EqualFold(strings.TrimSpace(asset.Architecture), strings.TrimSpace(architecture)) {
			return asset, true
		}
	}
	return Asset{}, false
}

func componentDependencies(component ComponentID) []ComponentID {
	switch component {
	case ComponentPython:
		return []ComponentID{ComponentUV}
	case ComponentStrix:
		return []ComponentID{ComponentPython, ComponentUV}
	case ComponentPNPM:
		return []ComponentID{ComponentNode}
	case ComponentDSH, ComponentCodex:
		return []ComponentID{ComponentPNPM}
	case ComponentNPM:
		return []ComponentID{ComponentNode}
	default:
		return nil
	}
}

func planID(plan ResolutionPlan) string {
	type content struct {
		Platform      string          `json:"platform"`
		Architecture  string          `json:"architecture"`
		Compatibility string          `json:"compatibility"`
		Components    []ComponentPlan `json:"components"`
	}
	data, _ := json.Marshal(content{Platform: plan.Platform, Architecture: plan.Architecture, Compatibility: plan.CompatibilityVersion, Components: plan.Components})
	sum := sha256.Sum256(data)
	return "plan-" + hex.EncodeToString(sum[:12])
}

type semanticVersion struct {
	major, minor, patch int
	pre                 string
}

func parseSemanticVersion(raw string) (semanticVersion, error) {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "v")
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return semanticVersion{}, errors.New("version is malformed")
	}
	base := value
	pre := ""
	if index := strings.IndexByte(value, '-'); index >= 0 {
		base, pre = value[:index], value[index+1:]
		if pre == "" {
			return semanticVersion{}, errors.New("version is malformed")
		}
	}
	if index := strings.IndexByte(base, '+'); index >= 0 {
		base = base[:index]
	}
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return semanticVersion{}, errors.New("version must contain major, minor, and patch")
	}
	values := [3]int{}
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return semanticVersion{}, errors.New("version is malformed")
		}
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return semanticVersion{}, errors.New("version is malformed")
		}
		values[index] = parsed
	}
	return semanticVersion{major: values[0], minor: values[1], patch: values[2], pre: pre}, nil
}

func (v semanticVersion) less(other semanticVersion) bool {
	if v.major != other.major {
		return v.major < other.major
	}
	if v.minor != other.minor {
		return v.minor < other.minor
	}
	if v.patch != other.patch {
		return v.patch < other.patch
	}
	if v.pre == "" {
		return false
	}
	return other.pre == ""
}

func releaseIsStable(release ReleaseMetadata, version semanticVersion) bool {
	// Catalog metadata is advisory; both an explicit stable marker and a
	// non-prerelease semantic version are required for production selection.
	return release.Stable && version.pre == ""
}

func releaseIsSelectable(release ReleaseMetadata, version semanticVersion) bool {
	if releaseIsStable(release, version) {
		return true
	}
	// DSH currently publishes its CLI through a vendor RC channel. Keep that
	// exception narrow and explicit rather than treating arbitrary prereleases
	// as production-ready managed runtimes.
	return release.Component == ComponentDSH && release.VerifiedPrerelease && release.Stable && version.pre != ""
}

func pythonInMatrix(version semanticVersion, matrix CompatibilityMatrix) bool {
	if matrix.MinPythonMajor != 0 {
		if version.major < matrix.MinPythonMajor || (version.major == matrix.MinPythonMajor && version.minor < matrix.MinPythonMinor) {
			return false
		}
	}
	if matrix.MaxPythonMajor != 0 {
		if version.major > matrix.MaxPythonMajor || (version.major == matrix.MaxPythonMajor && version.minor > matrix.MaxPythonMinor) {
			return false
		}
	}
	return true
}

type pythonConstraint struct {
	operator string
	version  semanticVersion
}

type pythonConstraints []pythonConstraint

func parsePythonConstraint(raw string) (pythonConstraints, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("Strix requires-python declaration is required")
	}
	var constraints pythonConstraints
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		operator := "=="
		for _, candidate := range []string{">=", "<=", ">", "<", "=="} {
			if strings.HasPrefix(part, candidate) {
				operator = candidate
				part = strings.TrimSpace(strings.TrimPrefix(part, candidate))
				break
			}
		}
		if strings.Count(part, ".") == 1 {
			part += ".0"
		}
		version, err := parseSemanticVersion(part)
		if err != nil {
			return nil, fmt.Errorf("parse Strix requires-python: %w", err)
		}
		constraints = append(constraints, pythonConstraint{operator: operator, version: version})
	}
	return constraints, nil
}

func (constraints pythonConstraints) matches(version semanticVersion) bool {
	for _, constraint := range constraints {
		compareLess := version.less(constraint.version)
		compareGreater := constraint.version.less(version)
		switch constraint.operator {
		case ">=":
			if compareLess {
				return false
			}
		case ">":
			if !compareGreater {
				return false
			}
		case "<=":
			if compareGreater {
				return false
			}
		case "<":
			if !compareLess {
				return false
			}
		case "==":
			if compareLess || compareGreater {
				return false
			}
		}
	}
	return true
}
