//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64)

package monty

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const missingWorkerHelperEnv = "GOMONTY_TEST_MISSING_WORKER"

func TestMissingWorkerFailsFast(t *testing.T) {
	if os.Getenv(missingWorkerHelperEnv) == "1" {
		_, err := New("40 + 2", CompileOptions{})
		if err == nil || !strings.Contains(err.Error(), "initialize Monty subprocess worker") {
			t.Fatalf("New error = %v, want worker initialization failure", err)
		}
		return
	}

	cacheDir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMissingWorkerFailsFast$")
	cmd.Env = append(os.Environ(),
		missingWorkerHelperEnv+"=1",
		"GOMONTY_WORKER_PATH="+filepath.Join(cacheDir, "missing-worker"),
		"GOMONTY_FFI_CACHE_DIR="+cacheDir,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper failed: %v\n%s", err, output)
	}
}

func TestReplExecutesInSubprocessAndRecoversAfterCrash(t *testing.T) {
	repl, err := NewRepl(ReplOptions{ScriptName: "crash-recovery.py"})
	if err != nil {
		t.Fatalf("NewRepl: %v", err)
	}
	t.Cleanup(func() { closeTestRepl(repl) })

	if _, err := repl.FeedRun(context.Background(), "kept = 41", FeedOptions{}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	before, ok := repl.WorkerPID()
	if !ok || before <= 0 {
		t.Fatalf("WorkerPID() = %d, %v", before, ok)
	}
	if before == os.Getpid() {
		t.Fatalf("Monty executed in the Go host process %d", before)
	}

	killed := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		process, findErr := os.FindProcess(before)
		if findErr != nil {
			killed <- findErr
			return
		}
		killed <- process.Kill()
	}()

	if _, err := repl.FeedRun(context.Background(), "while True:\n    pass", FeedOptions{}); err == nil {
		t.Fatal("FeedRun: expected killed worker error")
	}
	if err := <-killed; err != nil {
		t.Fatalf("kill worker: %v", err)
	}

	after, ok := repl.WorkerPID()
	if !ok || after <= 0 {
		t.Fatalf("recovered WorkerPID() = %d, %v", after, ok)
	}
	if after == before {
		t.Fatalf("worker was not replaced after crash: pid %d", after)
	}

	value, err := repl.FeedRun(context.Background(), "kept + 1", FeedOptions{})
	if err != nil {
		t.Fatalf("run after recovery: %v", err)
	}
	if got := value.Raw(); got != int64(42) {
		t.Fatalf("recovered state = %#v, want 42", got)
	}
}

