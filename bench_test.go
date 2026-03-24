package monty

import (
	"context"
	_ "embed"
	"math/big"
	"testing"
)

const benchScriptName = "bench.py"

const benchAddTwo = "1 + 2"

const benchListAppend = `
a = []
a.append(42)
a[0]
`

const benchLoopMod13 = `
v = ''
for i in range(1_000):
    if i % 13 == 0:
        v += 'x'
len(v)
`

const benchFuncCallKwargs = `
def add(a, b=2):
    return a + b

add(a=1)
`

const benchListAppendStr = `
a = []
for i in range(100_000):
    a.append(str(i))
len(a)
`

const benchListAppendInt = `
a = []
for i in range(100_000):
    a.append(i)
sum(a)
`

const benchFib25 = `
def fib(n):
    if n <= 1:
        return n
    return fib(n - 1) + fib(n - 2)

fib(25)
`

const benchListComp = "len([x * 2 for x in range(1000)])"

const benchDictComp = "len({i // 2: i * 2 for i in range(1000)})"

const benchEmptyTuples = "len([() for _ in range(100_000)])"

const benchPairTuples = "len([(i, i + 1) for i in range(100_000)])"

const benchExternalLoop = `
def run():
    total = 0
    for _ in range(100):
        total += host_value()
    return total

run()
`

const benchExternalKwargsLoop = `
def run():
    total = 0
    for i in range(100):
        total += host_add(a=i, b=1)
    return total

run()
`

const benchOSLoop = `
from pathlib import Path

def run():
    total = 0
    for _ in range(100):
        total += 1 if Path('/bench.txt').exists() else 0
    return total

run()
`

const benchNameLookup = "target"

const benchCallSnapshot = `
target()
`

//go:embed testdata/bench_kitchen_sink.py
var benchKitchenSink string

type benchmarkCase struct {
	name     string
	code     string
	expected int64
}

var benchmarkCases = []benchmarkCase{
	{name: "add_two", code: benchAddTwo, expected: 3},
	{name: "list_append", code: benchListAppend, expected: 42},
	{name: "loop_mod_13", code: benchLoopMod13, expected: 77},
	{name: "kitchen_sink", code: benchKitchenSink, expected: 373},
	{name: "func_call_kwargs", code: benchFuncCallKwargs, expected: 3},
	{name: "list_append_str", code: benchListAppendStr, expected: 100000},
	{name: "list_append_int", code: benchListAppendInt, expected: 4999950000},
	{name: "fib", code: benchFib25, expected: 75025},
	{name: "list_comp", code: benchListComp, expected: 1000},
	{name: "dict_comp", code: benchDictComp, expected: 500},
	{name: "empty_tuples", code: benchEmptyTuples, expected: 100000},
	{name: "pair_tuples", code: benchPairTuples, expected: 100000},
}

func BenchmarkMonty(b *testing.B) {
	for _, tc := range benchmarkCases {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkCompiledRunner(b, tc.code, tc.expected, RunOptions{})
		})
	}
}

func BenchmarkMontyEndToEnd(b *testing.B) {
	benchmarkEndToEnd(b, benchAddTwo, 3)
}

func BenchmarkMontyCallbacks(b *testing.B) {
	b.Run("external_loop", func(b *testing.B) {
		benchmarkCompiledRunner(b, benchExternalLoop, 100, RunOptions{
			Functions: map[string]ExternalFunction{
				"host_value": func(context.Context, Call) (Result, error) {
					return Return(Int(1)), nil
				},
			},
		})
	})

	b.Run("external_kwargs_loop", func(b *testing.B) {
		benchmarkCompiledRunner(b, benchExternalKwargsLoop, 5050, RunOptions{
			Functions: map[string]ExternalFunction{
				"host_add": func(_ context.Context, call Call) (Result, error) {
					var lhs int64
					var rhs int64
					for _, item := range call.Kwargs {
						switch item.Key.Raw().(string) {
						case "a":
							lhs = item.Value.Raw().(int64)
						case "b":
							rhs = item.Value.Raw().(int64)
						}
					}
					return Return(Int(lhs + rhs)), nil
				},
			},
		})
	})

	b.Run("os_loop", func(b *testing.B) {
		benchmarkCompiledRunner(b, benchOSLoop, 100, RunOptions{
			OS: func(_ context.Context, call OSCall) (Result, error) {
				if call.Function != OSPathExists {
					b.Fatalf("unexpected OS function: %s", call.Function)
				}
				return Return(Bool(true)), nil
			},
		})
	})
}

