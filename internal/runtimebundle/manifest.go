package runtimebundle

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	manifestSchema = 1
	maxArchiveSize = 256 << 20
)

var (
	// ErrNotPrepared means the verified native runtime is not present in the
	// local cache. Preparation is always an explicit operation.
	ErrNotPrepared = errors.New("gomonty native runtime is not prepared")
	// ErrIntegrity means bytes on disk or in a downloaded bundle do not match
	// the hashes committed with this Go module.
	ErrIntegrity = errors.New("gomonty native runtime integrity check failed")
	// ErrUnsupported means there is no runtime manifest for the current target.
	ErrUnsupported = errors.New("gomonty native runtime target is unsupported")
	// ErrBuildPrerequisite means local build preparation cannot prove that the
	// reviewed compiler and target are available.
	ErrBuildPrerequisite = errors.New("gomonty native runtime build prerequisite is not satisfied")
)

//go:embed manifests/current.json
var manifestFS embed.FS

// Manifest is the committed trust root for one native runtime release.
//
// The JSON representation is intentionally public to the repository tooling,
// but the Go type remains internal so consumers cannot replace the manifest at
// runtime.
type Manifest struct {
	Schema         int      `json:"schema"`
	RuntimeVersion string   `json:"runtime_version"`
	ReleaseTag     string   `json:"release_tag"`
	ReleaseBaseURL string   `json:"release_base_url"`
	MontyVersion   string   `json:"monty_version"`
	MontyCommit    string   `json:"monty_commit"`
	RustToolchain  string   `json:"rust_toolchain"`
	SourceSHA256   string   `json:"native_source_sha256"`
	Targets        []Target `json:"targets"`

	digest string
}

// Target describes one release asset and its exact installed contents.
type Target struct {
	ID            string `json:"id"`
	GOOS          string `json:"goos"`
	GOARCH        string `json:"goarch"`
	Variant       string `json:"variant,omitempty"`
	RustTarget    string `json:"rust_target"`
	Asset         string `json:"asset"`
	ArchiveSHA256 string `json:"archive_sha256"`
	ArchiveSize   int64  `json:"archive_size"`
	Files         []File `json:"files"`
}

// File describes an exact file installed from a runtime release asset.
type File struct {
	Role       string `json:"role"`
	Name       string `json:"name"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable"`
}

// CurrentManifest loads and validates the immutable manifest embedded in the
// package. Only text metadata, never native code, is embedded in the Go module.
func CurrentManifest() (Manifest, error) {
	raw, err := manifestFS.ReadFile("manifests/current.json")
	if err != nil {
		return Manifest{}, fmt.Errorf("read embedded runtime manifest: %w", err)
	}
	manifest, err := ParseManifest(raw)
	if err != nil {
		return Manifest{}, err
	}
	manifest.digest = digestBytes(raw)
	return manifest, nil
}

// ParseManifest decodes and strictly validates a manifest. It is exported only
// for the repository's release tooling and tests.
func ParseManifest(raw []byte) (Manifest, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode runtime manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Manifest{}, errors.New("decode runtime manifest: trailing JSON value")
		}
		return Manifest{}, fmt.Errorf("decode runtime manifest trailer: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	manifest.digest = digestBytes(raw)
	return manifest, nil
}