func TestReplRejectsASecondFeedUntilTheFirstFinishes(t *testing.T) {
	repl, err := NewRepl(ReplOptions{ScriptName: "single-flight.py"})
	if err != nil {
		t.Fatalf("NewRepl: %v", err)
	}
	t.Cleanup(func() { closeTestRepl(repl) })

	progress, err := repl.FeedStart(context.Background(), "ext()", FeedStartOptions{})
	if err != nil {
		t.Fatalf("first FeedStart: %v", err)
	}
	call, ok := progress.(*Snapshot)
	if !ok {
		t.Fatalf("progress = %T, want *Snapshot", progress)
	}
	if _, err := repl.FeedStart(context.Background(), "40 + 2", FeedStartOptions{}); !errors.Is(err, ErrConcurrentUse) {
		t.Fatalf("second FeedStart error = %v, want ErrConcurrentUse", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := call.ResumeReturn(canceled, None()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel first feed: %v, want context.Canceled", err)
	}
	value, err := repl.FeedRun(context.Background(), "40 + 2", FeedOptions{})
	if err != nil || value.Raw() != int64(42) {
		t.Fatalf("feed after cancellation = %#v, %v; want 42", value.Raw(), err)
	}
}

func TestReplSnapshotRestoresAndCancellationAbandonsFeed(t *testing.T) {
	repl, err := NewRepl(ReplOptions{ScriptName: "snapshot.py"})
	if err != nil {
		t.Fatalf("NewRepl: %v", err)
	}
	if _, err := repl.FeedRun(context.Background(), "kept = 40", FeedOptions{}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	progress, err := repl.FeedStart(
		context.Background(),
		"delta = ext()\nkept += delta\nkept",
		FeedStartOptions{},
	)
	if err != nil {
		t.Fatalf("FeedStart: %v", err)
	}
	call, ok := progress.(*Snapshot)
	if !ok {
		t.Fatalf("progress = %T, want *Snapshot", progress)
	}
	originalPID, ok := call.WorkerPID()
	if !ok {
		t.Fatal("snapshot has no worker PID")
	}
	blob, err := call.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	closeTestProgress(call)

	restored, owner, err := LoadReplSnapshot(blob)
	if err != nil {
		t.Fatalf("LoadReplSnapshot: %v", err)
	}
	t.Cleanup(func() { closeTestRepl(owner) })
	restoredCall, ok := restored.(*Snapshot)
	if !ok {
		t.Fatalf("restored progress = %T, want *Snapshot", restored)
	}
	restoredPID, ok := restoredCall.WorkerPID()
	if !ok || restoredPID == originalPID {
		t.Fatalf("restored worker PID = %d, want fresh PID distinct from %d", restoredPID, originalPID)
	}
	progress, err = restoredCall.ResumeReturn(context.Background(), Int(2))
	if err != nil {
		t.Fatalf("ResumeReturn: %v", err)
	}
	complete, ok := progress.(*Complete)
	if !ok || complete.Output.Raw() != int64(42) {
		t.Fatalf("completed progress = %#v (%T), want 42", progress, progress)
	}
	value, err := owner.FeedRun(context.Background(), "kept", FeedOptions{})
	if err != nil || value.Raw() != int64(42) {
		t.Fatalf("completed owner state = %#v, %v; want 42", value.Raw(), err)
	}

	abandoned, base, err := LoadReplSnapshot(blob)
	if err != nil {
		t.Fatalf("LoadReplSnapshot(abandon): %v", err)
	}
	t.Cleanup(func() { closeTestRepl(base) })
	abandonedCall := abandoned.(*Snapshot)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := abandonedCall.ResumeReturn(canceled, Int(99)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ResumeReturn error = %v, want context.Canceled", err)
	}
	value, err = base.FeedRun(context.Background(), "kept", FeedOptions{})
	if err != nil || value.Raw() != int64(40) {
		t.Fatalf("abandoned owner state = %#v, %v; want rollback value 40", value.Raw(), err)
	}
}

func TestRunnerSnapshotRestoresOnFreshWorker(t *testing.T) {
	runner, err := New("ext() + 1", CompileOptions{ScriptName: "runner-snapshot.py"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { closeTestRunner(runner) })

	progress, err := runner.Start(context.Background(), StartOptions{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	call := progress.(*Snapshot)
	originalPID, _ := call.WorkerPID()
	blob, err := call.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	closeTestProgress(call)

	restored, err := LoadSnapshot(blob)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	restoredCall := restored.(*Snapshot)
	restoredPID, _ := restoredCall.WorkerPID()
	if restoredPID == 0 || restoredPID == originalPID {
		t.Fatalf("restored worker PID = %d, want fresh PID distinct from %d", restoredPID, originalPID)
	}
	progress, err = restoredCall.ResumeReturn(context.Background(), Int(41))
	if err != nil {
		t.Fatalf("ResumeReturn: %v", err)
	}
	complete := progress.(*Complete)
	if got := complete.Output.Raw(); got != int64(42) {
		t.Fatalf("output = %#v, want 42", got)
	}
}

func TestLatestMontyValueVariantsRoundTrip(t *testing.T) {
	runner, err := New("class Widget:\n    pass\n(type(1), len, type(Widget()))", CompileOptions{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { closeTestRunner(runner) })
	value, err := runner.Run(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	items := value.Raw().(Tuple)
	pythonType, ok := items[0].Type()
	if !ok || pythonType.Name != "int" || pythonType.Instance {
		t.Fatalf("type value = %#v, %v", pythonType, ok)
	}
	builtin, ok := items[1].BuiltinFunction()
	if !ok || builtin.Name != "len" {
		t.Fatalf("builtin value = %#v, %v", builtin, ok)
	}
	// Monty v0.0.19 exposes user-defined class objects as opaque repr values;
	// only built-in type objects currently cross the public output boundary as
	// structured Type values.
	if got := items[2]; got.Kind() != "repr" || got.Raw() != "<class 'Widget'>" {
		t.Fatalf("user-defined type value = kind %q raw %#v", got.Kind(), got.Raw())
	}

	inputRunner, err := New("python_type is int and builtin([1, 2, 3]) == 3", CompileOptions{
		Inputs: []string{"python_type", "builtin"},
	})
	if err != nil {
		t.Fatalf("New(latest inputs): %v", err)
	}
	t.Cleanup(func() { closeTestRunner(inputRunner) })
	value, err = inputRunner.Run(context.Background(), RunOptions{Inputs: map[string]Value{
		"python_type": TypeValue(Type{Name: "int"}),
		"builtin":     BuiltinFunctionValue(BuiltinFunction{Name: "len"}),
	}})
	if err != nil || value.Raw() != true {
		t.Fatalf("latest input variants = %#v, %v; want true", value.Raw(), err)
	}

	fileRunner, err := New("open('/virtual/data.txt', 'r')", CompileOptions{})
	if err != nil {
		t.Fatalf("New(file): %v", err)
	}
	t.Cleanup(func() { closeTestRunner(fileRunner) })
	value, err = fileRunner.Run(context.Background(), RunOptions{
		OS: func(_ context.Context, call OSCall) (Result, error) {
			if call.Function != OSOpen {
				t.Fatalf("OS call = %q, want %q", call.Function, OSOpen)
			}
			return Return(FileHandleValue(FileHandle{
				Path: Path("/virtual/data.txt"), Mode: "r", Position: 0,
			})), nil
		},
	})
	if err != nil {
		t.Fatalf("Run(file): %v", err)
	}
	file, ok := value.FileHandle()
	if !ok || file.Path != "/virtual/data.txt" || file.Mode != "r" {
		t.Fatalf("file value = %#v, %v", file, ok)
	}
}

func TestMaxDurationStopsInfiniteLoopAndPoolRemainsUsable(t *testing.T) {
	runner, err := New("while True:\n    pass", CompileOptions{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = runner.Close() }()

	started := time.Now()
	_, err = runner.Run(context.Background(), RunOptions{
		Limits: &ResourceLimits{MaxDuration: 20 * time.Millisecond},
	})
	if err == nil {
		t.Fatal("Run: expected max-duration failure")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("max-duration failure took %s, want less than 3s", elapsed)
	}

	probe, err := New("6 * 7", CompileOptions{})
	if err != nil {
		t.Fatalf("New(probe): %v", err)
	}
	defer func() { _ = probe.Close() }()
	value, err := probe.Run(context.Background(), RunOptions{})
	if err != nil || value.Raw() != int64(42) {
		t.Fatalf("pool after timeout = %#v, %v; want 42", value.Raw(), err)
	}
}
