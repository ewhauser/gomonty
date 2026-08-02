package monty

import (
	"context"
	"errors"
	"net/http"

	"github.com/ewhauser/gomonty/internal/runtimebundle"
)

// PrepareMode selects how the native runtime is explicitly acquired.
type PrepareMode string

// HTTPDoer is the minimal download transport used by PrepareDownload.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

const (
	// PrepareDownload downloads the exact release asset named in the committed
	// manifest and verifies the archive, shared library, and worker hashes.
	PrepareDownload PrepareMode = "download"
	// PrepareBuild builds the reviewed Rust source locally and accepts the
	// output only after recording hashes that the loader will recheck.
	PrepareBuild PrepareMode = "build"
)

var (
	// ErrRuntimeNotPrepared means no verified runtime exists in the local cache.
	ErrRuntimeNotPrepared = runtimebundle.ErrNotPrepared
	// ErrRuntimeIntegrity means native source, release bytes, a build receipt,
	// or cached runtime bytes failed verification.
	ErrRuntimeIntegrity = runtimebundle.ErrIntegrity
	// ErrRuntimeUnsupported means the current platform has no runtime asset.
	ErrRuntimeUnsupported = runtimebundle.ErrUnsupported
)

// PrepareOptions configures an explicit native-runtime preparation. Normal
// execution never performs either operation automatically.
type PrepareOptions struct {
	Mode PrepareMode

	// SourceDir selects the GoMonty source tree for PrepareBuild. When empty,
	// the source tree containing this package is used, including from a Go
	// module cache.
	SourceDir string

	// ReleaseBaseURL is an optional HTTPS mirror for PrepareDownload. The
	// committed file and archive hashes remain authoritative.
	ReleaseBaseURL string

	// HTTPClient optionally supplies transport policy for PrepareDownload.
	// Redirects and TLS behavior are the caller's responsibility when set.
	HTTPClient HTTPDoer
}

// PreparedRuntime reports the verified installation selected for this process.
type PreparedRuntime struct {
	RuntimeVersion string      `json:"runtime_version"`
	Target         string      `json:"target"`
	Mode           PrepareMode `json:"mode"`
}

// Prepare explicitly acquires, verifies, and atomically caches the current
// native runtime. It does not load the library or execute the worker.
func Prepare(ctx context.Context, options PrepareOptions) (PreparedRuntime, error) {
	result, err := runtimebundle.Prepare(ctx, runtimebundle.Options{
		Mode:       runtimebundle.Mode(options.Mode),
		SourceDir:  options.SourceDir,
		BaseURL:    options.ReleaseBaseURL,
		HTTPClient: options.HTTPClient,
	})
	if err != nil {
		return PreparedRuntime{}, err
	}
	return preparedRuntime(result), nil
}

// Prepared verifies the cached native runtime without downloading, building,
// loading, or starting anything.
func Prepared() (PreparedRuntime, error) {
	result, err := runtimebundle.Locate()
	if err != nil {
		return PreparedRuntime{}, err
	}
	return preparedRuntime(result), nil
}

func preparedRuntime(result runtimebundle.Result) PreparedRuntime {
	return PreparedRuntime{
		RuntimeVersion: result.RuntimeVersion,
		Target:         result.Target,
		Mode:           PrepareMode(result.Origin),
	}
}

// IsRuntimeUnavailable reports errors caused by a missing, corrupt, or
// unsupported native runtime.
func IsRuntimeUnavailable(err error) bool {
	return errors.Is(err, ErrRuntimeNotPrepared) || errors.Is(err, ErrRuntimeIntegrity) || errors.Is(err, ErrRuntimeUnsupported)
}