func BenchmarkMontyDecompose(b *testing.B) {
	b.Run("compile_only", func(b *testing.B) {
		ctx := context.Background()

		verifyRunner, err := New(benchAddTwo, CompileOptions{ScriptName: benchScriptName})
		if err != nil {
			b.Fatalf("New: %v", err)
		}
		assertBenchmarkResult(b, verifyRunner, ctx, 3, RunOptions{})
		closeTestRunner(verifyRunner)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			runner, err := New(benchAddTwo, CompileOptions{ScriptName: benchScriptName})
			if err != nil {
				b.Fatalf("New: %v", err)
			}
			closeTestRunner(runner)
		}
	})

	b.Run("dump_load_runner", func(b *testing.B) {
		runner, err := New(benchListAppendInt, CompileOptions{ScriptName: benchScriptName})
		if err != nil {
			b.Fatalf("New: %v", err)
		}
		b.Cleanup(func() {
			closeTestRunner(runner)
		})

		dump, err := runner.Dump()
		if err != nil {
			b.Fatalf("Dump: %v", err)
		}

		loaded, err := LoadRunner(dump)
		if err != nil {
			b.Fatalf("LoadRunner: %v", err)
		}
		closeTestRunner(loaded)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			loaded, err := LoadRunner(dump)
			if err != nil {
				b.Fatalf("LoadRunner: %v", err)
			}
			closeTestRunner(loaded)
		}
	})

	b.Run("start_to_first_progress", func(b *testing.B) {
		runner, err := New(benchCallSnapshot, CompileOptions{ScriptName: benchScriptName})
		if err != nil {
			b.Fatalf("New: %v", err)
		}
		b.Cleanup(func() {
			closeTestRunner(runner)
		})

		ctx := context.Background()
		progress, err := runner.Start(ctx, StartOptions{})
		if err != nil {
			b.Fatalf("Start: %v", err)
		}
		if _, ok := progress.(*Snapshot); !ok {
			closeTestProgress(progress)
			b.Fatalf("unexpected progress type %T", progress)
		}
		closeTestProgress(progress)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			progress, err := runner.Start(ctx, StartOptions{})
			if err != nil {
				b.Fatalf("Start: %v", err)
			}
			if _, ok := progress.(*Snapshot); !ok {
				closeTestProgress(progress)
				b.Fatalf("unexpected progress type %T", progress)
			}
			closeTestProgress(progress)
		}
	})

	b.Run("resolved_name_lookup", func(b *testing.B) {
		snapshot := prepareNameLookupSnapshot(b, benchNameLookup)
		ctx := context.Background()

		progress, err := LoadSnapshot(snapshot)
		if err != nil {
			b.Fatalf("LoadSnapshot: %v", err)
		}
		nameLookup, ok := progress.(*NameLookupSnapshot)
		if !ok {
			closeTestProgress(progress)
			b.Fatalf("unexpected progress type %T", progress)
		}
		complete, err := nameLookup.ResumeValue(ctx, Int(42))
		if err != nil {
			b.Fatalf("ResumeValue: %v", err)
		}
		assertCompleteInt(b, complete, 42)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			progress, err := LoadSnapshot(snapshot)
			if err != nil {
				b.Fatalf("LoadSnapshot: %v", err)
			}
			nameLookup, ok := progress.(*NameLookupSnapshot)
			if !ok {
				closeTestProgress(progress)
				b.Fatalf("unexpected progress type %T", progress)
			}
			complete, err := nameLookup.ResumeValue(ctx, Int(42))
			if err != nil {
				b.Fatalf("ResumeValue: %v", err)
			}
			assertCompleteInt(b, complete, 42)
		}
	})

	b.Run("unresolved_name_lookup", func(b *testing.B) {
		snapshot := prepareNameLookupSnapshot(b, benchNameLookup)
		ctx := context.Background()

		progress, err := LoadSnapshot(snapshot)
		if err != nil {
			b.Fatalf("LoadSnapshot: %v", err)
		}
		nameLookup, ok := progress.(*NameLookupSnapshot)
		if !ok {
			closeTestProgress(progress)
			b.Fatalf("unexpected progress type %T", progress)
		}
		if _, err := nameLookup.ResumeUndefined(ctx); err == nil {
			b.Fatal("ResumeUndefined: expected error")
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			progress, err := LoadSnapshot(snapshot)
			if err != nil {
				b.Fatalf("LoadSnapshot: %v", err)
			}
			nameLookup, ok := progress.(*NameLookupSnapshot)
			if !ok {
				closeTestProgress(progress)
				b.Fatalf("unexpected progress type %T", progress)
			}
			if _, err := nameLookup.ResumeUndefined(ctx); err == nil {
				b.Fatal("ResumeUndefined: expected error")
			}
		}
	})

	b.Run("resume_call_complete", func(b *testing.B) {
		snapshot := prepareCallSnapshot(b)
		ctx := context.Background()

		progress, err := LoadSnapshot(snapshot)
		if err != nil {
			b.Fatalf("LoadSnapshot: %v", err)
		}
		callSnapshot, ok := progress.(*Snapshot)
		if !ok {
			closeTestProgress(progress)
			b.Fatalf("unexpected progress type %T", progress)
		}
		complete, err := callSnapshot.ResumeReturn(ctx, Int(42))
		if err != nil {
			b.Fatalf("ResumeReturn: %v", err)
		}
		assertCompleteInt(b, complete, 42)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			progress, err := LoadSnapshot(snapshot)
			if err != nil {
				b.Fatalf("LoadSnapshot: %v", err)
			}
			callSnapshot, ok := progress.(*Snapshot)
			if !ok {
				closeTestProgress(progress)
				b.Fatalf("unexpected progress type %T", progress)
			}
			complete, err := callSnapshot.ResumeReturn(ctx, Int(42))
			if err != nil {
				b.Fatalf("ResumeReturn: %v", err)
			}
			assertCompleteInt(b, complete, 42)
		}
	})

	b.Run("resume_pending", func(b *testing.B) {
		snapshot := prepareCallSnapshot(b)
		ctx := context.Background()

		progress, err := LoadSnapshot(snapshot)
		if err != nil {
			b.Fatalf("LoadSnapshot: %v", err)
		}
		callSnapshot, ok := progress.(*Snapshot)
		if !ok {
			closeTestProgress(progress)
			b.Fatalf("unexpected progress type %T", progress)
		}
		pending, err := callSnapshot.ResumePending(ctx)
		if err != nil {
			b.Fatalf("ResumePending: %v", err)
		}
		closeTestProgress(pending)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			progress, err := LoadSnapshot(snapshot)
			if err != nil {
				b.Fatalf("LoadSnapshot: %v", err)
			}
			callSnapshot, ok := progress.(*Snapshot)
			if !ok {
				closeTestProgress(progress)
				b.Fatalf("unexpected progress type %T", progress)
			}
			pending, err := callSnapshot.ResumePending(ctx)
			if err != nil {
				b.Fatalf("ResumePending: %v", err)
			}
			closeTestProgress(pending)
		}
	})
}

