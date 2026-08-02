package runtimebundle

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type runtimeFixture struct {
	manifest    Manifest
	target      Target
	asset       []byte
	libraryData []byte
	workerData  []byte
}

func TestCurrentManifestIsValidAndComplete(t *testing.T) {
	t.Parallel()
	manifest, err := CurrentManifest()
	if err != nil {
		t.Fatalf("CurrentManifest: %v", err)
	}
	if len(manifest.Targets) != 6 {
		t.Fatalf("target count = %d, want 6", len(manifest.Targets))
	}
	for _, target := range manifest.Targets {
		if strings.Trim(target.ArchiveSHA256, "0") == "" || target.ArchiveSize <= 1 {
			t.Fatalf("target %s has placeholder archive metadata", target.ID)
		}
	}
}

func TestCurrentManifestMatchesReviewedNativeSource(t *testing.T) {
	t.Parallel()
	manifest, err := CurrentManifest()
	if err != nil {
		t.Fatal(err)
	}
	root, err := packageSourceRoot()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ComputeNativeSourceDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if digest != manifest.SourceSHA256 {
		t.Fatalf("native source SHA-256 = %s, manifest = %s", digest, manifest.SourceSHA256)
	}
}

func TestPrepareRejectsModeSpecificOptionsBeforeWork(t *testing.T) {
	t.Parallel()
	fixture := newRuntimeFixture(t)
	tests := []Options{
		{Mode: ModeDownload, SourceDir: ".", CacheRoot: t.TempDir()},
		{Mode: ModeBuild, BaseURL: "https://example.test", CacheRoot: t.TempDir()},
		{Mode: ModeBuild, HTTPClient: http.DefaultClient, CacheRoot: t.TempDir()},
	}
	for _, options := range tests {
		p := fixture.preparer(options)
		p.build = func(context.Context, string, Manifest, Target, string) error {
			t.Fatal("build ran for invalid options")
			return nil
		}
		if _, err := p.prepare(context.Background()); err == nil {
			t.Fatalf("prepare(%+v) unexpectedly succeeded", options)
		}
	}
}

func TestBuildRefusesSourceMismatchBeforeCreatingCargoTarget(t *testing.T) {
	manifest, err := CurrentManifest()
	if err != nil {
		t.Fatal(err)
	}
	target, err := manifest.CurrentTarget()
	if err != nil {
		t.Fatal(err)
	}
	root, err := packageSourceRoot()
	if err != nil {
		t.Fatal(err)
	}
	manifest.SourceSHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	targetDirectory := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("CARGO_TARGET_DIR", targetDirectory)
	err = buildRuntime(context.Background(), root, manifest, target, t.TempDir())
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("buildRuntime error = %v, want ErrIntegrity", err)
	}
	if _, err := os.Stat(targetDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Cargo target was created before source verification: %v", err)
	}
}

func TestDownloadRequiresHTTPSBeforeTransport(t *testing.T) {
	t.Parallel()
	fixture := newRuntimeFixture(t)
	called := false
	client := httpDoerFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("transport should not run")
	})
	p := fixture.preparer(Options{Mode: ModeDownload, BaseURL: "http://example.test/release", HTTPClient: client, CacheRoot: t.TempDir()})
	if _, err := p.prepare(context.Background()); err == nil {
		t.Fatal("insecure download unexpectedly succeeded")
	}
	if called {
		t.Fatal("transport ran before HTTPS URL validation")
	}
}

func TestPrepareDownloadConcurrentInstallsOnce(t *testing.T) {
	t.Parallel()
	fixture := newRuntimeFixture(t)
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if got, want := request.URL.Path, "/"+fixture.target.Asset; got != want {
			t.Errorf("download path = %q, want %q", got, want)
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(fixture.asset)))
		_, _ = w.Write(fixture.asset)
	}))
	defer server.Close()

	cacheRoot := t.TempDir()
	p := fixture.preparer(Options{
		Mode:       ModeDownload,
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		CacheRoot:  cacheRoot,
	})
	const callers = 12
	results := make(chan Result, callers)
	errorsCh := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := p.prepare(context.Background())
			results <- result
			errorsCh <- err
		}()
	}
	group.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
	}
	var directory string
	for result := range results {
		if directory == "" {
			directory = result.Directory
		}
		if result.Directory != directory || result.Origin != string(ModeDownload) {
			t.Fatalf("unexpected result: %+v", result)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("download requests = %d, want 1", got)
	}
	assertFileData(t, filepath.Join(directory, fixture.target.Files[0].Name), fixture.libraryData)
	assertFileData(t, filepath.Join(directory, fixture.target.Files[1].Name), fixture.workerData)
}

