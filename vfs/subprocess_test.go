//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64)

package vfs

import (
	"context"
	"testing"
	"time"

	"github.com/ewhauser/gomonty"
)

func TestLatestMontyOSCallsEndToEnd(t *testing.T) {
	fileSystem := NewMemoryFS()
	fileSystem.AddText("/note.txt", "a")
	runner, err := monty.New(`
from pathlib import Path
from datetime import date, datetime

path = Path('/note.txt')
(path.append_text('bc'), path.read_text(), date.today(), datetime.now())
`, monty.CompileOptions{ScriptName: "latest-os.py"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = runner.Close() }()

	clock := fixedClock{value: time.Date(2026, time.August, 1, 12, 34, 56, 0, time.UTC)}
	value, err := runner.Run(context.Background(), monty.RunOptions{
		OS: HandlerWithClock(fileSystem, nil, clock),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	items, ok := value.Raw().(monty.Tuple)
	if !ok || len(items) != 4 {
		t.Fatalf("output = %#v, want four-item tuple", value.Raw())
	}
	if got := items[0].Raw(); got != int64(2) {
		t.Fatalf("append count = %#v, want 2", got)
	}
	if got := items[1].Raw(); got != "abc" {
		t.Fatalf("content = %#v, want abc", got)
	}
	if got, ok := items[2].Date(); !ok || got != (monty.Date{Year: 2026, Month: 8, Day: 1}) {
		t.Fatalf("today = %#v, %v", got, ok)
	}
	if got, ok := items[3].DateTime(); !ok || got.Hour != 12 || got.Minute != 34 || got.Second != 56 {
		t.Fatalf("now = %#v, %v", got, ok)
	}
}

func TestOpenFileHandleEndToEnd(t *testing.T) {
	fileSystem := NewMemoryFS()
	fileSystem.AddText("/note.txt", "a")
	runner, err := monty.New(`
file = open('/note.txt', 'a')
written = file.write('bc')
file.close()
file = open('/note.txt', 'r')
content = file.read()
file.close()
(written, content)
`, monty.CompileOptions{ScriptName: "open-file.py"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = runner.Close() }()

	value, err := runner.Run(context.Background(), monty.RunOptions{
		OS: Handler(fileSystem, nil),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	items, ok := value.Raw().(monty.Tuple)
	if !ok || len(items) != 2 {
		t.Fatalf("output = %#v, want two-item tuple", value.Raw())
	}
	if got := items[0].Raw(); got != int64(2) {
		t.Fatalf("write count = %#v, want 2", got)
	}
	if got := items[1].Raw(); got != "abc" {
		t.Fatalf("content = %#v, want abc", got)
	}
}
