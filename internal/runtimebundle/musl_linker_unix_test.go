//go:build !windows

package runtimebundle

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMuslLinkerEmbedsGCCRuntime(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	argsPath := filepath.Join(tempDir, "args")
	compilerPath := filepath.Join(tempDir, "compiler")
	compiler := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GOMONTY_TEST_LINKER_ARGS\"\n"
	if err := os.WriteFile(compilerPath, []byte(compiler), 0o700); err != nil {
		t.Fatalf("write fake compiler: %v", err)
	}

	linkerPath := filepath.Join("..", "..", "scripts", "musl-linker.sh")
	command := exec.Command(linkerPath, "input.o", "-Wl,-Bdynamic", "-lgcc_s", "-lc", "-shared")
	command.Env = append(os.Environ(),
		"GOMONTY_MUSL_CC="+compilerPath,
		"GOMONTY_TEST_LINKER_ARGS="+argsPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run musl linker: %v\n%s", err, output)
	}

	contents, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read rewritten arguments: %v", err)
	}
	got := strings.Fields(string(contents))
	want := []string{
		"input.o",
		"-Wl,-Bdynamic",
		"-Wl,-Bstatic", "-lgcc", "-lgcc_eh", "-Wl,-Bdynamic",
		"-lc", "-Wl,-Bstatic", "-lgcc", "-Wl,-Bdynamic",
		"-shared",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rewritten arguments = %#v, want %#v", got, want)
	}
}

func TestMuslLinkerRequiresCompiler(t *testing.T) {
	t.Parallel()

	linkerPath := filepath.Join("..", "..", "scripts", "musl-linker.sh")
	command := exec.Command(linkerPath, "input.o")
	command.Env = []string{"PATH=" + os.Getenv("PATH")}
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("musl linker unexpectedly accepted a missing compiler")
	}
	if !strings.Contains(string(output), "GOMONTY_MUSL_CC") {
		t.Fatalf("error = %q, want missing compiler diagnostic", output)
	}
}
