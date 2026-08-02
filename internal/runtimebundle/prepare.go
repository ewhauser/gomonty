package runtimebundle

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Mode selects the only two operations allowed to create a prepared runtime.
type Mode string

const (
	ModeDownload Mode = "download"
	ModeBuild    Mode = "build"
)

// Options configures an explicit preparation operation.
type Options struct {
	Mode       Mode
	SourceDir  string
	BaseURL    string
	HTTPClient HTTPDoer

	// CacheRoot is intentionally internal-package surface. The public API uses
	// the same default root as the loader; tests can isolate it here.
	CacheRoot string
}

// HTTPDoer is the transport port used by download preparation.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type preparer struct {
	manifest Manifest
	target   Target
	options  Options
	now      func() time.Time
	build    func(context.Context, string, Manifest, Target, string) error
}

// Prepare downloads or locally builds the current target, verifies every byte,
// and atomically installs it. It never loads the shared library or starts the
// worker.
func Prepare(ctx context.Context, options Options) (Result, error) {
	manifest, err := CurrentManifest()
	if err != nil {
		return Result{}, err
	}
	target, err := manifest.CurrentTarget()
	if err != nil {
		return Result{}, err
	}
	p := preparer{
		manifest: manifest,
		target:   target,
		options:  options,
		now:      time.Now,
		build:    buildRuntime,
	}
	return p.prepare(ctx)
}

func (p preparer) prepare(ctx context.Context) (Result, error) {
	if p.options.Mode != ModeDownload && p.options.Mode != ModeBuild {
		return Result{}, fmt.Errorf("unknown preparation mode %q; use %q or %q", p.options.Mode, ModeDownload, ModeBuild)
	}
	if p.options.Mode == ModeDownload && p.options.SourceDir != "" {
		return Result{}, errors.New("source directory is only valid for build preparation")
	}
	if p.options.Mode == ModeBuild && (p.options.BaseURL != "" || p.options.HTTPClient != nil) {
		return Result{}, errors.New("download URL and HTTP client are only valid for download preparation")
	}
	root := p.options.CacheRoot
	if root == "" {
		var err error
		root, err = DefaultCacheRoot()
		if err != nil {
			return Result{}, err
		}
	} else {
		var err error
		root, err = filepath.Abs(root)
		if err != nil {
			return Result{}, fmt.Errorf("resolve cache root: %w", err)
		}
	}
	final := runtimeDirectory(root, p.manifest, p.target)
	if result, err := verifyInstalled(final, p.manifest, p.target); err == nil && result.Origin == string(p.options.Mode) {
		return result, nil
	}

	lockPath := filepath.Join(root, "locks", p.manifest.RuntimeVersion+"-"+p.target.ID+"-"+p.manifest.Digest()+".lock")
	unlock, err := acquireFileLock(ctx, lockPath)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	if result, err := verifyInstalled(final, p.manifest, p.target); err == nil && result.Origin == string(p.options.Mode) {
		return result, nil
	}

	parent := filepath.Dir(final)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Result{}, fmt.Errorf("create runtime staging parent: %w", err)
	}
	digest := p.manifest.Digest()
	if len(digest) > 16 {
		digest = digest[:16]
	}
	stagingPrefix := "." + digest + ".prepare-"
	if err := cleanupStaging(parent, stagingPrefix); err != nil {
		return Result{}, err
	}
	work, err := os.MkdirTemp(parent, stagingPrefix)
	if err != nil {
		return Result{}, fmt.Errorf("create runtime staging directory: %w", err)
	}
	defer os.RemoveAll(work)
	payload := filepath.Join(work, "payload")
	if err := os.Mkdir(payload, 0o755); err != nil {
		return Result{}, fmt.Errorf("create runtime payload directory: %w", err)
	}

	var installedFiles []File
	switch p.options.Mode {
	case ModeDownload:
		if err := downloadRuntime(ctx, p.manifest, p.target, p.options.BaseURL, p.options.HTTPClient, work, payload); err != nil {
			return Result{}, err
		}
		if err := verifyPayload(payload, p.target); err != nil {
			return Result{}, err
		}
		installedFiles = append([]File(nil), p.target.Files...)
	case ModeBuild:
		if err := p.build(ctx, p.options.SourceDir, p.manifest, p.target, payload); err != nil {
			return Result{}, err
		}
		var err error
		installedFiles, err = inspectBuiltPayload(payload, p.target)
		if err != nil {
			return Result{}, err
		}
	}
	if err := writeReceipt(payload, p.manifest, p.target, p.options.Mode, installedFiles, p.now()); err != nil {
		return Result{}, err
	}
	if err := installStaging(payload, final); err != nil {
		return Result{}, err
	}
	result, err := verifyInstalled(final, p.manifest, p.target)
	if err != nil {
		return Result{}, fmt.Errorf("verify installed runtime: %w", err)
	}
	return result, nil
}

func cleanupStaging(parent, prefix string) error {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return fmt.Errorf("list runtime staging parent: %w", err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		path := filepath.Join(parent, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove interrupted runtime staging directory %s: %w", path, err)
		}
	}
	return nil
}