func TestPrepareDownloadRejectsHashMismatchWithoutInstall(t *testing.T) {
	t.Parallel()
	fixture := newRuntimeFixture(t)
	corrupt := append([]byte(nil), fixture.asset...)
	corrupt[len(corrupt)/2] ^= 0xff
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(corrupt)))
		_, _ = w.Write(corrupt)
	}))
	defer server.Close()
	cacheRoot := t.TempDir()
	p := fixture.preparer(Options{Mode: ModeDownload, BaseURL: server.URL, HTTPClient: server.Client(), CacheRoot: cacheRoot})
	_, err := p.prepare(context.Background())
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("prepare error = %v, want ErrIntegrity", err)
	}
	final := runtimeDirectory(cacheRoot, fixture.manifest, fixture.target)
	if _, statErr := os.Stat(final); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("runtime was installed after failed verification: %v", statErr)
	}
}

func TestTamperedCacheFailsClosedAndCanBeRepaired(t *testing.T) {
	t.Parallel()
	fixture := newRuntimeFixture(t)
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Length", fmt.Sprint(len(fixture.asset)))
		_, _ = w.Write(fixture.asset)
	}))
	defer server.Close()
	cacheRoot := t.TempDir()
	p := fixture.preparer(Options{Mode: ModeDownload, BaseURL: server.URL, HTTPClient: server.Client(), CacheRoot: cacheRoot})
	result, err := p.prepare(context.Background())
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if err := os.WriteFile(result.LibraryPath, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyInstalled(result.Directory, fixture.manifest, fixture.target); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("verify tampered cache error = %v, want ErrIntegrity", err)
	}
	repaired, err := p.prepare(context.Background())
	if err != nil {
		t.Fatalf("repair prepare: %v", err)
	}
	assertFileData(t, repaired.LibraryPath, fixture.libraryData)
	if got := requests.Load(); got != 2 {
		t.Fatalf("download requests = %d, want 2", got)
	}
}

func TestSymlinkedReceiptFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires additional Windows privileges")
	}
	fixture := newRuntimeFixture(t)
	cacheRoot := t.TempDir()
	p := fixture.preparer(Options{Mode: ModeBuild, CacheRoot: cacheRoot})
	p.build = func(_ context.Context, _ string, _ Manifest, _ Target, payload string) error {
		writeExecutable(t, filepath.Join(payload, fixture.target.Files[0].Name), fixture.libraryData)
		writeExecutable(t, filepath.Join(payload, fixture.target.Files[1].Name), fixture.workerData)
		return nil
	}
	result, err := p.prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(result.Directory, receiptName)
	receiptCopy := filepath.Join(t.TempDir(), receiptName)
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptCopy, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(receiptPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(receiptCopy, receiptPath); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyInstalled(result.Directory, fixture.manifest, fixture.target); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("symlinked receipt error = %v, want ErrIntegrity", err)
	}
}

func TestFailedRepairPreservesExistingBytes(t *testing.T) {
	t.Parallel()
	fixture := newRuntimeFixture(t)
	serverGood := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(fixture.asset)))
		_, _ = w.Write(fixture.asset)
	}))
	cacheRoot := t.TempDir()
	p := fixture.preparer(Options{Mode: ModeDownload, BaseURL: serverGood.URL, HTTPClient: serverGood.Client(), CacheRoot: cacheRoot})
	result, err := p.prepare(context.Background())
	serverGood.Close()
	if err != nil {
		t.Fatalf("initial prepare: %v", err)
	}
	broken := []byte("known-corruption")
	if err := os.WriteFile(result.LibraryPath, broken, 0o755); err != nil {
		t.Fatal(err)
	}
	serverBad := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "3")
		_, _ = w.Write([]byte("bad"))
	}))
	defer serverBad.Close()
	p.options.BaseURL = serverBad.URL
	p.options.HTTPClient = serverBad.Client()
	if _, err := p.prepare(context.Background()); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("failed repair error = %v, want ErrIntegrity", err)
	}
	assertFileData(t, result.LibraryPath, broken)
}

