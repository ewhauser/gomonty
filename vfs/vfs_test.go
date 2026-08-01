package vfs

import (
	"context"
	"testing"
	"time"

	"github.com/regularkevvv/gomonty"
)

type fixedClock struct{ value time.Time }

func (clock fixedClock) Now() time.Time { return clock.value }

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

func TestHandlerLatestMontyCapabilitiesStayAbstract(t *testing.T) {
	fileSystem := NewMemoryFS()
	fileSystem.AddText("/note.txt", "a")
	// Hide MemoryFS's optional append methods to prove the generic fallback.
	baseOnly := struct{ FileSystem }{FileSystem: fileSystem}
	clock := fixedClock{value: time.Date(2026, time.August, 1, 12, 34, 56, 123_456_000, time.UTC)}
	handler := HandlerWithClock(baseOnly, nil, clock)

	appendResult, err := handler(context.Background(), monty.OSCall{
		Function: monty.OSPathAppendText,
		Args: []monty.Value{
			monty.PathValue("/note.txt"),
			monty.String("bc"),
		},
	})
	if err != nil {
		t.Fatalf("append handler: %v", err)
	}
	if got := appendResult.Value().Raw(); got != int64(2) {
		t.Fatalf("append result = %#v, want 2", got)
	}
	if got, err := fileSystem.ReadText("/note.txt"); err != nil || got != "abc" {
		t.Fatalf("appended content = %q, %v", got, err)
	}

	openResult, err := handler(context.Background(), monty.OSCall{
		Function: monty.OSOpen,
		Args: []monty.Value{
			monty.PathValue("/note.txt"),
			monty.String("w"),
		},
	})
	if err != nil {
		t.Fatalf("open handler: %v", err)
	}
	file, ok := openResult.Value().FileHandle()
	if !ok || file.Path != "/note.txt" || file.Mode != "w" {
		t.Fatalf("open result = %#v, %v", file, ok)
	}
	if got, err := fileSystem.ReadText("/note.txt"); err != nil || got != "" {
		t.Fatalf("open(w) did not truncate: %q, %v", got, err)
	}

	todayResult, err := handler(context.Background(), monty.OSCall{Function: monty.OSDateToday})
	if err != nil {
		t.Fatalf("date.today handler: %v", err)
	}
	today, ok := todayResult.Value().Date()
	if !ok || today != (monty.Date{Year: 2026, Month: 8, Day: 1}) {
		t.Fatalf("date.today result = %#v, %v", today, ok)
	}

	nowResult, err := handler(context.Background(), monty.OSCall{
		Function: monty.OSDateTimeNow,
		Args:     []monty.Value{monty.None()},
	})
	if err != nil {
		t.Fatalf("datetime.now handler: %v", err)
	}
	now, ok := nowResult.Value().DateTime()
	if !ok || now.Year != 2026 || now.Month != 8 || now.Day != 1 || now.Microsecond != 123_456 {
		t.Fatalf("datetime.now result = %#v, %v", now, ok)
	}
}
