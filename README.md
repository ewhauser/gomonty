# gomonty

`gomonty` is an experimental standalone repository providing Go bindings to
[Monty](https://github.com/pydantic/monty). The Go package keeps the compatible
package name `monty`, while the Rust boundary is mapped to Monty's current
subprocess architecture through pinned Cargo git dependencies.

Documentation: https://pkg.go.dev/github.com/ewhauser/gomonty

## Status

- Experimental.
- Go module path: `github.com/ewhauser/gomonty`
- Upstream runtime: Monty `v0.0.19` (`e347739909877f4fb03877e23dd092286fc7e659`)
- Go bindings are cgo-free and use `purego` with an explicitly prepared shared
  library and version-matched worker executable
- Rust FFI crate: `crates/monty-go-ffi`
- Protocol worker crate: `crates/gomonty-worker`
- Upstream Monty source: pinned in the root [`Cargo.toml`](./Cargo.toml)
- Native executables are not stored in Git or in Go module ZIPs
- Reviewable release hashes: `internal/runtimebundle/manifests/current.json`
- Generated header: checked into `internal/ffi/include/monty_go_ffi.h`
- Linux GNU/glibc versus musl is detected from the running system before an
  asset is selected; ambiguous systems fail closed instead of guessing

Installing the Go module does not execute or fetch native code. Before first
use, the application owner explicitly chooses either a verified GitHub release
download or a local build from the reviewed, digest-pinned Rust source. Normal
calls such as `monty.New` never build or access the network.

Monty code executes in a child process managed by Monty's worker pool. This
provides crash isolation, process replacement, parent-side timeout enforcement,
and serializable session recovery. It is not an OS security sandbox: filesystem,
network, and other confinement still require a separate policy and sandboxing
layer.

## Repository Layout

- `*.go`, `vfs/`, `internal/ffi/`: copied Go bindings adapted to the root module layout
- [`go/README.md`](./go/README.md): consumer-facing Go API notes and examples carried over from the source repo
- `examples/`: standalone example module for local consumption examples
- `crates/monty-go-ffi/`: Rust C ABI adapter over `monty-pool`
- `crates/gomonty-worker/`: small version-matched Monty protocol child
- `cmd/gomonty/`: explicit `prepare download` and `prepare build` command
- `internal/runtimebundle/`: manifest, verification, cache, and release tooling
- `scripts/build-go-ffi.sh <target-triple>`: builds one target's shared library
  and worker for repository development and release automation

## Prepare the Native Runtime

Install the source-only module and preparation command at the same version:

```bash
go get github.com/ewhauser/gomonty@vX.Y.Z
go install github.com/ewhauser/gomonty/cmd/gomonty@vX.Y.Z
```

Then choose one explicit preparation mode:

```bash
# Download the exact target asset from the versioned GitHub release.
gomonty prepare download

# Or compile the digest-pinned Rust source with your local toolchain.
gomonty prepare build
```

The same operation is available from Go:

```go
prepared, err := monty.Prepare(ctx, monty.PrepareOptions{
	Mode: monty.PrepareDownload, // or monty.PrepareBuild
})
```

`prepare download` requires HTTPS and verifies the archive SHA-256 before
extracting it. It then verifies the size and SHA-256 of both the library and
worker. `prepare build` verifies the complete native source digest before
running the build script, enforces the manifest-pinned Rust and Cargo versions
and target standard library, then records the locally built files' hashes. This
mode deliberately trusts the caller's local Python, linker, and SDK; local
builds are not required to reproduce CI output byte-for-byte.

Published GNU/Linux downloads require glibc 2.35 or newer. They are built and
executed on both amd64 and arm64 Ubuntu 22.04 runners, and release assembly
rejects either native file when its imported GLIBC symbol version exceeds that
declared floor. Musl downloads do not use glibc. Users on an older GNU system
can choose `prepare build` to compile against their host instead.

Both modes use a cross-process lock and atomic staging. Before every `Dlopen` or
worker initialization, the loader rechecks the receipt and hashes of both files.
Missing, changed, extra, symlinked, or mismatched files fail closed with
`ErrRuntimeNotPrepared` or `ErrRuntimeIntegrity`. The `GOMONTY_CACHE_DIR`
environment variable selects an alternate cache root without disabling checks.

The verification boundary protects against accidental corruption, wrong release
assets, and unreviewed binary substitution in distribution. It is not code
signing and does not defend against a privileged local attacker able to modify
the application process or cache during use. Monty's subprocess boundary is
also not an OS sandbox.

Build preparation additionally requires Rust and Cargo 1.95.0, the manifest's
Rust target, and Python on `PATH` (or `PYO3_PYTHON`). `PrepareBuild` overrides a
caller-supplied `RUSTUP_TOOLCHAIN`, verifies the actual compiler versions, and
fails with the exact `rustup target add` command when the target standard
library is absent. Linux chooses GNU/glibc or musl by inspecting the running
system's ELF interpreter; users do not need a special Go build tag. The
standard `CARGO_TARGET_DIR` environment variable is respected when callers want
to reuse or isolate Cargo's compilation cache:

```bash
gomonty prepare build
go test ./...
```

## Consumer Example

After explicit preparation, import and use the library normally:

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

The same example lives in [`examples/cmd/example`](./examples/cmd/example). To
run it from this repository checkout:

```bash
go run ./cmd/gomonty prepare build --source .
cd examples
CGO_ENABLED=0 go run ./cmd/example
```

## Benchmarks

The Go benchmark suite mirrors the current upstream Monty benchmark cases so
the two projects exercise the same scripts and expected outputs. The shared
kitchen-sink workload is copied into [`testdata/bench_kitchen_sink.py`](./testdata/bench_kitchen_sink.py).

With a native runtime prepared, run the local Go-only benchmarks with:

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

The previous sample table measured the retired in-process v0.0.9 binding and is
intentionally not retained: subprocess startup, checkout reuse, and protocol
round trips make those numbers inapplicable to the v0.0.19 architecture. Run
the comparison script on the target host before publishing new results.

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

The native runner fuzzers require a prepared native runtime and run with
`CGO_ENABLED=0`. `FuzzValueJSON` remains pure Go.

## Upstream Overrides

The default build uses pinned git dependencies on `https://github.com/pydantic/monty.git`. For local development against a sibling checkout, you can temporarily override them with a Cargo patch:

```toml
[patch."https://github.com/pydantic/monty.git"]
monty = { path = "../monty/crates/monty" }
monty-pool = { path = "../monty/crates/monty-pool" }
monty-proto = { path = "../monty/crates/monty-proto" }
monty-types = { path = "../monty/crates/monty-types" }
monty-type-checking = { path = "../monty/crates/monty-type-checking" }
```

See [`RELEASING.md`](./RELEASING.md) for bumping the upstream pin and for the
exact-byte native runtime release flow.
