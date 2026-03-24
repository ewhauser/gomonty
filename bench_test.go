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
			benchmarkCompiledRunner(b, tc.code, tc.expected)
		})
	}
}

func BenchmarkMontyEndToEnd(b *testing.B) {
	benchmarkEndToEnd(b, benchAddTwo, 3)
}

func benchmarkCompiledRunner(b *testing.B, code string, expected int64) {
	b.Helper()

	runner, err := New(code, CompileOptions{ScriptName: benchScriptName})
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	b.Cleanup(func() {
		closeBenchmarkRunner(runner)
	})

	ctx := context.Background()
	assertBenchmarkResult(b, runner, ctx, expected)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		assertBenchmarkResult(b, runner, ctx, expected)
	}
}

func benchmarkEndToEnd(b *testing.B, code string, expected int64) {
	b.Helper()

	ctx := context.Background()
	verifyRunner, err := New(code, CompileOptions{ScriptName: benchScriptName})
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	assertBenchmarkResult(b, verifyRunner, ctx, expected)
	closeBenchmarkRunner(verifyRunner)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runner, err := New(code, CompileOptions{ScriptName: benchScriptName})
		if err != nil {
			b.Fatalf("New: %v", err)
		}
		assertBenchmarkResult(b, runner, ctx, expected)
		closeBenchmarkRunner(runner)
	}
}

func assertBenchmarkResult(b *testing.B, runner *Runner, ctx context.Context, expected int64) {
	b.Helper()

	value, err := runner.Run(ctx, RunOptions{})
	if err != nil {
		b.Fatalf("Run: %v", err)
	}
	if got := benchmarkInt64(b, value); got != expected {
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

func closeBenchmarkRunner(runner *Runner) {
	if runner == nil || runner.state == nil {
		return
	}

	runner.state.mu.Lock()
	handle := runner.state.handle
	runner.state.handle = nil
	runner.state.mu.Unlock()

	if handle != nil {
		handle.Close()
	}
}
