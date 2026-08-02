//go:build linux

package runtimebundle

import (
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

const maxELFInterpreterSize = 4 << 10

func currentRuntimeVariant() (string, error) {
	return detectLinuxRuntimeVariant(runtime.GOARCH, readELFInterpreter, regularFileExists)
}

func detectLinuxRuntimeVariant(goarch string, interpreter func(string) (string, error), exists func(string) bool) (string, error) {
	for _, candidate := range []string{"/proc/self/exe", "/bin/sh", "/bin/ls", "/usr/bin/env"} {
		value, err := interpreter(candidate)
		if err != nil {
			continue
		}
		if variant, ok := classifyLinuxInterpreter(value); ok {
			return variant, nil
		}
	}

	muslLoader, glibcLoaders := linuxLoaderPaths(goarch)
	musl := muslLoader != "" && exists(muslLoader)
	glibc := false
	for _, path := range glibcLoaders {
		glibc = glibc || exists(path)
	}
	switch {
	case musl && !glibc:
		return "musl", nil
	case glibc && !musl:
		return "", nil
	default:
		return "", fmt.Errorf("%w: cannot determine Linux libc for %s; refusing to guess between GNU and musl runtime assets", ErrUnsupported, goarch)
	}
}

func classifyLinuxInterpreter(value string) (string, bool) {
	base := strings.ToLower(value)
	switch {
	case strings.Contains(base, "ld-musl-"):
		return "musl", true
	case strings.Contains(base, "ld-linux"), strings.HasSuffix(base, "/ld.so"):
		return "", true
	default:
		return "", false
	}
}

func linuxLoaderPaths(goarch string) (string, []string) {
	switch goarch {
	case "amd64":
		return "/lib/ld-musl-x86_64.so.1", []string{
			"/lib64/ld-linux-x86-64.so.2",
			"/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2",
		}
	case "arm64":
		return "/lib/ld-musl-aarch64.so.1", []string{
			"/lib/ld-linux-aarch64.so.1",
			"/lib/aarch64-linux-gnu/ld-linux-aarch64.so.1",
		}
	default:
		return "", nil
	}
}

func readELFInterpreter(path string) (string, error) {
	file, err := elf.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	for _, program := range file.Progs {
		if program.Type != elf.PT_INTERP {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(program.Open(), maxELFInterpreterSize))
		if err != nil {
			return "", err
		}
		value := strings.TrimRight(string(data), "\x00")
		if value == "" {
			return "", errors.New("ELF interpreter is empty")
		}
		return value, nil
	}
	return "", errors.New("ELF file has no interpreter")
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
