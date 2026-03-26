# Release Process

## Upstream Monty Pin

`crates/monty-go-ffi` builds against upstream Monty through the pinned git
dependencies in the root `Cargo.toml`:

- `monty`
- `monty_type_checking`

To bump the upstream dependency:

1. Update the `rev` for both dependencies in `Cargo.toml`.
2. Refresh `Cargo.lock` with `cargo update`.
3. Rebuild the host shared library and run local verification:

```bash
MONTY_GO_FFI_SKIP_HEADER=1 scripts/build-go-ffi.sh aarch64-apple-darwin
CGO_ENABLED=0 go test ./...
```

If you want to develop against a local Monty checkout instead of the pinned git
dependency, use a temporary Cargo patch:

```toml
[patch."https://github.com/pydantic/monty.git"]
monty = { path = "../monty/crates/monty" }
monty_type_checking = { path = "../monty/crates/monty-type-checking" }
```

## Release-Prep Workflow

Before tagging, run the `release-prep` GitHub Actions workflow. It:

- builds the tracked shared libraries:
  - `darwin/arm64`
  - `linux/amd64` (GNU/glibc)
  - `linux/arm64` (GNU/glibc)
  - `linux/amd64` (musl/Alpine)
  - `linux/arm64` (musl/Alpine)
  - `windows/amd64`
- regenerates `internal/ffi/include/monty_go_ffi.h` exactly once
- updates `internal/ffi/lib/...`
- refreshes `internal/ffi/checksums.txt`
- opens a release-prep branch and pull request automatically

The workflow is the default path because the repo must contain the updated
shared libraries before a tag is created.

Current CI coverage:

- native `CGO_ENABLED=0` Go tests on Linux, macOS, and Windows
- build verification for musl Linux shared libraries

## Why The Shared Libraries Must Be Committed Before Tagging

Go module consumers fetch the tagged source tree. They do not fetch GitHub
release assets as part of `go get`.

Because the current runtime loader embeds the checked-in shared libraries under
`internal/ffi/lib/<target>`, the tag itself must already contain the correct
shared libraries and header. Release assets are optional convenience copies
only.

## Manual Shared Library Refresh

If you need to refresh artifacts locally instead of using the workflow, build
each supported target explicitly on a compatible host:

```bash
scripts/build-go-ffi.sh aarch64-apple-darwin
scripts/build-go-ffi.sh aarch64-unknown-linux-gnu
scripts/build-go-ffi.sh aarch64-unknown-linux-musl
scripts/build-go-ffi.sh x86_64-unknown-linux-gnu
scripts/build-go-ffi.sh x86_64-unknown-linux-musl
scripts/build-go-ffi.sh x86_64-pc-windows-msvc
```

Commit the updated files:

- `Cargo.lock`
- `internal/ffi/include/monty_go_ffi.h`
- `internal/ffi/lib/...`
- `internal/ffi/checksums.txt`

## Tagging

After the release-prep PR is merged and CI is green:

1. Create and push the release tag, for example `v0.0.8`.
2. Optionally create a GitHub release and upload the same shared libraries and checksum
   file for convenience.