// Validate rejects ambiguous or incomplete manifests rather than guessing at
// an installation layout.
func (m Manifest) Validate() error {
	if m.Schema != manifestSchema {
		return fmt.Errorf("runtime manifest schema %d is unsupported", m.Schema)
	}
	if !safeToken(m.RuntimeVersion) || !safeToken(m.ReleaseTag) {
		return errors.New("runtime manifest has an invalid runtime version or release tag")
	}
	releaseURL, err := url.Parse(m.ReleaseBaseURL)
	if err != nil || releaseURL.Scheme != "https" || releaseURL.Host == "" || releaseURL.User != nil || releaseURL.RawQuery != "" || releaseURL.Fragment != "" {
		return errors.New("runtime manifest release_base_url must use https")
	}
	if !strings.HasSuffix(strings.TrimRight(releaseURL.Path, "/"), "/"+m.ReleaseTag) {
		return errors.New("runtime manifest release_base_url must end with the exact release tag")
	}
	if !safeVersion(m.MontyVersion) || !validHex(m.MontyCommit, 40) {
		return errors.New("runtime manifest has an invalid Monty version or commit")
	}
	if !stableToolchainVersion(m.RustToolchain) {
		return errors.New("runtime manifest has an invalid Rust toolchain version")
	}
	if !validHex(m.SourceSHA256, sha256.Size*2) {
		return errors.New("runtime manifest has an invalid native source SHA-256")
	}
	if len(m.Targets) == 0 {
		return errors.New("runtime manifest has no targets")
	}

	seenTargets := make(map[string]struct{}, len(m.Targets))
	seenAssets := make(map[string]struct{}, len(m.Targets))
	for _, target := range m.Targets {
		if !safeToken(target.ID) || !safeToken(target.GOOS) || !safeToken(target.GOARCH) ||
			(target.Variant != "" && !safeToken(target.Variant)) || !safeToken(target.RustTarget) {
			return fmt.Errorf("runtime manifest target %q has invalid identity fields", target.ID)
		}
		key := target.GOOS + "/" + target.GOARCH + "/" + target.Variant
		if _, exists := seenTargets[key]; exists {
			return fmt.Errorf("runtime manifest contains duplicate target %s", key)
		}
		seenTargets[key] = struct{}{}
		if filepath.Base(target.Asset) != target.Asset || !safeToken(target.Asset) || !strings.HasSuffix(target.Asset, ".zip") {
			return fmt.Errorf("runtime manifest target %s has unsafe asset name", target.ID)
		}
		if _, exists := seenAssets[target.Asset]; exists {
			return fmt.Errorf("runtime manifest contains duplicate asset %s", target.Asset)
		}
		seenAssets[target.Asset] = struct{}{}
		if !validHex(target.ArchiveSHA256, sha256.Size*2) || target.ArchiveSize <= 0 || target.ArchiveSize > maxArchiveSize {
			return fmt.Errorf("runtime manifest target %s has invalid archive metadata", target.ID)
		}
		if err := validateFiles(target); err != nil {
			return err
		}
	}
	return nil
}

func validateFiles(target Target) error {
	if len(target.Files) != 2 {
		return fmt.Errorf("runtime manifest target %s must contain exactly a library and worker", target.ID)
	}
	roles := map[string]bool{"library": false, "worker": false}
	names := make(map[string]struct{}, len(target.Files))
	for _, file := range target.Files {
		if _, ok := roles[file.Role]; !ok || roles[file.Role] {
			return fmt.Errorf("runtime manifest target %s has an invalid or duplicate role %q", target.ID, file.Role)
		}
		roles[file.Role] = true
		if filepath.Base(file.Name) != file.Name || !safeToken(file.Name) {
			return fmt.Errorf("runtime manifest target %s has unsafe file name %q", target.ID, file.Name)
		}
		if _, exists := names[file.Name]; exists {
			return fmt.Errorf("runtime manifest target %s has duplicate file %q", target.ID, file.Name)
		}
		names[file.Name] = struct{}{}
		if !validHex(file.SHA256, sha256.Size*2) || file.Size <= 0 || file.Size > maxArchiveSize {
			return fmt.Errorf("runtime manifest target %s has invalid metadata for %s", target.ID, file.Name)
		}
	}
	return nil
}

// CurrentTarget returns the manifest entry selected for the current process.
// Linux libc is detected from the running system so callers cannot silently
// prepare a GNU runtime on musl, or the reverse, because of a missing build tag.
func (m Manifest) CurrentTarget() (Target, error) {
	variant, err := currentRuntimeVariant()
	if err != nil {
		return Target{}, err
	}
	return m.TargetFor(runtime.GOOS, runtime.GOARCH, variant)
}

// TargetFor resolves an exact platform tuple.
func (m Manifest) TargetFor(goos, goarch, variant string) (Target, error) {
	for _, target := range m.Targets {
		if target.GOOS == goos && target.GOARCH == goarch && target.Variant == variant {
			return target, nil
		}
	}
	return Target{}, fmt.Errorf("%w: %s/%s variant %q", ErrUnsupported, goos, goarch, variant)
}

// Digest returns the SHA-256 of the exact manifest bytes.
func (m Manifest) Digest() string { return m.digest }

// AssetURL returns the immutable release URL for target. A non-empty base URL
// is an explicit mirror override; hashes remain anchored in the manifest.
func (m Manifest) AssetURL(target Target, baseURL string) string {
	if baseURL == "" {
		baseURL = m.ReleaseBaseURL
	}
	return strings.TrimRight(baseURL, "/") + "/" + target.Asset
}

func (t Target) File(role string) (File, bool) {
	for _, file := range t.Files {
		if file.Role == role {
			return file, true
		}
	}
	return File{}, false
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func validHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func safeToken(value string) bool {
	if value == "" || strings.ContainsAny(value, "/\\\x00") || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && !strings.ContainsRune("._-", char) {
			return false
		}
	}
	return true
}

func safeVersion(value string) bool {
	return strings.HasPrefix(value, "v") && safeToken(value)
}

func stableToolchainVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return false
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return false
			}
		}
	}
	return true
}