func TestPrepareBuildRecordsAndRechecksLocalOutput(t *testing.T) {
	t.Parallel()
	fixture := newRuntimeFixture(t)
	cacheRoot := t.TempDir()
	var builds atomic.Int32
	p := fixture.preparer(Options{Mode: ModeBuild, CacheRoot: cacheRoot})
	p.build = func(_ context.Context, _ string, _ Manifest, _ Target, payload string) error {
		builds.Add(1)
		writeExecutable(t, filepath.Join(payload, fixture.target.Files[0].Name), fixture.libraryData)
		writeExecutable(t, filepath.Join(payload, fixture.target.Files[1].Name), fixture.workerData)
		return nil
	}
	first, err := p.prepare(context.Background())
	if err != nil {
		t.Fatalf("prepare build: %v", err)
	}
	second, err := p.prepare(context.Background())
	if err != nil {
		t.Fatalf("reuse build: %v", err)
	}
	if first.Directory != second.Directory || first.Origin != string(ModeBuild) || builds.Load() != 1 {
		t.Fatalf("first=%+v second=%+v builds=%d", first, second, builds.Load())
	}

	badCache := t.TempDir()
	bad := fixture.preparer(Options{Mode: ModeBuild, CacheRoot: badCache})
	bad.build = func(_ context.Context, _ string, _ Manifest, _ Target, payload string) error {
		writeExecutable(t, filepath.Join(payload, fixture.target.Files[0].Name), []byte("not-trusted"))
		writeExecutable(t, filepath.Join(payload, fixture.target.Files[1].Name), fixture.workerData)
		return nil
	}
	locallyBuilt, err := bad.prepare(context.Background())
	if err != nil {
		t.Fatalf("explicit local build: %v", err)
	}
	if locallyBuilt.Origin != string(ModeBuild) {
		t.Fatalf("local build origin = %q", locallyBuilt.Origin)
	}
	if err := os.WriteFile(locallyBuilt.LibraryPath, []byte("tampered-after-build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyInstalled(locallyBuilt.Directory, fixture.manifest, fixture.target); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("tampered local build error = %v, want ErrIntegrity", err)
	}
}

func TestPrepareModeSwitchReplacesThePreviousOrigin(t *testing.T) {
	t.Parallel()
	fixture := newRuntimeFixture(t)
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Length", fmt.Sprint(len(fixture.asset)))
		_, _ = w.Write(fixture.asset)
	}))
	defer server.Close()

	cacheRoot := t.TempDir()
	download := fixture.preparer(Options{
		Mode:       ModeDownload,
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		CacheRoot:  cacheRoot,
	})
	downloaded, err := download.prepare(context.Background())
	if err != nil {
		t.Fatalf("prepare download: %v", err)
	}
	if downloaded.Origin != string(ModeDownload) {
		t.Fatalf("download origin = %q", downloaded.Origin)
	}

	localLibrary := []byte("locally-built-library")
	localWorker := []byte("locally-built-worker")
	var builds atomic.Int32
	build := fixture.preparer(Options{Mode: ModeBuild, CacheRoot: cacheRoot})
	build.build = func(_ context.Context, _ string, _ Manifest, _ Target, payload string) error {
		builds.Add(1)
		writeExecutable(t, filepath.Join(payload, fixture.target.Files[0].Name), localLibrary)
		writeExecutable(t, filepath.Join(payload, fixture.target.Files[1].Name), localWorker)
		return nil
	}
	built, err := build.prepare(context.Background())
	if err != nil {
		t.Fatalf("prepare build after download: %v", err)
	}
	if built.Origin != string(ModeBuild) || builds.Load() != 1 {
		t.Fatalf("built result = %+v, build calls = %d", built, builds.Load())
	}
	assertFileData(t, built.LibraryPath, localLibrary)

	redownloaded, err := download.prepare(context.Background())
	if err != nil {
		t.Fatalf("prepare download after build: %v", err)
	}
	if redownloaded.Origin != string(ModeDownload) || requests.Load() != 2 {
		t.Fatalf("redownloaded result = %+v, requests = %d", redownloaded, requests.Load())
	}
	assertFileData(t, redownloaded.LibraryPath, fixture.libraryData)
}

