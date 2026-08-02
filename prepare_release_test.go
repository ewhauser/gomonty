//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64)

package monty

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ewhauser/gomonty/internal/runtimebundle"
)

func TestPreparedReleaseAssetEndToEnd(t *testing.T) {
	assetRoot := os.Getenv("GOMONTY_TEST_RELEASE_ASSET_DIR")
	if assetRoot == "" {
		t.Skip("set GOMONTY_TEST_RELEASE_ASSET_DIR to a generated release asset directory")
	}
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

	t.Setenv("GOMONTY_CACHE_DIR", t.TempDir())
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
	t.Setenv("GOMONTY_CACHE_DIR", t.TempDir())
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
