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

If you are consuming a branch, local checkout, or unreleased commit instead of a
prepared tag, you may need to build or refresh the archive for your platform
first:

```bash
scripts/build-go-ffi.sh aarch64-apple-darwin
```

## Upstream Overrides

The default build uses pinned git dependencies on `https://github.com/pydantic/monty.git`. For local development against a sibling checkout, you can temporarily override them with a Cargo patch:

```toml
[patch."https://github.com/pydantic/monty.git"]
monty = { path = "../monty/crates/monty" }
monty_type_checking = { path = "../monty/crates/monty-type-checking" }
```

See [`RELEASING.md`](./RELEASING.md) for bumping the upstream pin and for the release-prep workflow that refreshes the tracked archives and checksums before tagging.
