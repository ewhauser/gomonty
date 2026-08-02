package runtimebundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const receiptName = "runtime.json"

type receipt struct {
	Schema         int    `json:"schema"`
	RuntimeVersion string `json:"runtime_version"`
	Target         string `json:"target"`
	ManifestSHA256 string `json:"manifest_sha256"`
	SourceSHA256   string `json:"native_source_sha256"`
	Origin         string `json:"origin"`
	InstalledAt    string `json:"installed_at"`
	Files          []File `json:"files"`
}

// Result identifies an installed runtime that was verified against the
// embedded manifest in this process.
type Result struct {
	RuntimeVersion string `json:"runtime_version"`
	Target         string `json:"target"`
	Origin         string `json:"origin"`
	Directory      string `json:"directory"`
	LibraryPath    string `json:"library_path"`
	WorkerPath     string `json:"worker_path"`
}

// DefaultCacheRoot returns the cache root used by both Prepare and the loader.
// GOMONTY_CACHE_DIR is an explicit location override; it does not weaken hash
// verification.
func DefaultCacheRoot() (string, error) {
	if root := os.Getenv("GOMONTY_CACHE_DIR"); root != "" {
		return filepath.Abs(root)
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}
	if root == "" {
		return "", errors.New("resolve user cache directory: empty path")
	}
	return filepath.Join(root, "gomonty"), nil
}

// Locate verifies and returns the current target's prepared runtime. No
// network, build tool, shared-library loader, or child process is invoked.
func Locate() (Result, error) {
	manifest, err := CurrentManifest()
	if err != nil {
		return Result{}, err
	}
	target, err := manifest.CurrentTarget()
	if err != nil {
		return Result{}, err
	}
	root, err := DefaultCacheRoot()
	if err != nil {
		return Result{}, err
	}
	return verifyInstalled(runtimeDirectory(root, manifest, target), manifest, target)
}

func runtimeDirectory(root string, manifest Manifest, target Target) string {
	digest := manifest.Digest()
	if len(digest) > 16 {
		digest = digest[:16]
	}
	return filepath.Join(root, "runtimes", manifest.RuntimeVersion, target.ID, digest)
}

