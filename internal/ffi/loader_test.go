//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64)

package ffi

import (
	"errors"
	"testing"

	"github.com/ewhauser/gomonty/internal/runtimebundle"
)

func TestLocateFailurePreventsNativeLibraryLoad(t *testing.T) {
	t.Parallel()
	want := errors.New("verification failed")
	loaded := false
	_, _, err := locateAndLoadRuntime(
		func() (runtimebundle.Result, error) { return runtimebundle.Result{}, want },
		func(string) (uintptr, error) {
			loaded = true
			return 0, nil
		},
	)
	if !errors.Is(err, want) {
		t.Fatalf("locateAndLoadRuntime error = %v, want %v", err, want)
	}
	if loaded {
		t.Fatal("native library loader ran before runtime verification succeeded")
	}
}

func TestVerifiedPathsArePassedToNativeLibraryLoad(t *testing.T) {
	t.Parallel()
	want := runtimebundle.Result{LibraryPath: "/verified/library", WorkerPath: "/verified/worker"}
	var loadedPath string
	got, handle, err := locateAndLoadRuntime(
		func() (runtimebundle.Result, error) { return want, nil },
		func(path string) (uintptr, error) {
			loadedPath = path
			return 42, nil
		},
	)
	if err != nil {
		t.Fatalf("locateAndLoadRuntime: %v", err)
	}
	if got != want || handle != 42 || loadedPath != want.LibraryPath {
		t.Fatalf("got result=%+v handle=%d loaded=%q", got, handle, loadedPath)
	}
}

func TestUnavailableErrorPreservesIntegrityCause(t *testing.T) {
	t.Parallel()
	err := unavailableError(runtimebundle.ErrIntegrity)
	if !errors.Is(err.Cause(), runtimebundle.ErrIntegrity) {
		t.Fatalf("Cause() = %v, want ErrIntegrity", err.Cause())
	}
}
