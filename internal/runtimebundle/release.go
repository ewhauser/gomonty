package runtimebundle

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

var sourceDirectories = map[string]string{
	"darwin-arm64":     "darwin_arm64",
	"linux-amd64":      "linux_amd64",
	"linux-amd64-musl": "linux_amd64_musl",
	"linux-arm64":      "linux_arm64",
	"linux-arm64-musl": "linux_arm64_musl",
	"windows-amd64":    "windows_amd64",
}

// GenerateReleaseAssets is used only by repository release automation. It
// creates deterministic ZIP files and returns a manifest containing hashes for
// both each archive and both files inside it.
func GenerateReleaseAssets(manifest Manifest, inputRoot, outputRoot string) (Manifest, error) {
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("create release output directory: %w", err)
	}
	for targetIndex := range manifest.Targets {
		target := &manifest.Targets[targetIndex]
		directory, ok := sourceDirectories[target.ID]
		if !ok {
			return Manifest{}, fmt.Errorf("no source directory mapping for target %s", target.ID)
		}
		inputDirectory := filepath.Join(inputRoot, directory)
		for fileIndex := range target.Files {
			file := &target.Files[fileIndex]
			path := filepath.Join(inputDirectory, file.Name)
			info, err := os.Lstat(path)
			if err != nil {
				return Manifest{}, fmt.Errorf("inspect release file %s: %w", path, err)
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return Manifest{}, fmt.Errorf("release file %s is not a regular file", path)
			}
			file.Size = info.Size()
			file.SHA256, err = hashFile(path)
			if err != nil {
				return Manifest{}, fmt.Errorf("hash release file %s: %w", path, err)
			}
		}
		archivePath := filepath.Join(outputRoot, target.Asset)
		if err := writeDeterministicZIP(archivePath, inputDirectory, target.Files); err != nil {
			return Manifest{}, err
		}
		info, err := os.Stat(archivePath)
		if err != nil {
			return Manifest{}, fmt.Errorf("inspect release archive: %w", err)
		}
		target.ArchiveSize = info.Size()
		target.ArchiveSHA256, err = hashFile(archivePath)
		if err != nil {
			return Manifest{}, fmt.Errorf("hash release archive: %w", err)
		}
	}
	manifest.digest = ""
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("validate generated runtime manifest: %w", err)
	}
	return manifest, nil
}

// WriteManifest writes canonical, reviewable manifest JSON.
func WriteManifest(path string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write runtime manifest: %w", err)
	}
	return nil
}

// VerifyReleaseAssets proves that exact release bytes match a manifest.
func VerifyReleaseAssets(manifest Manifest, assetRoot string) error {
	allowed := make(map[string]struct{}, len(manifest.Targets))
	for _, target := range manifest.Targets {
		allowed[target.Asset] = struct{}{}
		path := filepath.Join(assetRoot, target.Asset)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("inspect release asset %s: %w", target.Asset, err)
		}
		if info.Size() != target.ArchiveSize {
			return fmt.Errorf("%w: asset %s size is %d, expected %d", ErrIntegrity, target.Asset, info.Size(), target.ArchiveSize)
		}
		digest, err := hashFile(path)
		if err != nil {
			return err
		}
		if digest != target.ArchiveSHA256 {
			return fmt.Errorf("%w: asset %s SHA-256 is %s, expected %s", ErrIntegrity, target.Asset, digest, target.ArchiveSHA256)
		}
		temp, err := os.MkdirTemp("", "gomonty-release-verify-*")
		if err != nil {
			return err
		}
		err = extractArchive(path, temp, target)
		if err == nil {
			err = verifyPayload(temp, target)
		}
		removeErr := os.RemoveAll(temp)
		if err != nil {
			return fmt.Errorf("verify release asset %s: %w", target.Asset, err)
		}
		if removeErr != nil {
			return fmt.Errorf("remove release verification directory: %w", removeErr)
		}
	}
	entries, err := os.ReadDir(assetRoot)
	if err != nil {
		return fmt.Errorf("list release asset directory: %w", err)
	}
	for _, entry := range entries {
		if _, ok := allowed[entry.Name()]; !ok || entry.IsDir() {
			return fmt.Errorf("%w: unexpected release asset %q", ErrIntegrity, entry.Name())
		}
	}
	return nil
}

// VerifyInputFiles checks platform build outputs against committed per-file
// hashes without creating or executing an archive. It is used by build-only CI
// targets such as musl.
func VerifyInputFiles(manifest Manifest, inputRoot string) error {
	for _, target := range manifest.Targets {
		if err := VerifyInputTarget(manifest, inputRoot, target.ID); err != nil {
			return err
		}
	}
	return nil
}

// VerifyInputTarget checks one platform build output.
func VerifyInputTarget(manifest Manifest, inputRoot, targetID string) error {
	var target Target
	found := false
	for _, candidate := range manifest.Targets {
		if candidate.ID == targetID {
			target = candidate
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("runtime manifest has no target %q", targetID)
	}
	directory, ok := sourceDirectories[target.ID]
	if !ok {
		return fmt.Errorf("no source directory mapping for target %s", target.ID)
	}
	for _, expected := range target.Files {
		path := filepath.Join(inputRoot, directory, expected.Name)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("inspect build output %s: %w", path, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != expected.Size {
			return fmt.Errorf("%w: build output %s has unexpected type or size", ErrIntegrity, path)
		}
		digest, err := hashFile(path)
		if err != nil {
			return err
		}
		if digest != expected.SHA256 {
			return fmt.Errorf("%w: build output %s SHA-256 is %s, expected %s", ErrIntegrity, path, digest, expected.SHA256)
		}
	}
	return nil
}

func writeDeterministicZIP(destination, inputDirectory string, files []File) error {
	temp, err := os.CreateTemp(filepath.Dir(destination), filepath.Base(destination)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create release archive: %w", err)
	}
	tempPath := temp.Name()
	cleanup := func() {
		temp.Close()
		_ = os.Remove(tempPath)
	}
	archive := zip.NewWriter(temp)
	ordered := append([]File(nil), files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	for _, file := range ordered {
		header := &zip.FileHeader{Name: file.Name, Method: zip.Deflate}
		header.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
		if file.Executable {
			header.SetMode(0o755)
		} else {
			header.SetMode(0o644)
		}
		writer, err := archive.CreateHeader(header)
		if err != nil {
			cleanup()
			return fmt.Errorf("create release archive entry %s: %w", file.Name, err)
		}
		source, err := os.Open(filepath.Join(inputDirectory, file.Name))
		if err != nil {
			cleanup()
			return fmt.Errorf("open release file %s: %w", file.Name, err)
		}
		_, copyErr := io.Copy(writer, source)
		closeErr := source.Close()
		if copyErr != nil {
			cleanup()
			return fmt.Errorf("archive release file %s: %w", file.Name, copyErr)
		}
		if closeErr != nil {
			cleanup()
			return fmt.Errorf("close release file %s: %w", file.Name, closeErr)
		}
	}
	if err := archive.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close release archive: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync release archive: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close release archive file: %w", err)
	}
	if err := os.Rename(tempPath, destination); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("install release archive: %w", err)
	}
	if err := os.Chmod(destination, 0o644); err != nil {
		return fmt.Errorf("set release archive permissions: %w", err)
	}
	return nil
}