func verifyInstalled(directory string, manifest Manifest, target Target) (Result, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{}, fmt.Errorf("%w: run `gomonty prepare download` or call monty.Prepare", ErrNotPrepared)
		}
		return Result{}, fmt.Errorf("inspect prepared runtime directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Result{}, fmt.Errorf("%w: runtime path is not a regular directory", ErrIntegrity)
	}

	receiptPath := filepath.Join(directory, receiptName)
	receiptInfo, err := os.Lstat(receiptPath)
	if err != nil || !receiptInfo.Mode().IsRegular() || receiptInfo.Mode()&os.ModeSymlink != 0 {
		return Result{}, fmt.Errorf("%w: runtime receipt is not a regular file", ErrIntegrity)
	}
	receiptBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		return Result{}, fmt.Errorf("%w: read runtime receipt: %v", ErrIntegrity, err)
	}
	var installed receipt
	decoder := json.NewDecoder(strings.NewReader(string(receiptBytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&installed); err != nil {
		return Result{}, fmt.Errorf("%w: decode runtime receipt: %v", ErrIntegrity, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Result{}, fmt.Errorf("%w: runtime receipt contains trailing data", ErrIntegrity)
	}
	if installed.Schema != manifestSchema || installed.RuntimeVersion != manifest.RuntimeVersion ||
		installed.Target != target.ID || installed.ManifestSHA256 != manifest.Digest() ||
		installed.SourceSHA256 != manifest.SourceSHA256 ||
		(installed.Origin != string(ModeDownload) && installed.Origin != string(ModeBuild)) {
		return Result{}, fmt.Errorf("%w: runtime receipt does not match the embedded manifest", ErrIntegrity)
	}
	if _, err := time.Parse(time.RFC3339Nano, installed.InstalledAt); err != nil {
		return Result{}, fmt.Errorf("%w: runtime receipt has an invalid installation time", ErrIntegrity)
	}
	expectedFiles, err := receiptFiles(installed, target)
	if err != nil {
		return Result{}, err
	}

	allowed := map[string]struct{}{receiptName: {}}
	for _, file := range expectedFiles {
		allowed[file.Name] = struct{}{}
		if err := verifyFile(filepath.Join(directory, file.Name), file); err != nil {
			return Result{}, err
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return Result{}, fmt.Errorf("%w: list runtime directory: %v", ErrIntegrity, err)
	}
	for _, entry := range entries {
		if _, ok := allowed[entry.Name()]; !ok {
			return Result{}, fmt.Errorf("%w: unexpected file %q in runtime directory", ErrIntegrity, entry.Name())
		}
	}

	library, _ := target.File("library")
	worker, _ := target.File("worker")
	return Result{
		RuntimeVersion: manifest.RuntimeVersion,
		Target:         target.ID,
		Origin:         installed.Origin,
		Directory:      directory,
		LibraryPath:    filepath.Join(directory, library.Name),
		WorkerPath:     filepath.Join(directory, worker.Name),
	}, nil
}

func receiptFiles(installed receipt, target Target) ([]File, error) {
	if installed.Origin == string(ModeDownload) {
		if !equalFiles(installed.Files, target.Files) {
			return nil, fmt.Errorf("%w: downloaded runtime receipt does not match release hashes", ErrIntegrity)
		}
		return target.Files, nil
	}
	if len(installed.Files) != len(target.Files) {
		return nil, fmt.Errorf("%w: built runtime receipt has an invalid file count", ErrIntegrity)
	}
	byRole := make(map[string]File, len(installed.Files))
	for _, file := range installed.Files {
		if !validHex(file.SHA256, sha256.Size*2) || file.Size <= 0 || file.Size > maxArchiveSize {
			return nil, fmt.Errorf("%w: built runtime receipt has invalid file metadata", ErrIntegrity)
		}
		if _, exists := byRole[file.Role]; exists {
			return nil, fmt.Errorf("%w: built runtime receipt has duplicate role %q", ErrIntegrity, file.Role)
		}
		byRole[file.Role] = file
	}
	for _, expected := range target.Files {
		actual, ok := byRole[expected.Role]
		if !ok || actual.Name != expected.Name || actual.Executable != expected.Executable {
			return nil, fmt.Errorf("%w: built runtime receipt changed the %s file identity", ErrIntegrity, expected.Role)
		}
	}
	return installed.Files, nil
}

func equalFiles(left, right []File) bool {
	if len(left) != len(right) {
		return false
	}
	leftByRole := make(map[string]File, len(left))
	for _, file := range left {
		leftByRole[file.Role] = file
	}
	for _, file := range right {
		if leftByRole[file.Role] != file {
			return false
		}
	}
	return true
}

func verifyPayload(directory string, target Target) error {
	allowed := make(map[string]struct{}, len(target.Files))
	for _, file := range target.Files {
		allowed[file.Name] = struct{}{}
		if err := verifyFile(filepath.Join(directory, file.Name), file); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("%w: list staged runtime: %v", ErrIntegrity, err)
	}
	for _, entry := range entries {
		if _, ok := allowed[entry.Name()]; !ok {
			return fmt.Errorf("%w: unexpected staged file %q", ErrIntegrity, entry.Name())
		}
	}
	return nil
}

func inspectBuiltPayload(directory string, target Target) ([]File, error) {
	allowed := make(map[string]struct{}, len(target.Files))
	files := make([]File, 0, len(target.Files))
	for _, identity := range target.Files {
		allowed[identity.Name] = struct{}{}
		path := filepath.Join(directory, identity.Name)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect built runtime file %s: %w", identity.Name, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxArchiveSize {
			return nil, fmt.Errorf("%w: built runtime file %s has an invalid type or size", ErrIntegrity, identity.Name)
		}
		digest, err := hashFile(path)
		if err != nil {
			return nil, err
		}
		file := identity
		file.Size = info.Size()
		file.SHA256 = digest
		if runtime.GOOS != "windows" && file.Executable && info.Mode().Perm()&0o111 == 0 {
			return nil, fmt.Errorf("%w: built runtime file %s is not executable", ErrIntegrity, identity.Name)
		}
		files = append(files, file)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if _, ok := allowed[entry.Name()]; !ok {
			return nil, fmt.Errorf("%w: unexpected built runtime file %q", ErrIntegrity, entry.Name())
		}
	}
	return files, nil
}

func verifyFile(path string, expected File) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: inspect %s: %v", ErrIntegrity, expected.Name, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is not a regular file", ErrIntegrity, expected.Name)
	}
	if info.Size() != expected.Size {
		return fmt.Errorf("%w: %s size is %d, expected %d", ErrIntegrity, expected.Name, info.Size(), expected.Size)
	}
	digest, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("%w: hash %s: %v", ErrIntegrity, expected.Name, err)
	}
	if digest != expected.SHA256 {
		return fmt.Errorf("%w: %s SHA-256 is %s, expected %s", ErrIntegrity, expected.Name, digest, expected.SHA256)
	}
	if runtime.GOOS != "windows" && expected.Executable && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%w: %s is not executable", ErrIntegrity, expected.Name)
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func writeReceipt(directory string, manifest Manifest, target Target, mode Mode, files []File, now time.Time) error {
	value := receipt{
		Schema:         manifestSchema,
		RuntimeVersion: manifest.RuntimeVersion,
		Target:         target.ID,
		ManifestSHA256: manifest.Digest(),
		SourceSHA256:   manifest.SourceSHA256,
		Origin:         string(mode),
		InstalledAt:    now.UTC().Format(time.RFC3339Nano),
		Files:          append([]File(nil), files...),
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime receipt: %w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(directory, receiptName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create runtime receipt: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write runtime receipt: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync runtime receipt: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close runtime receipt: %w", err)
	}
	return syncDirectory(directory)
}

func installStaging(staging, final string) error {
	parent := filepath.Dir(final)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create runtime cache parent: %w", err)
	}
	backup := final + ".replaced"
	_ = os.RemoveAll(backup)
	if _, err := os.Lstat(final); err == nil {
		if err := os.Rename(final, backup); err != nil {
			return fmt.Errorf("quarantine old runtime cache: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect old runtime cache: %w", err)
	}
	if err := os.Rename(staging, final); err != nil {
		if _, backupErr := os.Lstat(backup); backupErr == nil {
			_ = os.Rename(backup, final)
		}
		return fmt.Errorf("install verified runtime: %w", err)
	}
	if err := syncDirectory(parent); err != nil {
		return err
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove replaced runtime cache: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil && runtime.GOOS != "windows" {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}