func benchmarkCompiledRunner(b *testing.B, code string, expected int64, opts RunOptions) {
	b.Helper()

	runner, err := New(code, CompileOptions{ScriptName: benchScriptName})
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	b.Cleanup(func() {
		closeTestRunner(runner)
	})

	ctx := context.Background()
	assertBenchmarkResult(b, runner, ctx, expected, opts)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		assertBenchmarkResult(b, runner, ctx, expected, opts)
	}
}

func benchmarkEndToEnd(b *testing.B, code string, expected int64) {
	b.Helper()

	ctx := context.Background()
	verifyRunner, err := New(code, CompileOptions{ScriptName: benchScriptName})
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	assertBenchmarkResult(b, verifyRunner, ctx, expected, RunOptions{})
	closeTestRunner(verifyRunner)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runner, err := New(code, CompileOptions{ScriptName: benchScriptName})
		if err != nil {
			b.Fatalf("New: %v", err)
		}
		assertBenchmarkResult(b, runner, ctx, expected, RunOptions{})
		closeTestRunner(runner)
	}
}

func prepareNameLookupSnapshot(b *testing.B, code string) []byte {
	b.Helper()

	runner, err := New(code, CompileOptions{ScriptName: benchScriptName})
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	defer closeTestRunner(runner)

	progress, err := runner.Start(context.Background(), StartOptions{})
	if err != nil {
		b.Fatalf("Start: %v", err)
	}

	nameLookup, ok := progress.(*NameLookupSnapshot)
	if !ok {
		closeTestProgress(progress)
		b.Fatalf("unexpected progress type %T", progress)
	}

	dump, err := nameLookup.Dump()
	if err != nil {
		closeTestProgress(progress)
		b.Fatalf("Dump: %v", err)
	}
	closeTestProgress(progress)
	return dump
}