func TestInterruptedStagingIsRemovedBeforePreparation(t *testing.T) {
	t.Parallel()
	fixture := newRuntimeFixture(t)
	cacheRoot := t.TempDir()
	final := runtimeDirectory(cacheRoot, fixture.manifest, fixture.target)
	parent := filepath.Dir(final)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	digest := fixture.manifest.Digest()[:16]
	stale := filepath.Join(parent, "."+digest+".prepare-crashed")
	if err := os.Mkdir(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "partial"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := fixture.preparer(Options{Mode: ModeBuild, CacheRoot: cacheRoot})
	p.build = func(_ context.Context, _ string, _ Manifest, _ Target, payload string) error {
		writeExecutable(t, filepath.Join(payload, fixture.target.Files[0].Name), fixture.libraryData)
		writeExecutable(t, filepath.Join(payload, fixture.target.Files[1].Name), fixture.workerData)
		return nil
	}
	if _, err := p.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale staging still exists: %v", err)
	}
}

func TestExtractArchiveRejectsAmbiguousFrontiers(t *testing.T) {
	t.Parallel()
	fixture := newRuntimeFixture(t)
	tests := []struct {
		name    string
		entries []zipEntry
	}{
		{"extra", []zipEntry{{fixture.target.Files[0].Name, fixture.libraryData, 0o755}, {fixture.target.Files[1].Name, fixture.workerData, 0o755}, {"extra", []byte("x"), 0o644}}},
		{"traversal", []zipEntry{{"../" + fixture.target.Files[0].Name, fixture.libraryData, 0o755}, {fixture.target.Files[1].Name, fixture.workerData, 0o755}}},
		{"duplicate", []zipEntry{{fixture.target.Files[0].Name, fixture.libraryData, 0o755}, {fixture.target.Files[0].Name, fixture.libraryData, 0o755}, {fixture.target.Files[1].Name, fixture.workerData, 0o755}}},
		{"symlink", []zipEntry{{fixture.target.Files[0].Name, fixture.libraryData, os.ModeSymlink | 0o777}, {fixture.target.Files[1].Name, fixture.workerData, 0o755}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			archive := filepath.Join(t.TempDir(), "bad.zip")
			writeZIP(t, archive, test.entries)
			if err := extractArchive(archive, t.TempDir(), fixture.target); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("extractArchive error = %v, want ErrIntegrity", err)
			}
		})
	}
}

func TestDeterministicReleaseArchiveIsByteStable(t *testing.T) {
	t.Parallel()
	fixture := newRuntimeFixture(t)
	input := t.TempDir()
	writeExecutable(t, filepath.Join(input, fixture.target.Files[0].Name), fixture.libraryData)
	writeExecutable(t, filepath.Join(input, fixture.target.Files[1].Name), fixture.workerData)
	first := filepath.Join(t.TempDir(), "first.zip")
	second := filepath.Join(t.TempDir(), "second.zip")
	if err := writeDeterministicZIP(first, input, fixture.target.Files); err != nil {
		t.Fatal(err)
	}
	if err := writeDeterministicZIP(second, input, fixture.target.Files); err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := os.ReadFile(first)
	secondBytes, _ := os.ReadFile(second)
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("deterministic release archives differ")
	}
}

