//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64)

package monty

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestRunnerAndReplDumpLoadAcrossSubprocesses(t *testing.T) {
	runner, err := New("input_value + 1", CompileOptions{
		ScriptName: "dump-runner.py",
		Inputs:     []string{"input_value"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runnerState, err := runner.Dump()
	if err != nil {
		t.Fatalf("runner Dump: %v", err)
	}
	_ = runner.Close()
	loadedRunner, err := LoadRunner(runnerState)
	if err != nil {
		t.Fatalf("LoadRunner: %v", err)
	}
	defer func() { _ = loadedRunner.Close() }()
	value, err := loadedRunner.Run(context.Background(), RunOptions{
		Inputs: map[string]Value{"input_value": Int(41)},
	})
	if err != nil || value.Raw() != int64(42) {
		t.Fatalf("loaded runner output = %#v, %v; want 42", value.Raw(), err)
	}

	repl, err := NewRepl(ReplOptions{ScriptName: "dump-repl.py"})
	if err != nil {
		t.Fatalf("NewRepl: %v", err)
	}
	if _, err := repl.FeedRun(context.Background(), "kept = 41", FeedOptions{}); err != nil {
		t.Fatalf("seed REPL: %v", err)
	}
	originalPID, _ := repl.WorkerPID()
	replState, err := repl.Dump()
	if err != nil {
		t.Fatalf("REPL Dump: %v", err)
	}
	_ = repl.Close()
	process, err := os.FindProcess(originalPID)
	if err != nil {
		t.Fatalf("FindProcess(%d): %v", originalPID, err)
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("kill idle worker %d: %v", originalPID, err)
	}
	time.Sleep(20 * time.Millisecond)
	loadedRepl, err := LoadRepl(replState)
	if err != nil {
		t.Fatalf("LoadRepl: %v", err)
	}
	defer func() { _ = loadedRepl.Close() }()
	loadedPID, _ := loadedRepl.WorkerPID()
	if loadedPID == 0 || loadedPID == originalPID {
		t.Fatalf("loaded REPL PID = %d, want fresh PID distinct from %d", loadedPID, originalPID)
	}
	value, err = loadedRepl.FeedRun(context.Background(), "kept + 1", FeedOptions{})
	if err != nil || value.Raw() != int64(42) {
		t.Fatalf("loaded REPL output = %#v, %v; want 42", value.Raw(), err)
	}
}

func TestReplRuntimeErrorPreservesCompletedMutations(t *testing.T) {
	repl, err := NewRepl(ReplOptions{})
	if err != nil {
		t.Fatalf("NewRepl: %v", err)
	}
	defer func() { _ = repl.Close() }()
	if _, err := repl.FeedRun(context.Background(), "items = []", FeedOptions{}); err != nil {
		t.Fatalf("seed REPL: %v", err)
	}
	if _, err := repl.FeedRun(context.Background(), "items.append(1)\n1 / 0", FeedOptions{}); err == nil {
		t.Fatal("expected ZeroDivisionError")
	}
	value, err := repl.FeedRun(context.Background(), "len(items)", FeedOptions{})
	if err != nil || value.Raw() != int64(1) {
		t.Fatalf("state after runtime error = %#v, %v; want one retained mutation", value.Raw(), err)
	}
}

func TestKilledSuspendedWorkerRollsBackWithoutReplay(t *testing.T) {
	repl, err := NewRepl(ReplOptions{ScriptName: "no-replay.py"})
	if err != nil {
		t.Fatalf("NewRepl: %v", err)
	}
	defer func() { _ = repl.Close() }()
	if _, err := repl.FeedRun(context.Background(), "counter = 0", FeedOptions{}); err != nil {
		t.Fatalf("seed REPL: %v", err)
	}
	progress, err := repl.FeedStart(
		context.Background(),
		"counter += 1\next()",
		FeedStartOptions{},
	)
	if err != nil {
		t.Fatalf("FeedStart: %v", err)
	}
	call := progress.(*Snapshot)
	pid, ok := call.WorkerPID()
	if !ok {
		t.Fatal("snapshot has no worker PID")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := call.ResumeReturn(context.Background(), None()); err == nil {
		t.Fatal("ResumeReturn: expected worker crash")
	}

	value, err := repl.FeedRun(context.Background(), "counter", FeedOptions{})
	if err != nil || value.Raw() != int64(0) {
		t.Fatalf("counter after crash = %#v, %v; want rollback value 0", value.Raw(), err)
	}
}

func TestReplTypeCheckingCanBeSkippedPerFeed(t *testing.T) {
	repl, err := NewRepl(ReplOptions{TypeCheck: true})
	if err != nil {
		t.Fatalf("NewRepl: %v", err)
	}
	defer func() { _ = repl.Close() }()
	if _, err := repl.FeedRun(context.Background(), "answer: int = 'wrong'", FeedOptions{}); err == nil {
		t.Fatal("type-checked feed: expected typing error")
	}
	value, err := repl.FeedRun(
		context.Background(),
		"answer: int = 'accepted'\nanswer",
		FeedOptions{SkipTypeCheck: true},
	)
	if err != nil || value.Raw() != "accepted" {
		t.Fatalf("skip-type-check output = %#v, %v", value.Raw(), err)
	}
}

func TestInvalidFeedInputLeavesReplUsable(t *testing.T) {
	repl, err := NewRepl(ReplOptions{})
	if err != nil {
		t.Fatalf("NewRepl: %v", err)
	}
	defer func() { _ = repl.Close() }()

	_, err = repl.FeedStart(context.Background(), "bad", FeedStartOptions{
		Inputs: map[string]Value{"bad": ReprValue("<opaque>")},
	})
	if err == nil {
		t.Fatal("FeedStart: expected output-only input rejection")
	}

	value, err := repl.FeedRun(context.Background(), "40 + 2", FeedOptions{})
	if err != nil || value.Raw() != int64(42) {
		t.Fatalf("REPL after rejected input = %#v, %v; want 42", value.Raw(), err)
	}
}

func TestAssertMessageOptionsSurviveSerialization(t *testing.T) {
	off := AssertMessageAnnotations(0)
	runner, err := New("assert 1 == 2", CompileOptions{
		AssertMessageAnnotations: &off,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runnerDump, err := runner.Dump()
	if err != nil {
		t.Fatalf("Runner.Dump: %v", err)
	}
	_ = runner.Close()
	runner, err = LoadRunner(runnerDump)
	if err != nil {
		t.Fatalf("LoadRunner: %v", err)
	}
	defer func() { _ = runner.Close() }()
	_, err = runner.Run(context.Background(), RunOptions{})
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.summary.Message != "" {
		t.Fatalf("annotations-off error = %#v, want empty AssertionError message", err)
	}

	maxBytes := AssertMessageAnnotations(6)
	repl, err := NewRepl(ReplOptions{AssertMessageAnnotations: &maxBytes})
	if err != nil {
		t.Fatalf("NewRepl: %v", err)
	}
	replDump, err := repl.Dump()
	if err != nil {
		t.Fatalf("Repl.Dump: %v", err)
	}
	_ = repl.Close()
	repl, err = LoadRepl(replDump)
	if err != nil {
		t.Fatalf("LoadRepl: %v", err)
	}
	defer func() { _ = repl.Close() }()
	_, err = repl.FeedRun(context.Background(), "assert 'abcdefghij' == ''", FeedOptions{})
	runtimeErr = nil
	if !errors.As(err, &runtimeErr) || runtimeErr.summary.Message != "assert 'abcde… == ''" {
		t.Fatalf("custom annotations error = %#v, want truncated AssertionError message", err)
	}
}
