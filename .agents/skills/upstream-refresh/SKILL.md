---
name: upstream-refresh
description: Refresh gomonty Go bindings against upstream pydantic/monty. Use this skill whenever the user wants to bump the monty dependency, add support for new upstream types, update the FFI wire format, sync with upstream changes, or prepare a release after upstream moves forward. Trigger on phrases like "upstream refresh", "bump monty", "new upstream types", "sync with upstream", "update monty pin", "release prep", or any mention of pydantic/monty changes that need to be reflected in gomonty.
---

# Upstream Refresh for gomonty

This skill walks through refreshing the Go bindings in gomonty against a newer version of the upstream [pydantic/monty](https://github.com/pydantic/monty) Python interpreter.

gomonty wraps upstream Monty through a Rust FFI crate (`crates/monty-go-ffi`) that serializes values across the boundary using a versioned MessagePack wire format. When upstream adds new types or changes its `MontyObject` enum, the wire format and Go bindings must be updated to match.

## Overview

The refresh touches four layers, always in this order:

```
Upstream Monty (MontyObject enum)
    |
    v
Rust FFI wire format (crates/monty-go-ffi/src/wire.rs)
    |
    v
Go wire format (wire.go)
    |
    v
Go public types (types.go)
```

Changes flow top-down. Never skip a layer — if upstream adds a variant, all four layers need updates.

## Step 1: Discover upstream changes

Find the currently pinned revision in `Cargo.toml` (the `rev` field on the `monty` dependency), then compare it against upstream `main`:

```bash
# Get the pinned rev
grep 'rev = ' Cargo.toml

# List commits since the pin
gh api 'repos/pydantic/monty/compare/<pinned-rev>...main' \
  --jq '.commits[] | "\(.sha[0:12]) \(.commit.message | split("\n")[0])"'
```

Use the GitHub API (via `gh`) to get the full SHA of the target commit — short SHAs from the commits list are truncated and will fail Cargo resolution:

```bash
gh api 'repos/pydantic/monty/commits?per_page=1' --jq '.[0].sha'
```

## Step 2: Classify changes by FFI impact

Not every upstream commit affects the Go bindings. Classify each commit:

**FFI-affecting** (requires wire format + Go changes):
- New `MontyObject` variants (new Python types like `Date`, `DateTime`, etc.)
- Changed fields on existing variants (renamed/added/removed fields)
- New `ExcType` variants
- Changes to `run_progress.rs`, `object.rs`, or the public API surface

**Internal-only** (just bump the pin, no binding changes):
- New builtin modules that don't introduce new types (e.g., `json`, `math`)
- New string/bytes methods
- Compiler/VM optimizations
- Heap/memory management refactors
- CI/tooling changes

To identify FFI-affecting changes, check the PR file lists for changes to `object.rs`, `run_progress.rs`, and `convert.rs` (the Python/JS converters — if they handle a new variant, we need to as well):

```bash
gh api 'repos/pydantic/monty/pulls/<PR-number>/files' --jq '.[].filename'
```

For new `MontyObject` variants, read the struct definitions from upstream to understand the exact fields:

```bash
gh api 'repos/pydantic/monty/contents/crates/monty/src/object.rs?ref=main' \
  --jq '.content' | base64 -d | grep -B2 -A20 'pub struct MontyFoo'
```

## Step 3: Bump the upstream pin

Update both `rev` values in `Cargo.toml` (they must stay aligned):

```toml
monty = { git = "https://github.com/pydantic/monty.git", rev = "<full-sha>" }
monty_type_checking = { git = "https://github.com/pydantic/monty.git", package = "monty_type_checking", rev = "<full-sha>" }
```

Use the **full 40-character SHA** — Cargo will fail to resolve truncated SHAs.

## Step 4: Update the Rust wire format

File: `crates/monty-go-ffi/src/wire.rs`

This file defines the binary wire protocol between Rust and Go. For each new or changed `MontyObject` variant:

### 4a: Wire constants

Add new `WIRE_VALUE_*` constants, continuing the sequence after the last existing constant:

```rust
pub const WIRE_VALUE_NEW_TYPE: u8 = <next-number>;
```

### 4b: WireValue fields

Add fields to the `WireValue` struct for any new data the type carries. Use `serde` skip attributes to keep the wire format compact:

```rust
#[serde(default, skip_serializing_if = "is_zero_i32")]
pub new_field: i32,
```

Reuse existing fields where the semantics match (e.g., `string_value` for string-like data). Only add new fields when the type carries data that doesn't map to any existing field.

Add any needed zero-check helper functions (`is_zero_*`), but check for duplicates first — the file already has helpers for common types.

### 4c: from_monty / into_monty

Add match arms in both directions:

- `from_monty`: converts `MontyObject` -> `WireValue` (serialization, Rust to Go)
- `into_monty`: converts `WireValue` -> `MontyObject` (deserialization, Go to Rust)

For output-only types (like `Repr` or `Cycle`), `into_monty` should return an error since they can't be used as inputs.

### 4d: Update imports

Add any new types to the `use monty::{...}` import at the top of the file, and in the `#[cfg(test)] mod tests` block.

### 4e: Rust tests

Add round-trip tests for each new type. The pattern is:

```rust
#[test]
fn wire_value_round_trips_new_type() {
    let original = MontyObject::NewType(MontyNewType { ... });
    let decoded = WireValue::from_monty(&original)
        .into_monty()
        .expect("new type should round-trip");
    assert_eq!(decoded, original);
}
```

Test edge cases: zero values, optional fields as `None` vs `Some`, negative values where applicable.

### Handling removed or renamed variants

If upstream removes a `MontyObject` variant:
- Remove the corresponding `WIRE_VALUE_*` constant, but do NOT renumber existing constants (wire compatibility)
- Remove the `from_monty` / `into_monty` arms
- Remove any `WireValue` fields that are no longer used by any variant

If upstream renames fields on an existing variant:
- Update the `from_monty` / `into_monty` arms to use the new field names
- The `WireValue` field names (which are the msgpack keys) can stay the same to maintain wire compatibility, or bump `WIRE_VERSION` if a breaking change is needed

## Step 5: Update the Go wire format

File: `wire.go`

Mirror every change from Step 4:

### 5a: Wire constants

```go
const (
    // ... existing constants using iota ...
    wireValueNewType
)
```

The Go constants use `iota` so they auto-number — just add new ones at the end in the same order as the Rust constants.

### 5b: wireValue fields

```go
type wireValue struct {
    // ... existing fields ...
    NewField int32 `msgpack:"new_field,omitempty"`
}
```

Field names and msgpack tags must match the Rust `WireValue` serde names exactly.

### 5c: wireValueFromPublic / toPublic

Add cases in both `wireValueFromPublic` (Go Value -> wireValue) and `toPublic` (wireValue -> Go Value) for each new type. Place them before the `default` case.

## Step 6: Update Go public types

File: `types.go`

### 6a: Value kinds

```go
const (
    // ... existing kinds ...
    valueKindNewType ValueKind = "new_type"
)
```

### 6b: Struct definitions

Define a Go struct for each new upstream type. Match the upstream field names and types:

| Rust type | Go type |
|-----------|---------|
| `i32` | `int32` |
| `u8` | `uint8` |
| `u32` | `uint32` |
| `i64` | `int64` |
| `f64` | `float64` |
| `String` | `string` |
| `Option<T>` | `*T` |
| `Vec<T>` | `[]T` |

Include JSON struct tags for the JSON marshal/unmarshal path.

### 6c: Value constructor

```go
func NewTypeValue(val NewType) Value {
    return Value{kind: valueKindNewType, data: val}
}
```

### 6d: Accessor method

```go
func (v Value) NewType() (NewType, bool) {
    value, ok := v.data.(NewType)
    return value, ok
}
```

### 6e: ValueOf case

Add a case in the `ValueOf` switch for the new Go struct type.

### 6f: JSON MarshalJSON / UnmarshalJSON

Add cases in both `MarshalJSON` and `UnmarshalJSON` on the `Value` type. Follow the existing pattern — marshal with an inline struct containing a `Kind` field, unmarshal by switching on the kind discriminant.

### 6g: String()

Add a case in the `String()` method. Format should match Python's representation where reasonable.

## Step 7: Verify

### Rust

```bash
cargo test -p monty-go-ffi
```

All existing and new wire round-trip tests must pass.

### Go

```bash
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
```

### Local native build

Build the FFI for the local platform to verify the full stack:

```bash
MONTY_GO_FFI_SKIP_HEADER=1 scripts/build-go-ffi.sh aarch64-apple-darwin
CGO_ENABLED=0 go test ./...
```

## Step 8: Branch, commit, push, and create PR

Create a feature branch, commit all changes (including `Cargo.lock` and the locally-rebuilt shared library), push, and open a PR:

```bash
git checkout -b ewhauser/<descriptive-branch-name>
git add Cargo.lock Cargo.toml crates/monty-go-ffi/src/wire.rs \
  internal/ffi/lib/darwin_arm64/libmonty_go_ffi.dylib \
  types.go types_test.go wire.go
git commit -m "Support upstream <feature> types ..."
git push -u origin ewhauser/<branch-name>
gh pr create --title "..." --body "..."
```

## Step 9: Merge PR and trigger the release workflow

After the PR is reviewed and merged:

1. Trigger the single release entrypoint from a local checkout:

```bash
make release
```

2. The Make target fetches tags from `origin`, computes the next patch semver
   tag, and dispatches the `release.yml` GitHub Actions workflow on `main`. If
   a non-patch version is required, pass `VERSION=vX.Y.Z`.

3. The workflow builds for: darwin-arm64, linux-amd64, linux-arm64, linux-amd64-musl, linux-arm64-musl, windows-amd64.

4. It updates the tracked release files on `main`, tags the release, creates the
   GitHub release with a commit-by-commit changelog since the previous tag, and
   warms the Go proxy so `pkg.go.dev` can discover the new version.

**Important**: The code changes PR must be merged to `main` before triggering the
release workflow, because the workflow runs from `main` and publishes directly
from that branch.

## Step 10: Tag and release

The workflow handles the version bump in `Cargo.toml`, shared-library refresh,
tag creation, GitHub release creation, and Go proxy warm-up. The shared
libraries still must be committed in the tagged tree because Go module
consumers fetch the tagged source via `go get` — they don't download GitHub
release assets. The release assets remain optional convenience copies.

## Common pitfalls

- **Truncated SHA**: Always use the full 40-char commit SHA in `Cargo.toml`. The GitHub API list endpoint returns truncated SHAs — use `gh api repos/pydantic/monty/commits/<short-sha> --jq .sha` to get the full one.
- **Duplicate helpers**: Before adding `is_zero_*` functions in `wire.rs`, check if one already exists — the compiler will reject duplicates.
- **Wire constant ordering**: Go uses `iota` so constants must be in the same order as the Rust numeric values. Never renumber existing constants.
- **WireValue field reuse**: The `TimeZone` type reuses the `days` wire field for `offset_seconds` and `timezone_name` for `name`. This is intentional to keep the struct flat. When adding new types, check if existing fields can serve double duty before adding new ones.
- **Optional vs zero**: Rust `Option<T>` maps to Go `*T` (pointer). A zero value and an absent value are different — use pointer types for fields where `None` carries distinct meaning from the zero value.