func prepareCallSnapshot(b *testing.B) []byte {
	b.Helper()

	runner, err := New(benchCallSnapshot, CompileOptions{ScriptName: benchScriptName})
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	defer closeTestRunner(runner)

	ctx := context.Background()
	progress, err := runner.Start(ctx, StartOptions{})
	if err != nil {
		b.Fatalf("Start: %v", err)
	}

	snapshot, ok := progress.(*Snapshot)
	if !ok {
		closeTestProgress(progress)
		b.Fatalf("unexpected progress type %T", progress)
	}

	dump, err := snapshot.Dump()
	if err != nil {
		closeTestProgress(progress)
		b.Fatalf("Dump: %v", err)
	}
	closeTestProgress(progress)
	return dump
}

func assertBenchmarkResult(b *testing.B, runner *Runner, ctx context.Context, expected int64, opts RunOptions) {
	b.Helper()

	value, err := runner.Run(ctx, opts)
	if err != nil {
		b.Fatalf("Run: %v", err)
	}
	if got := benchmarkInt64(b, value); got != expected {
		b.Fatalf("unexpected result: got %d want %d", got, expected)
	}
}

func assertCompleteInt(b *testing.B, progress Progress, expected int64) {
	b.Helper()

	complete, ok := progress.(*Complete)
	if !ok {
		closeTestProgress(progress)
		b.Fatalf("unexpected progress type %T", progress)
	}
	if got := benchmarkInt64(b, complete.Output); got != expected {
		b.Fatalf("unexpected result: got %d want %d", got, expected)
	}
}

func benchmarkInt64(b *testing.B, value Value) int64 {
	b.Helper()

	switch value.Kind() {
	case valueKindInt:
		return value.Raw().(int64)
	case valueKindBigInt:
		bigValue := value.Raw().(*big.Int)
		if !bigValue.IsInt64() {
			b.Fatalf("big int result out of int64 range: %s", bigValue.String())
		}
		return bigValue.Int64()
	default:
		b.Fatalf("unexpected result kind: %s", value.Kind())
		return 0
	}
}
