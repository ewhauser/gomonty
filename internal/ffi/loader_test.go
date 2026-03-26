//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64)

package ffi

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestExtractEmbeddedLibraryWritesAndReusesCache(t *testing.T) {
	t.Parallel()

	cacheRoot := t.TempDir()
	libraryName := "libmonty_go_ffi.test"
	source := fstest.MapFS{
		"lib/test/" + libraryName: &fstest.MapFile{Data: []byte("native-library")},
	}

	firstPath, err := extractEmbeddedLibrary(source, "lib/test", libraryName, cacheRoot)
	if err != nil {
		t.Fatalf("extractEmbeddedLibrary(first): %v", err)
	}
	firstBytes, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatalf("ReadFile(first): %v", err)
	}
	if got := string(firstBytes); got != "native-library" {
		t.Fatalf("unexpected first extraction contents: got %q", got)
	}

	secondPath, err := extractEmbeddedLibrary(source, "lib/test", libraryName, cacheRoot)
	if err != nil {
		t.Fatalf("extractEmbeddedLibrary(second): %v", err)
	}
	if firstPath != secondPath {
		t.Fatalf("expected cache reuse path %q, got %q", firstPath, secondPath)
	}
}

func TestExtractEmbeddedLibraryRewritesHashMismatch(t *testing.T) {
	t.Parallel()

	cacheRoot := t.TempDir()
	libraryName := "libmonty_go_ffi.test"
	source := fstest.MapFS{
		"lib/test/" + libraryName: &fstest.MapFile{Data: []byte("expected-bytes")},
	}

	cachedPath, err := extractEmbeddedLibrary(source, "lib/test", libraryName, cacheRoot)
	if err != nil {
		t.Fatalf("extractEmbeddedLibrary(initial): %v", err)
	}
	if err := os.WriteFile(cachedPath, []byte("corrupt"), 0o644); err != nil {
		t.Fatalf("WriteFile(corrupt cache): %v", err)
	}

	rewrittenPath, err := extractEmbeddedLibrary(source, "lib/test", libraryName, cacheRoot)
	if err != nil {
		t.Fatalf("extractEmbeddedLibrary(rewrite): %v", err)
	}
	if cachedPath != rewrittenPath {
		t.Fatalf("expected rewritten cache path %q, got %q", cachedPath, rewrittenPath)
	}
	rewrittenBytes, err := os.ReadFile(rewrittenPath)
	if err != nil {
		t.Fatalf("ReadFile(rewritten): %v", err)
	}
	if got := string(rewrittenBytes); got != "expected-bytes" {
		t.Fatalf("unexpected rewritten contents: got %q", got)
	}
}

func TestExtractEmbeddedLibraryMissingLibrary(t *testing.T) {
	t.Parallel()

	_, err := extractEmbeddedLibrary(fstest.MapFS{}, "lib/test", "missing.bin", t.TempDir())
	if err == nil {
		t.Fatal("expected missing embedded library error")
	}
}

func TestLibraryCacheRootHonorsOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "ffi-cache")
	t.Setenv("GOMONTY_FFI_CACHE_DIR", override)
	if got := libraryCacheRoot(); got != override {
		t.Fatalf("libraryCacheRoot() = %q, want %q", got, override)
	}
}
