package runtimebundle

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func downloadRuntime(ctx context.Context, manifest Manifest, target Target, baseURL string, client HTTPDoer, work, payload string) error {
	assetURL := manifest.AssetURL(target, baseURL)
	parsed, err := url.Parse(assetURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("runtime asset URL must be absolute HTTPS: %q", assetURL)
	}
	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Minute,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many runtime asset redirects")
				}
				if req.URL.Scheme != "https" {
					return errors.New("runtime asset redirect must use HTTPS")
				}
				return nil
			},
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return fmt.Errorf("create runtime download request: %w", err)
	}
	request.Header.Set("User-Agent", "gomonty-prepare/1")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download runtime asset: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download runtime asset: %s", response.Status)
	}
	if response.ContentLength >= 0 && response.ContentLength != target.ArchiveSize {
		return fmt.Errorf("%w: runtime archive Content-Length is %d, expected %d", ErrIntegrity, response.ContentLength, target.ArchiveSize)
	}

	archivePath := filepath.Join(work, target.Asset)
	archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create staged runtime archive: %w", err)
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(archive, hasher), io.LimitReader(response.Body, target.ArchiveSize+1))
	syncErr := archive.Sync()
	closeErr := archive.Close()
	if copyErr != nil {
		return fmt.Errorf("download runtime archive: %w", copyErr)
	}
	if written != target.ArchiveSize {
		return fmt.Errorf("%w: runtime archive size is %d, expected %d", ErrIntegrity, written, target.ArchiveSize)
	}
	if syncErr != nil {
		return fmt.Errorf("sync runtime archive: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close runtime archive: %w", closeErr)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if digest != target.ArchiveSHA256 {
		return fmt.Errorf("%w: runtime archive SHA-256 is %s, expected %s", ErrIntegrity, digest, target.ArchiveSHA256)
	}
	return extractArchive(archivePath, payload, target)
}

func extractArchive(archivePath, payload string, target Target) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("%w: open runtime archive: %v", ErrIntegrity, err)
	}
	defer reader.Close()

	expected := make(map[string]File, len(target.Files))
	for _, file := range target.Files {
		expected[file.Name] = file
	}
	seen := make(map[string]struct{}, len(target.Files))
	for _, entry := range reader.File {
		if entry.Name != filepath.Base(entry.Name) || strings.Contains(entry.Name, "\\") || entry.FileInfo().IsDir() {
			return fmt.Errorf("%w: unsafe archive entry %q", ErrIntegrity, entry.Name)
		}
		if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() {
			return fmt.Errorf("%w: archive entry %q is not a regular file", ErrIntegrity, entry.Name)
		}
		expectedFile, ok := expected[entry.Name]
		if !ok {
			return fmt.Errorf("%w: unexpected archive entry %q", ErrIntegrity, entry.Name)
		}
		if _, duplicate := seen[entry.Name]; duplicate {
			return fmt.Errorf("%w: duplicate archive entry %q", ErrIntegrity, entry.Name)
		}
		seen[entry.Name] = struct{}{}
		if int64(entry.UncompressedSize64) != expectedFile.Size {
			return fmt.Errorf("%w: archive entry %s size is %d, expected %d", ErrIntegrity, entry.Name, entry.UncompressedSize64, expectedFile.Size)
		}
		if err := extractFile(entry, filepath.Join(payload, entry.Name), expectedFile); err != nil {
			return err
		}
	}
	if len(seen) != len(expected) {
		missing := make([]string, 0, len(expected)-len(seen))
		for name := range expected {
			if _, ok := seen[name]; !ok {
				missing = append(missing, name)
			}
		}
		return fmt.Errorf("%w: runtime archive is missing %s", ErrIntegrity, strings.Join(missing, ", "))
	}
	return syncDirectory(payload)
}

func extractFile(entry *zip.File, destination string, expected File) error {
	source, err := entry.Open()
	if err != nil {
		return fmt.Errorf("%w: open archive entry %s: %v", ErrIntegrity, entry.Name, err)
	}
	defer source.Close()
	mode := os.FileMode(0o644)
	if runtime.GOOS != "windows" && expected.Executable {
		mode = 0o755
	}
	destinationFile, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create staged runtime file %s: %w", entry.Name, err)
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(destinationFile, hasher), io.LimitReader(source, expected.Size+1))
	syncErr := destinationFile.Sync()
	closeErr := destinationFile.Close()
	if copyErr != nil {
		return fmt.Errorf("extract runtime file %s: %w", entry.Name, copyErr)
	}
	if written != expected.Size {
		return fmt.Errorf("%w: extracted %s size is %d, expected %d", ErrIntegrity, entry.Name, written, expected.Size)
	}
	if syncErr != nil {
		return fmt.Errorf("sync runtime file %s: %w", entry.Name, syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close runtime file %s: %w", entry.Name, closeErr)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if digest != expected.SHA256 {
		return fmt.Errorf("%w: extracted %s SHA-256 is %s, expected %s", ErrIntegrity, entry.Name, digest, expected.SHA256)
	}
	return nil
}
