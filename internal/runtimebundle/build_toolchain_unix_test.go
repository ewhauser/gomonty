//go:build !windows

package runtimebundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildRejectsWrongRustVersionBeforeCreatingCargoTarget(t *testing.T) {
	manifest, target, root := currentBuildInputs(t)
	bin := t.TempDir()
	writeTool(t, bin, "rustc", `
if [ "$RUSTUP_TOOLCHAIN" != "1.95.0" ]; then exit 91; fi
printf '%s\n' 'rustc 1.94.0 (wrong)' 'release: 1.94.0' 'host: test-host'
`)
	writeTool(t, bin, "cargo", `printf '%s\n' 'cargo 1.95.0 (test)'`)
	t.Setenv("PATH", bin)
	t.Setenv("RUSTUP_TOOLCHAIN", "nightly")
	targetDirectory := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("CARGO_TARGET_DIR", targetDirectory)
	err := buildRuntime(context.Background(), root, manifest, target, t.TempDir())
	if !errors.Is(err, ErrBuildPrerequisite) {
		t.Fatalf("buildRuntime error = %v, want ErrBuildPrerequisite", err)
	}
	if _, err := os.Stat(targetDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Cargo target was created before toolchain verification: %v", err)
	}
}

func TestBuildRejectsMissingRustTargetBeforeCreatingCargoTarget(t *testing.T) {
	manifest, target, root := currentBuildInputs(t)
	bin := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing-target")
	writeTool(t, bin, "rustc", `
if [ "$1" = "--version" ]; then
  printf '%s\n' 'rustc 1.95.0 (test)' 'release: 1.95.0' 'host: test-host'
else
  printf '%s\n' '`+missing+`'
fi
`)
	writeTool(t, bin, "cargo", `printf '%s\n' 'cargo 1.95.0 (test)'`)
	t.Setenv("PATH", bin)
	targetDirectory := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("CARGO_TARGET_DIR", targetDirectory)
	err := buildRuntime(context.Background(), root, manifest, target, t.TempDir())
	if !errors.Is(err, ErrBuildPrerequisite) {
		t.Fatalf("buildRuntime error = %v, want ErrBuildPrerequisite", err)
	}
	if _, err := os.Stat(targetDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Cargo target was created before target verification: %v", err)
	}
}

func currentBuildInputs(t *testing.T) (Manifest, Target, string) {
	t.Helper()
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
	return manifest, target, root
}

func writeTool(t *testing.T, directory, name, body string) {
	t.Helper()
	contents := "#!/bin/sh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}
