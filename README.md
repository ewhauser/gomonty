# gomonty

`gomonty` is an experimental standalone repository for the Go bindings to [Monty](https://github.com/pydantic/monty). The Go package keeps the copied binding API and package name `monty`, while the Rust FFI crate is split out so it can build against upstream Monty through pinned Cargo git dependencies.

## Status

- Experimental.
- Go module path: `github.com/ewhauser/gomonty`
- Rust FFI crate: `crates/monty-go-ffi`
- Upstream Monty source: pinned in the root [`Cargo.toml`](./Cargo.toml)
- Native archives: checked into `internal/ffi/lib/<target>`
- Generated header: checked into `internal/ffi/include/monty_go_ffi.h`

Tagged source trees must already contain the native archives required by the current cgo linking model. GitHub release assets are optional convenience copies, not the source of truth for Go module consumers.

## Repository Layout

- `*.go`, `vfs/`, `internal/ffi/`: copied Go bindings adapted to the root module layout
- [`go/README.md`](./go/README.md): consumer-facing Go API notes and examples carried over from the source repo
- `examples/`: standalone example module for local consumption examples
- `crates/monty-go-ffi/`: copied Rust C ABI crate
- `scripts/build-go-ffi.sh <target-triple>`: builds one target archive into `internal/ffi/lib/...`

## Build Notes

The Go package remains cgo-backed. Normal cgo builds link against a prebuilt static archive for the current target from `internal/ffi/lib/<target>`.

The `verify` workflow runs cgo-enabled Go tests on native Linux and macOS runners. Windows is currently build-only verification in CI, while still producing the tracked `windows/amd64` archive.

To build or refresh the host archive:

```bash
scripts/build-go-ffi.sh aarch64-apple-darwin
go test ./...
```

Requirements:

- Go 1.24+
- Rust toolchain
- Python available on `PATH`, or `PYO3_PYTHON` set explicitly
- `cbindgen` only when regenerating `internal/ffi/include/monty_go_ffi.h`

For repeat builds where the checked-in header does not need to change, set `MONTY_GO_FFI_SKIP_HEADER=1`.

## Consumer Example

For normal consumers, the intended path is to depend on a tagged version of this
repo whose source tree already contains the native archive for the consumer's
target platform.

Add the module:

```bash
go get github.com/ewhauser/gomonty@latest
```

Or in `go.mod`:

```go
require github.com/ewhauser/gomonty latest
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
go run ./cmd/example
```

If you are consuming a branch, local checkout, or unreleased commit instead of a
prepared tag, you may need to build or refresh the archive for your platform
first:

```bash
scripts/build-go-ffi.sh aarch64-apple-darwin
```

## Benchmarks

The Go benchmark suite mirrors the current upstream Monty benchmark cases so
the two projects exercise the same scripts and expected outputs. The shared
kitchen-sink workload is copied into [`testdata/bench_kitchen_sink.py`](./testdata/bench_kitchen_sink.py).

After building the host archive, run the local Go-only benchmarks with:

```bash
go test -run '^$' -bench BenchmarkMonty -benchmem
```

This covers the parse-once/repeated-run benchmark cases plus
`BenchmarkMontyEndToEnd` for parse-and-run in the loop.

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
measured from `gomonty` `83788efac34f-dirty` against upstream Monty
`982709bd52b1-dirty`:

| Case | gomonty | raw monty | Ratio |
| --- | ---: | ---: | ---: |
| `add_two` | `4.083 us` | `739 ns` | `5.53x` |
| `list_append` | `4.191 us` | `900 ns` | `4.66x` |
| `loop_mod_13` | `43.064 us` | `39.176 us` | `1.10x` |
| `kitchen_sink` | `9.219 us` | `4.283 us` | `2.15x` |
| `func_call_kwargs` | `4.644 us` | `1.085 us` | `4.28x` |
| `list_append_str` | `14.090 ms` | `14.902 ms` | `0.95x` |
| `list_append_int` | `4.951 ms` | `5.158 ms` | `0.96x` |
| `fib` | `22.299 ms` | `22.294 ms` | `1.00x` |
| `list_comp` | `34.926 us` | `30.921 us` | `1.13x` |
| `dict_comp` | `80.672 us` | `72.881 us` | `1.11x` |
| `empty_tuples` | `2.813 ms` | `2.813 ms` | `1.00x` |
| `pair_tuples` | `9.506 ms` | `9.952 ms` | `0.96x` |
| `end_to_end` | `6.058 us` | `1.952 us` | `3.10x` |

These numbers are host-specific. They compare the same benchmark scripts, but
the Go side uses `testing.B` while upstream uses Criterion.

## Fuzzing

The repo also includes Go fuzz targets for:

- `FuzzValueJSON`: pure-Go value wire-format decoding and normalization
- `FuzzCompileAndRun`: arbitrary source strings compiled and executed with tight resource limits
- `FuzzLoadRunner`: arbitrary bytes fed through `LoadRunner`, including valid dumped-runner seeds

Run a short fuzzing pass with:

```bash
go test -run '^$' -fuzz FuzzValueJSON -fuzztime=10s .
go test -run '^$' -fuzz FuzzCompileAndRun -fuzztime=10s .
go test -run '^$' -fuzz FuzzLoadRunner -fuzztime=10s .
```

The native runner fuzzers require a cgo-enabled build for the current host
archive, while `FuzzValueJSON` is pure Go.

## Upstream Overrides

The default build uses pinned git dependencies on `https://github.com/pydantic/monty.git`. For local development against a sibling checkout, you can temporarily override them with a Cargo patch:

```toml
[patch."https://github.com/pydantic/monty.git"]
monty = { path = "../monty/crates/monty" }
monty_type_checking = { path = "../monty/crates/monty-type-checking" }
```

See [`RELEASING.md`](./RELEASING.md) for bumping the upstream pin and for the release-prep workflow that refreshes the tracked archives and checksums before tagging.
