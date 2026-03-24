package vfs

import (
	"context"
	"testing"

	"github.com/ewhauser/gomonty"
)

func TestMemoryFSReadWriteRename(t *testing.T) {
	fileSystem := NewMemoryFS()
	fileSystem.AddText("/config.txt", "alpha")

	text, err := fileSystem.ReadText("/config.txt")
	if err != nil {
		t.Fatalf("ReadText: %v", err)
	}
	if text != "alpha" {
		t.Fatalf("unexpected text %q", text)
	}

	if err := fileSystem.Mkdir("/data", false, false); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if _, err := fileSystem.WriteText("/data/output.txt", "beta"); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	if err := fileSystem.Rename("/data/output.txt", "/data/result.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	entries, err := fileSystem.Iterdir("/data")
	if err != nil {
		t.Fatalf("Iterdir: %v", err)
	}
	if len(entries) != 1 || entries[0] != "/data/result.txt" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestHandlerEnvironmentAndErrorMapping(t *testing.T) {
	fileSystem := NewMemoryFS()
	fileSystem.AddText("/note.txt", "hello")

	handler := Handler(fileSystem, MapEnvironment{
		"HOME": "/sandbox",
	})

	getenvResult, err := handler(context.Background(), monty.OSCall{
		Function: monty.OSGetenv,
		Args: []monty.Value{
			monty.String("HOME"),
		},
	})
	if err != nil {
		t.Fatalf("getenv handler: %v", err)
	}
	if getenvResult.Value().Raw().(string) != "/sandbox" {
		t.Fatalf("unexpected getenv value: %#v", getenvResult.Value())
	}

	statResult, err := handler(context.Background(), monty.OSCall{
		Function: monty.OSPathStat,
		Args: []monty.Value{
			monty.PathValue(monty.Path("/note.txt")),
		},
	})
	if err != nil {
		t.Fatalf("stat handler: %v", err)
	}
	if _, ok := statResult.Value().StatResult(); !ok {
		t.Fatalf("expected stat result, got %#v", statResult.Value())
	}

	missingResult, err := handler(context.Background(), monty.OSCall{
		Function: monty.OSPathReadText,
		Args: []monty.Value{
			monty.PathValue(monty.Path("/missing.txt")),
		},
	})
	if err != nil {
		t.Fatalf("missing read handler: %v", err)
	}
	exception, ok := missingResult.Raised()
	if !ok {
		t.Fatalf("expected exception result, got %#v", missingResult)
	}
	if exception.Type != "FileNotFoundError" {
		t.Fatalf("unexpected exception type %q", exception.Type)
	}
}
