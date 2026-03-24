package monty

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

const fuzzScriptName = "fuzz.py"

var fuzzRunLimits = &ResourceLimits{
	MaxAllocations:    1_000_000,
	MaxDuration:       50 * time.Millisecond,
	MaxMemory:         8 << 20,
	MaxRecursionDepth: 128,
}

func FuzzValueJSON(f *testing.F) {
	f.Add([]byte(`{"kind":"none"}`))
	f.Add([]byte(`{"kind":"int","value":42}`))
	f.Add([]byte(`{"kind":"big_int","value":"1180591620717411303424"}`))
	f.Add([]byte(`{"kind":"dict","items":[{"key":{"kind":"string","value":"answer"},"value":{"kind":"int","value":42}}]}`))
	f.Add([]byte(`{"kind":"named_tuple","type_name":"StatResult","field_names":["st_mode","st_ino","st_dev","st_nlink","st_uid","st_gid","st_size","st_atime","st_mtime","st_ctime"],"values":[{"kind":"int","value":33188},{"kind":"int","value":1},{"kind":"int","value":2},{"kind":"int","value":1},{"kind":"int","value":10},{"kind":"int","value":20},{"kind":"int","value":123},{"kind":"float","value":1.5},{"kind":"float","value":2.5},{"kind":"float","value":3.5}]}`))
	f.Add([]byte("not json"))

	f.Fuzz(func(t *testing.T, data []byte) {
		var value Value
		if err := json.Unmarshal(data, &value); err != nil {
			return
		}

		_ = value.Kind()
		_ = value.Raw()
		_, _ = value.StatResult()
		_, _ = value.Function()
		_, _ = value.Exception()
		_ = value.String()

		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal decoded value: %v", err)
		}

		var decoded Value
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal normalized value: %v", err)
		}

		encodedAgain, err := json.Marshal(decoded)
		if err != nil {
			t.Fatalf("re-marshal normalized value: %v", err)
		}

		if string(encoded) != string(encodedAgain) {
			t.Fatalf("non-stable value encoding:\n%s\n%s", encoded, encodedAgain)
		}
	})
}

func FuzzCompileAndRun(f *testing.F) {
	f.Add("40 + 2")
	f.Add(benchKitchenSink)
	f.Add(benchFib25)
	f.Add(benchListAppendInt)
	f.Add("def outer(x):\n    def inner(y):\n        return x + y\n    return inner(2)\nouter(40)")

	f.Fuzz(func(t *testing.T, code string) {
		if len(code) > 4096 {
			t.Skip()
		}

		runner, err := New(code, CompileOptions{ScriptName: fuzzScriptName})
		if err != nil {
			return
		}
		defer closeTestRunner(runner)

		dump, err := runner.Dump()
		if err != nil {
			t.Fatalf("Dump: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, _ = runner.Run(ctx, RunOptions{Limits: fuzzRunLimits})

		loaded, err := LoadRunner(dump)
		if err != nil {
			t.Fatalf("LoadRunner(Dump()): %v", err)
		}
		defer closeTestRunner(loaded)

		_, _ = loaded.Run(ctx, RunOptions{Limits: fuzzRunLimits})
	})
}

func FuzzLoadRunner(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x01, 0x02, 0x03})
	addSerializedRunnerSeed(f, "40 + 2")
	addSerializedRunnerSeed(f, benchKitchenSink)
	addSerializedRunnerSeed(f, "def fib(n):\n    if n <= 1:\n        return n\n    return fib(n - 1) + fib(n - 2)\n\nfib(10)")

	f.Fuzz(func(t *testing.T, data []byte) {
		runner, err := LoadRunner(data)
		if err != nil {
			return
		}
		defer closeTestRunner(runner)

		dump, err := runner.Dump()
		if err != nil {
			t.Fatalf("Dump loaded runner: %v", err)
		}

		loaded, err := LoadRunner(dump)
		if err != nil {
			t.Fatalf("LoadRunner(normalized dump): %v", err)
		}
		defer closeTestRunner(loaded)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, _ = runner.Run(ctx, RunOptions{Limits: fuzzRunLimits})
		_, _ = loaded.Run(ctx, RunOptions{Limits: fuzzRunLimits})
	})
}

func addSerializedRunnerSeed(f *testing.F, code string) {
	runner, err := New(code, CompileOptions{ScriptName: fuzzScriptName})
	if err != nil {
		return
	}
	defer closeTestRunner(runner)

	dump, err := runner.Dump()
	if err != nil {
		return
	}
	f.Add(dump)
}
