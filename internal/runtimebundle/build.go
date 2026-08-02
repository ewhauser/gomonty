package runtimebundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
)

func buildRuntime(ctx context.Context, sourceDir string, manifest Manifest, target Target, payload string) error {
	if sourceDir == "" {
		var err error
		sourceDir, err = packageSourceRoot()
		if err != nil {
			return err
		}
	}
	sourceDir, err := filepath.Abs(sourceDir)
	if err != nil {
		return fmt.Errorf("resolve GoMonty source directory: %w", err)
	}
	for _, required := range []string{"go.mod", "Cargo.toml", filepath.Join("scripts", "build-go-ffi.sh")} {
		info, statErr := os.Stat(filepath.Join(sourceDir, required))
		if statErr != nil || info.IsDir() {
			return fmt.Errorf("GoMonty source directory %s is missing %s", sourceDir, required)
		}
	}
	sourceDigest, err := ComputeNativeSourceDigest(sourceDir)
	if err != nil {
		return err
	}
	if sourceDigest != manifest.SourceSHA256 {
		return fmt.Errorf("%w: native source SHA-256 is %s, expected %s; refusing to run the build", ErrIntegrity, sourceDigest, manifest.SourceSHA256)
	}

	targetDirectory := os.Getenv("CARGO_TARGET_DIR")
	if targetDirectory == "" {
		work, err := os.MkdirTemp(filepath.Dir(payload), ".cargo-*")
		if err != nil {
			return fmt.Errorf("create Cargo staging directory: %w", err)
		}
		defer os.RemoveAll(work)
		targetDirectory = filepath.Join(work, "target")
	} else {
		targetDirectory, err = filepath.Abs(targetDirectory)
		if err != nil {
			return fmt.Errorf("resolve CARGO_TARGET_DIR: %w", err)
		}
		if err := os.MkdirAll(targetDirectory, 0o755); err != nil {
			return fmt.Errorf("create CARGO_TARGET_DIR: %w", err)
		}
	}
	command := exec.CommandContext(ctx, "bash", filepath.Join(sourceDir, "scripts", "build-go-ffi.sh"), target.RustTarget)
	command.Dir = sourceDir
	command.Env = append(command.Environ(),
		"MONTY_GO_FFI_SKIP_HEADER=1",
		"GOMONTY_NATIVE_OUTPUT_DIR="+payload,
		"CARGO_TARGET_DIR="+targetDirectory,
	)
	var output limitedBuffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ctx.Err()
		}
		return fmt.Errorf("build GoMonty native runtime: %w\n%s", err, output.String())
	}
	return nil
}

// ComputeNativeSourceDigest hashes every reviewed input executed or compiled by
// PrepareBuild. Generated target directories and native outputs are excluded.
func ComputeNativeSourceDigest(sourceDir string) (string, error) {
	paths := []string{"Cargo.toml", "Cargo.lock", "rust-toolchain.toml", filepath.Join("scripts", "build-go-ffi.sh")}
	for _, directory := range []string{filepath.Join("crates", "monty-go-ffi"), filepath.Join("crates", "gomonty-worker")} {
		err := filepath.WalkDir(filepath.Join(sourceDir, directory), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("native source input %s is not a regular file", path)
			}
			relative, err := filepath.Rel(sourceDir, path)
			if err != nil {
				return err
			}
			paths = append(paths, filepath.ToSlash(relative))
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("enumerate native source directory %s: %w", directory, err)
		}
	}
	sort.Strings(paths)
	hasher := sha256.New()
	for _, relative := range paths {
		path := filepath.Join(sourceDir, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if err != nil {
			return "", fmt.Errorf("inspect native source input %s: %w", relative, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("native source input %s is not a regular file", relative)
		}
		_, _ = fmt.Fprintf(hasher, "%s\x00%d\x00", filepath.ToSlash(relative), info.Size())
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(hasher, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		_, _ = hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func packageSourceRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("locate GoMonty module source: runtime caller unavailable; set PrepareOptions.SourceDir")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "Cargo.toml")); err == nil {
		return root, nil
	}
	modulePath := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		if strings.HasSuffix(info.Main.Path, "/gomonty") {
			modulePath = info.Main.Path
		}
		for _, dependency := range info.Deps {
			candidate := dependency
			if dependency.Replace != nil {
				candidate = dependency.Replace
			}
			if strings.HasSuffix(dependency.Path, "/gomonty") {
				if candidate.Path != "" && (strings.HasPrefix(candidate.Path, ".") || filepath.IsAbs(candidate.Path)) {
					if candidateRoot, err := filepath.Abs(candidate.Path); err == nil {
						if _, err := os.Stat(filepath.Join(candidateRoot, "Cargo.toml")); err == nil {
							return candidateRoot, nil
						}
					}
				}
				modulePath = dependency.Path
				break
			}
		}
	}
	if modulePath != "" {
		command := exec.Command("go", "list", "-m", "-f={{.Dir}}", modulePath)
		var output limitedBuffer
		command.Stdout = &output
		command.Stderr = &output
		if err := command.Run(); err == nil {
			candidate := strings.TrimSpace(output.Buffer.String())
			if _, err := os.Stat(filepath.Join(candidate, "Cargo.toml")); err == nil {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("locate GoMonty module source from %s; set PrepareOptions.SourceDir", file)
}

type limitedBuffer struct {
	bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	const limit = 32 << 10
	original := len(data)
	remaining := limit - b.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
			b.truncated = true
		}
		_, _ = b.Buffer.Write(data)
	} else {
		b.truncated = true
	}
	return original, nil
}

func (b *limitedBuffer) String() string {
	value := b.Buffer.String()
	if b.truncated {
		value = strings.TrimRight(value, "\n") + "\n... build output truncated ..."
	}
	return value
}
