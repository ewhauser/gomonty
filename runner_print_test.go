//go:build cgo && !(darwin && amd64)

package monty

import (
	"context"
	"strings"
	"testing"
)

func TestRunnerRunCapturesInitialPrint(t *testing.T) {
	runner, err := New("print('hello')", CompileOptions{ScriptName: "probe.py"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		closeTestRunner(runner)
	})

	var out strings.Builder
	value, err := runner.Run(context.Background(), RunOptions{
		Print: WriterPrintCallback(&out),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := out.String(); got != "hello\n" {
		t.Fatalf("unexpected print output: got %q want %q", got, "hello\n")
	}
	if value.Kind() != valueKindNone {
		t.Fatalf("unexpected result kind: got %v want None", value.Kind())
	}
}

func TestRunnerRunCapturesInitialPrintBeforeExternalDispatch(t *testing.T) {
	runner, err := New("print('before')\next()", CompileOptions{ScriptName: "probe.py"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		closeTestRunner(runner)
	})

	var out strings.Builder
	calls := 0
	value, err := runner.Run(context.Background(), RunOptions{
		Print: WriterPrintCallback(&out),
		Functions: map[string]ExternalFunction{
			"ext": func(context.Context, Call) (Result, error) {
				calls++
				return Return(Int(7)), nil
			},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := out.String(); got != "before\n" {
		t.Fatalf("unexpected print output: got %q want %q", got, "before\n")
	}
	if calls != 1 {
		t.Fatalf("unexpected external call count: got %d want %d", calls, 1)
	}
	got, ok := value.Raw().(int64)
	if !ok || got != 7 {
		t.Fatalf("unexpected result: got %#v want %d", value.Raw(), 7)
	}
}

func TestRunnerRunCapturesPrintAfterResume(t *testing.T) {
	runner, err := New("ext()\nprint('after')", CompileOptions{ScriptName: "probe.py"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		closeTestRunner(runner)
	})

	var out strings.Builder
	calls := 0
	value, err := runner.Run(context.Background(), RunOptions{
		Print: WriterPrintCallback(&out),
		Functions: map[string]ExternalFunction{
			"ext": func(context.Context, Call) (Result, error) {
				calls++
				return Return(None()), nil
			},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := out.String(); got != "after\n" {
		t.Fatalf("unexpected print output: got %q want %q", got, "after\n")
	}
	if calls != 1 {
		t.Fatalf("unexpected external call count: got %d want %d", calls, 1)
	}
	if value.Kind() != valueKindNone {
		t.Fatalf("unexpected result kind: got %v want None", value.Kind())
	}
}

func TestReplFeedRunCapturesInitialPrint(t *testing.T) {
	repl, err := NewRepl(ReplOptions{ScriptName: "probe.py"})
	if err != nil {
		t.Fatalf("NewRepl: %v", err)
	}
	t.Cleanup(func() {
		closeTestRepl(repl)
	})

	var out strings.Builder
	value, err := repl.FeedRun(context.Background(), "print('hello')", FeedOptions{
		Print: WriterPrintCallback(&out),
	})
	if err != nil {
		t.Fatalf("FeedRun: %v", err)
	}

	if got := out.String(); got != "hello\n" {
		t.Fatalf("unexpected print output: got %q want %q", got, "hello\n")
	}
	if value.Kind() != valueKindNone {
		t.Fatalf("unexpected result kind: got %v want None", value.Kind())
	}
}
