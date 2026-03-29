# gomonty

`gomonty` is an experimental standalone repository for the Go bindings to [Monty](https://github.com/pydantic/monty). The Go package keeps the copied binding API and package name `monty`, while the Rust FFI crate is split out so it can build against upstream Monty through pinned Cargo git dependencies.

Documentation: https://pkg.go.dev/github.com/ewhauser/gomonty

## Status

- Experimental.
- Go module path: `github.com/ewhauser/gomonty`
- Go bindings are cgo-free and use `purego` with bundled shared libraries
- Rust FFI crate: `crates/monty-go-ffi`
- Upstream Monty source: pinned in the root [`Cargo.toml`](./Cargo.toml)
- Native shared libraries: checked into `internal/ffi/lib/<target>`
- Generated header: checked into `internal/ffi/include/monty_go_ffi.h`
- Alpine/musl builds use a separate `musl` Go build tag and musl-specific shared libraries

Tagged source trees must already contain the native shared libraries required by the runtime loader. GitHub release assets are optional convenience copies, not the source of truth for Go module consumers.

## Repository Layout

- `*.go`, `vfs/`, `internal/ffi/`: copied Go bindings adapted to the root module layout
- [`go/README.md`](./go/README.md): consumer-facing Go API notes and examples carried over from the source repo
- `examples/`: standalone example module for local consumption examples
- `crates/monty-go-ffi/`: copied Rust C ABI crate
- `scripts/build-go-ffi.sh <target-triple>`: builds one target shared library into `internal/ffi/lib/...`

## Build Notes

The Go package is cgo-free. It uses `purego` to load a bundled shared library for the current target from `internal/ffi/lib/<target>`.

On first use, the loader extracts the embedded shared library to `os.UserCacheDir()` with an `os.TempDir()` fallback, then opens it with the platform loader.

Default Linux builds target the GNU/glibc shared libraries. Alpine and other musl-based Linux builds must opt into the musl family with the `musl` Go build tag.

The `verify` workflow runs `CGO_ENABLED=0` Go tests on native Linux, macOS, and Windows runners. Musl shared libraries are build-verified rather than executed in CI.

To build or refresh the shared library for the current host:

```bash
scripts/build-go-ffi.sh aarch64-apple-darwin
CGO_ENABLED=0 go test ./...
```

Requirements:

- Go 1.25+
- Rust toolchain
- Python available on `PATH`, or `PYO3_PYTHON` set explicitly
- `cbindgen` only when regenerating `internal/ffi/include/monty_go_ffi.h`

For repeat builds where the checked-in header does not need to change, set `MONTY_GO_FFI_SKIP_HEADER=1`.

For Alpine or another musl-based Linux environment:

```bash
scripts/build-go-ffi.sh x86_64-unknown-linux-musl
go test -tags musl ./...
```

## Consumer Example

For normal consumers, the intended path is to depend on a tagged version of this
repo whose source tree already contains the native shared library for the consumer's
target platform.

Add the module:

```bash
go get github.com/ewhauser/gomonty@latest
```

Or in `go.mod`:

```go
require github.com/ewhauser/gomonty vX.Y.Z
```

Then import and use it:

```go
package main

import (
	"context"
	"fmt"
	"log"

	monty "github.com/ewhauser/gomonty"
)

func main() {
	runner, err := monty.New("40 + 2", monty.CompileOptions{
		ScriptName: "example.py",
	})
	if err != nil {
		log.Fatal(err)
	}

	value, err := runner.Run(context.Background(), monty.RunOptions{})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(value.Raw())
}
```

The same example lives in [`examples/cmd/example`](./examples/cmd/example). To run it from this repo checkout:

```bash
cd examples
CGO_ENABLED=0 go run ./cmd/example
```

If you are consuming a branch, local checkout, or unreleased commit instead of a
prepared tag, you may need to build or refresh the shared library for your platform
first:

```bash
scripts/build-go-ffi.sh aarch64-apple-darwin
```

For Alpine or musl-based Linux consumers, also add the `musl` build tag when
building or testing your application:

```bash
go build -tags musl ./...
```

## Benchmarks

The Go benchmark suite mirrors the current upstream Monty benchmark cases so
the two projects exercise the same scripts and expected outputs. The shared
kitchen-sink workload is copied into [`testdata/bench_kitchen_sink.py`](./testdata/bench_kitchen_sink.py).

With a host shared library built, run the local Go-only benchmarks with:

```bash
CGO_ENABLED=0 go test -run '^$' -bench BenchmarkMonty -benchmem
```

This covers the parse-once/repeated-run benchmark cases plus
`BenchmarkMontyEndToEnd` for parse-and-run in the loop.

There are also Go-specific benchmark suites for wrapper overhead:

```bash
CGO_ENABLED=0 go test -run '^$' -bench BenchmarkMontyCallbacks -benchmem
CGO_ENABLED=0 go test -run '^$' -bench BenchmarkMontyDecompose -benchmem
```

These add:

- callback-heavy runs with repeated external function and OS handler calls
- low-level decomposition benchmarks for compile-only, dump/load, start-to-first-progress, name lookup, call resume, and pending resume paths

To capture CPU and allocation profiles for the representative hot paths, run:

```bash
scripts/profile-benchmarks.sh
```

By default the script writes profiles and `pprof -top` summaries to `/tmp/gomonty-bench-profiles` for:

- `BenchmarkMontyEndToEnd`
- `BenchmarkMonty/list_append_int`
- `BenchmarkMontyCallbacks/external_loop`

To compare `gomonty` against a local upstream Monty checkout on the same host,
run:

```bash
python3 scripts/compare-benchmarks.py --upstream ../monty
```

The comparison script:

- runs the Go benchmark suite and aggregates the median `ns/op` across three runs
- runs the upstream Criterion `__monty` benchmarks
- sets `PYO3_PYTHON` for the upstream run if the upstream checkout still expects a local `.venv/bin/python3`
- prints a Markdown table suitable for pasting back into this README

Current sample comparison from 2026-03-24 on `darwin/arm64` (`Apple M3 Max`),
measured from `gomonty` `dddae9616d8b-dirty` against upstream Monty
`982709bd52b1-dirty`:

| Case | gomonty | raw monty | Ratio |
| --- | ---: | ---: | ---: |
| `add_two` | `2.916 us` | `721 ns` | `4.04x` |
| `list_append` | `3.204 us` | `853 ns` | `3.76x` |
| `loop_mod_13` | `42.157 us` | `37.906 us` | `1.11x` |
| `kitchen_sink` | `7.942 us` | `4.035 us` | `1.97x` |
| `func_call_kwargs` | `3.501 us` | `1.045 us` | `3.35x` |
| `list_append_str` | `14.200 ms` | `14.557 ms` | `0.98x` |
| `list_append_int` | `4.855 ms` | `4.976 ms` | `0.98x` |
| `fib` | `20.547 ms` | `21.204 ms` | `0.97x` |
| `list_comp` | `32.750 us` | `29.786 us` | `1.10x` |
| `dict_comp` | `78.033 us` | `69.671 us` | `1.12x` |
| `empty_tuples` | `2.664 ms` | `2.794 ms` | `0.95x` |
| `pair_tuples` | `8.917 ms` | `9.111 ms` | `0.98x` |
| `end_to_end` | `5.240 us` | `1.891 us` | `2.77x` |

These numbers are host-specific. They compare the same benchmark scripts, but
the Go side uses `testing.B` while upstream uses Criterion.

## Fuzzing

The repo also includes Go fuzz targets for:

- `FuzzValueJSON`: pure-Go value wire-format decoding and normalization
- `FuzzCompileAndRun`: arbitrary source strings compiled and executed with tight resource limits
- `FuzzLoadRunner`: arbitrary bytes fed through `LoadRunner`, including valid dumped-runner seeds

Run a short fuzzing pass with:

```bash
CGO_ENABLED=0 go test -run '^$' -fuzz FuzzValueJSON -fuzztime=10s .
CGO_ENABLED=0 go test -run '^$' -fuzz FuzzCompileAndRun -fuzztime=10s .
CGO_ENABLED=0 go test -run '^$' -fuzz FuzzLoadRunner -fuzztime=10s .
```

The native runner fuzzers require a supported host shared library and run with
`CGO_ENABLED=0`. `FuzzValueJSON` remains pure Go.

## Upstream Overrides

The default build uses pinned git dependencies on `https://github.com/pydantic/monty.git`. For local development against a sibling checkout, you can temporarily override them with a Cargo patch:

```toml
[patch."https://github.com/pydantic/monty.git"]
monty = { path = "../monty/crates/monty" }
monty_type_checking = { path = "../monty/crates/monty-type-checking" }
```

See [`RELEASING.md`](./RELEASING.md) for bumping the upstream pin and for the single `make release` workflow that refreshes tracked shared libraries, tags the release, and warms the Go module proxy.
