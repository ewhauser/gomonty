//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64)

package monty

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/ewhauser/gomonty/internal/runtimebundle"
)

const preparedReleaseHelper = "GOMONTY_TEST_PREPARED_RELEASE_HELPER"

func TestPreparedReleaseAssetEndToEnd(t *testing.T) {
	assetRoot := os.Getenv("GOMONTY_TEST_RELEASE_ASSET_DIR")
	if assetRoot == "" {
		t.Skip("set GOMONTY_TEST_RELEASE_ASSET_DIR to a generated release asset directory")
	}
	if runtime.GOOS == "windows" && os.Getenv(preparedReleaseHelper) == "" {
		cacheDir := t.TempDir()
		command := exec.Command(os.Args[0], "-test.run=^TestPreparedReleaseAssetEndToEnd$")
		command.Env = append(os.Environ(),
			preparedReleaseHelper+"=1",
			"GOMONTY_CACHE_DIR="+cacheDir,
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("release asset helper: %v\n%s", err, output)
		}
		return
	}
	cacheDir := os.Getenv("GOMONTY_CACHE_DIR")
	if cacheDir == "" {
		cacheDir = t.TempDir()
	}
	t.Setenv("GOMONTY_CACHE_DIR", cacheDir)

	manifest, err := runtimebundle.CurrentManifest()
	if err != nil {
		t.Fatal(err)
	}
	target, err := manifest.CurrentTarget()
	if err != nil {
		t.Fatal(err)
	}
	assetPath := filepath.Join(assetRoot, target.Asset)
	assetInfo, err := os.Stat(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path != "/"+target.Asset {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Length", fmt.Sprint(assetInfo.Size()))
		http.ServeFile(response, request, assetPath)
	}))
	defer server.Close()

	if _, err := New("40 + 2", CompileOptions{}); !errors.Is(err, ErrRuntimeNotPrepared) {
		t.Fatalf("New before preparation error = %v, want ErrRuntimeNotPrepared", err)
	}
	prepared, err := Prepare(context.Background(), PrepareOptions{
		Mode:           PrepareDownload,
		ReleaseBaseURL: server.URL,
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatalf("PrepareDownload: %v", err)
	}
	if prepared.Mode != PrepareDownload || requests.Load() != 1 {
		t.Fatalf("prepared=%+v requests=%d", prepared, requests.Load())
	}
	preparedAgain, err := Prepare(context.Background(), PrepareOptions{
		Mode:           PrepareDownload,
		ReleaseBaseURL: server.URL,
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatalf("second PrepareDownload: %v", err)
	}
	if preparedAgain != prepared || requests.Load() != 1 {
		t.Fatalf("second prepared=%+v requests=%d", preparedAgain, requests.Load())
	}
	assertPreparedWorker(t)
}

func TestPublishedReleaseEndToEnd(t *testing.T) {
	if os.Getenv("GOMONTY_TEST_PUBLIC_RELEASE") == "" {
		t.Skip("set GOMONTY_TEST_PUBLIC_RELEASE=1 after publishing the immutable runtime release")
	}
	cacheDir := os.Getenv("GOMONTY_CACHE_DIR")
	if cacheDir == "" {
		if runtime.GOOS != "windows" {
			cacheDir = t.TempDir()
		} else {
			var err error
			cacheDir, err = os.MkdirTemp("", "gomonty-public-release-*")
			if err != nil {
				t.Fatalf("create public release cache: %v", err)
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(cacheDir); err != nil {
					// The process-wide worker pool keeps its executable open on
					// Windows until this test process exits. The surrounding runner
					// owns and cleans its temporary directory after process exit.
					t.Logf("defer public release cache cleanup until process exit: %v", err)
				}
			})
		}
	}
	t.Setenv("GOMONTY_CACHE_DIR", cacheDir)
	if _, err := New("40 + 2", CompileOptions{}); !errors.Is(err, ErrRuntimeNotPrepared) {
		t.Fatalf("New before preparation error = %v, want ErrRuntimeNotPrepared", err)
	}
	prepared, err := Prepare(context.Background(), PrepareOptions{Mode: PrepareDownload})
	if err != nil {
		t.Fatalf("PrepareDownload public release: %v", err)
	}
	if prepared.Mode != PrepareDownload || prepared.RustToolchain != "1.95.0" {
		t.Fatalf("prepared public runtime = %+v", prepared)
	}
	assertPreparedWorker(t)
}

func assertPreparedWorker(t *testing.T) {
	t.Helper()
	repl, err := NewRepl(ReplOptions{ScriptName: "release-asset.py"})
	if err != nil {
		t.Fatalf("NewRepl: %v", err)
	}
	defer repl.Close()
	workerPID, ok := repl.WorkerPID()
	if !ok || workerPID == 0 || workerPID == os.Getpid() {
		t.Fatalf("WorkerPID() = %d, %v; host PID = %d", workerPID, ok, os.Getpid())
	}
	value, err := repl.FeedRun(context.Background(), "40 + 2", FeedOptions{})
	if err != nil {
		t.Fatalf("FeedRun: %v", err)
	}
	if got := value.Raw(); got != int64(42) {
		t.Fatalf("FeedRun value = %#v, want 42", got)
	}
}
