//go:build linux

package runtimebundle

import (
	"errors"
	"testing"
)

func TestClassifyLinuxInterpreter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		interpreter string
		want        string
		ok          bool
	}{
		{"/lib/ld-musl-x86_64.so.1", "musl", true},
		{"/lib/ld-musl-aarch64.so.1", "musl", true},
		{"/lib64/ld-linux-x86-64.so.2", "", true},
		{"/lib/ld-linux-aarch64.so.1", "", true},
		{"/unknown/loader", "", false},
	}
	for _, test := range tests {
		got, ok := classifyLinuxInterpreter(test.interpreter)
		if got != test.want || ok != test.ok {
			t.Errorf("classifyLinuxInterpreter(%q) = %q, %v; want %q, %v", test.interpreter, got, ok, test.want, test.ok)
		}
	}
}

func TestDetectLinuxRuntimeVariantPrefersRunningELF(t *testing.T) {
	t.Parallel()
	interpreter := func(path string) (string, error) {
		if path == "/proc/self/exe" {
			return "/lib/ld-musl-x86_64.so.1", nil
		}
		return "", errors.New("missing")
	}
	variant, err := detectLinuxRuntimeVariant("amd64", interpreter, func(string) bool { return true })
	if err != nil || variant != "musl" {
		t.Fatalf("variant = %q, error = %v; want musl", variant, err)
	}
}

func TestDetectLinuxRuntimeVariantRefusesAmbiguousFallback(t *testing.T) {
	t.Parallel()
	missing := func(string) (string, error) { return "", errors.New("missing") }
	_, err := detectLinuxRuntimeVariant("amd64", missing, func(string) bool { return true })
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}
