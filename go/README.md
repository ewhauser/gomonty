# Monty Go Bindings

`github.com/ewhauser/gomonty` exposes Monty as a Go package with:

- runner and REPL APIs
- high-level host callback dispatch for external functions
- low-level pause/resume snapshots
- a typed OS/filesystem callback surface in `github.com/ewhauser/gomonty/vfs`

## Status

This package is currently experimental.

It is also `cgo`-backed. Builds succeed only when the module includes a bundled native archive for the target platform under `internal/ffi/lib/...`.

The code is wired for these targets:

- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`
- `windows/amd64`

If the archive for your target is missing, `go build` or `go test` will fail at link time.

## Requirements

- Go 1.24+
- `CGO_ENABLED=1`
- a repo/tag that includes the native archive for your target

## Install

```go
require github.com/ewhauser/gomonty latest
```

## Quick Start

This example shows:

- compiling Monty code with `monty.New`
- handling an external function in Go
- providing a Go-owned filesystem and environment
- capturing `print()` output

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	monty "github.com/ewhauser/gomonty"
	"github.com/ewhauser/gomonty/vfs"
)

func main() {
	fs := vfs.NewMemoryFS()
	fs.AddText("/data/input.txt", "hello from go")

	runner, err := monty.New(`
from pathlib import Path

def run():
    text = Path('/data/input.txt').read_text()
    total = host_add(20, 22)
    print(text)
    return f'{text}:{total}'

run()
`, monty.CompileOptions{
		ScriptName: "example.py",
	})
	if err != nil {
		log.Fatal(err)
	}

	value, err := runner.Run(context.Background(), monty.RunOptions{
		Functions: map[string]monty.ExternalFunction{
			"host_add": func(ctx context.Context, call monty.Call) (monty.Result, error) {
				lhs := call.Args[0].Raw().(int64)
				rhs := call.Args[1].Raw().(int64)
				return monty.Return(monty.Int(lhs + rhs)), nil
			},
		},
		OS: vfs.Handler(fs, vfs.MapEnvironment{
			"HOME": "/sandbox",
		}),
		Print: monty.WriterPrintCallback(os.Stdout),
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(value.Raw().(string))
}
```

## Custom Filesystem From Go

If you want Monty code to use a Go-defined filesystem, implement `vfs.FileSystem` and pass it through `vfs.Handler(...)`.

```go
type MyFS struct{}

func (fs *MyFS) Exists(path string) (bool, error)                  { ... }
func (fs *MyFS) IsFile(path string) (bool, error)                  { ... }
func (fs *MyFS) IsDir(path string) (bool, error)                   { ... }
func (fs *MyFS) IsSymlink(path string) (bool, error)               { ... }
func (fs *MyFS) ReadText(path string) (string, error)              { ... }
func (fs *MyFS) ReadBytes(path string) ([]byte, error)             { ... }
func (fs *MyFS) WriteText(path string, data string) (int, error)   { ... }
func (fs *MyFS) WriteBytes(path string, data []byte) (int, error)  { ... }
func (fs *MyFS) Mkdir(path string, parents bool, existOK bool) error { ... }
func (fs *MyFS) Unlink(path string) error                          { ... }
func (fs *MyFS) Rmdir(path string) error                           { ... }
func (fs *MyFS) Iterdir(path string) ([]string, error)             { ... }
func (fs *MyFS) Stat(path string) (monty.StatResult, error)        { ... }
func (fs *MyFS) Rename(oldPath string, newPath string) error       { ... }
func (fs *MyFS) Resolve(path string) (string, error)               { ... }
func (fs *MyFS) Absolute(path string) (string, error)              { ... }
```

Then:

```go
handler := vfs.Handler(&MyFS{}, vfs.MapEnvironment{
	"HOME": "/sandbox",
})

value, err := runner.Run(ctx, monty.RunOptions{
	OS: handler,
})
```

`vfs.Handler` maps common Go filesystem errors into Python-style exceptions such as `FileNotFoundError`.

## Values and Results

Public APIs use `monty.Value` as a tagged union.

Common constructors:

- `monty.None()`
- `monty.Bool(...)`
- `monty.Int(...)`
- `monty.Float(...)`
- `monty.String(...)`
- `monty.Bytes(...)`
- `monty.List(...)`
- `monty.TupleValue(...)`
- `monty.DictValue(...)`
- `monty.PathValue(...)`
- `monty.DataclassValue(...)`

You can also convert ordinary Go values with `monty.ValueOf(...)` or `monty.MustValueOf(...)`.

Host callbacks return `monty.Result`:

- `monty.Return(value)` for success
- `monty.Raise(monty.Exception{...})` to raise a Python exception
- `monty.Pending(waiter)` for async work

## Async External Functions

High-level `Run` and `FeedRun` support pending external calls. Return `monty.Pending(...)` from your callback and implement:

```go
type waiter struct{}

func (w waiter) Wait(ctx context.Context) monty.Result {
	return monty.Return(monty.String("done"))
}
```

Then:

```go
"fetch": func(ctx context.Context, call monty.Call) (monty.Result, error) {
	return monty.Pending(waiter{}), nil
},
```

The helper loop will wait for the result and resume Monty automatically.

## Low-Level Pause/Resume API

If you want full control over dispatch, use `Start` / `FeedStart` directly.

```go
progress, err := runner.Start(ctx, monty.StartOptions{})
if err != nil {
	log.Fatal(err)
}

for {
	switch current := progress.(type) {
	case *monty.Snapshot:
		progress, err = current.ResumeReturn(ctx, monty.String("ok"))
	case *monty.NameLookupSnapshot:
		progress, err = current.ResumeUndefined(ctx)
	case *monty.FutureSnapshot:
		progress, err = current.ResumeResults(ctx, map[uint32]monty.Result{})
	case *monty.Complete:
		fmt.Println(current.Output)
		return
	}
	if err != nil {
		log.Fatal(err)
	}
}
```

Snapshots and runners are serializable with:

- `Runner.Dump()` / `LoadRunner(...)`
- `Snapshot.Dump()` / `LoadSnapshot(...)`
- `LoadReplSnapshot(...)`
- `Repl.Dump()`

## Errors

Monty errors are returned as typed Go errors:

- `*monty.SyntaxError`
- `*monty.RuntimeError`
- `*monty.TypingError`

Host callback errors returned as ordinary Go `error` values are converted to runtime exceptions unless you return an explicit `monty.Raise(...)`.