func TestVerifyReleaseAssetsRejectsUnexpectedFiles(t *testing.T) {
	t.Parallel()
	fixture := newRuntimeFixture(t)
	assets := t.TempDir()
	if err := os.WriteFile(filepath.Join(assets, fixture.target.Asset), fixture.asset, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyReleaseAssets(fixture.manifest, assets); err != nil {
		t.Fatalf("VerifyReleaseAssets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assets, "unreviewed.bin"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyReleaseAssets(fixture.manifest, assets); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("unexpected asset error = %v, want ErrIntegrity", err)
	}
}

func TestInspectInputTargetRecordsLocalHashesWithoutReleaseEquivalence(t *testing.T) {
	t.Parallel()
	fixture := newRuntimeFixture(t)
	inputRoot := t.TempDir()
	inputDirectory := filepath.Join(inputRoot, sourceDirectories[fixture.target.ID])
	if err := os.MkdirAll(inputDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	localLibrary := []byte("local-library-output")
	localWorker := []byte("local-worker-output")
	writeExecutable(t, filepath.Join(inputDirectory, fixture.target.Files[0].Name), localLibrary)
	writeExecutable(t, filepath.Join(inputDirectory, fixture.target.Files[1].Name), localWorker)

	files, err := InspectInputTarget(fixture.manifest, inputRoot, fixture.target.ID)
	if err != nil {
		t.Fatalf("InspectInputTarget: %v", err)
	}
	if files[0].SHA256 != sha256Hex(localLibrary) || files[1].SHA256 != sha256Hex(localWorker) {
		t.Fatalf("local hashes = %+v", files)
	}
	if err := VerifyInputTarget(fixture.manifest, inputRoot, fixture.target.ID); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("VerifyInputTarget error = %v, want ErrIntegrity", err)
	}

	if err := os.WriteFile(filepath.Join(inputDirectory, "unexpected"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectInputTarget(fixture.manifest, inputRoot, fixture.target.ID); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("unexpected-file error = %v, want ErrIntegrity", err)
	}
}

func TestFileLockHonorsCancellation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "runtime.lock")
	unlock, err := acquireFileLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	_, err = acquireFileLock(ctx, path)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock error = %v, want deadline", err)
	}
}

func newRuntimeFixture(t *testing.T) runtimeFixture {
	t.Helper()
	libraryData := []byte("reviewed-library-bytes")
	workerData := []byte("reviewed-worker-bytes")
	target := Target{
		ID:         "darwin-arm64",
		GOOS:       "darwin",
		GOARCH:     "arm64",
		RustTarget: "aarch64-apple-darwin",
		Asset:      "gomonty-test-runtime.zip",
		Files: []File{
			{Role: "library", Name: "libmonty_test.dylib", SHA256: sha256Hex(libraryData), Size: int64(len(libraryData)), Executable: true},
			{Role: "worker", Name: "gomonty-test-worker", SHA256: sha256Hex(workerData), Size: int64(len(workerData)), Executable: true},
		},
	}
	input := t.TempDir()
	writeExecutable(t, filepath.Join(input, target.Files[0].Name), libraryData)
	writeExecutable(t, filepath.Join(input, target.Files[1].Name), workerData)
	archive := filepath.Join(t.TempDir(), target.Asset)
	if err := writeDeterministicZIP(archive, input, target.Files); err != nil {
		t.Fatal(err)
	}
	asset, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	target.ArchiveSHA256 = sha256Hex(asset)
	target.ArchiveSize = int64(len(asset))
	manifest := Manifest{
		Schema:         manifestSchema,
		RuntimeVersion: "test-v1",
		ReleaseTag:     "runtime-test-v1",
		ReleaseBaseURL: "https://example.test/releases/runtime-test-v1",
		MontyVersion:   "v0.0.19",
		MontyCommit:    "e347739909877f4fb03877e23dd092286fc7e659",
		SourceSHA256:   "1111111111111111111111111111111111111111111111111111111111111111",
		Targets:        []Target{target},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return runtimeFixture{manifest: manifest, target: target, asset: asset, libraryData: libraryData, workerData: workerData}
}

func (f runtimeFixture) preparer(options Options) preparer {
	return preparer{manifest: f.manifest, target: f.target, options: options, now: func() time.Time { return time.Unix(1, 0) }, build: buildRuntime}
}

func writeExecutable(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertFileData(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type zipEntry struct {
	name string
	data []byte
	mode os.FileMode
}

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (function httpDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func writeZIP(t *testing.T, path string, entries []zipEntry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		header.SetMode(entry.mode)
		part, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(part, bytes.NewReader(entry.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
