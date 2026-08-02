# Release Process

GoMonty distributes reviewable Go and Rust source through Git and Go module
tags. Native runtime pairs are distributed only as immutable GitHub release
assets. They are never committed to Git or embedded in a Go module ZIP.

Build and publication are separate: publication promotes the exact bytes that a
named CI run already built, hashed, verified, and attested. It never rebuilds.

## Upstream Monty Pin

`crates/monty-go-ffi` and `crates/gomonty-worker` build against the pinned Monty
git dependencies in `Cargo.toml`:

- `monty`
- `monty-pool`
- `monty-proto`
- `monty-types`
- `monty-type-checking`

All five dependencies must use the same exact release commit because the FFI
library and worker negotiate Monty's versioned subprocess protocol. The Rust
toolchain is pinned in both `rust-toolchain.toml` and the release manifest;
build preparation rejects a compiler, Cargo version, or target standard library
that does not match. Hosted GNU, Darwin, and Windows builders pin and verify
Python 3.12.10, the newest exact 3.12 patch published by `actions/python-versions`
for every supported native platform. Musl CI independently pins Alpine Python
3.12.13-r0 and uses separate Go 1.25.0 and Rust 1.95.0 Alpine images by immutable
multi-platform digest. Every explicitly installed Alpine build package is pinned;
missing pinned packages fail the build instead of silently selecting newer tools. The Go
consumer is compiled inside Alpine because the pure-Go FFI loader still links
to the host libc: an Ubuntu cross-build is a glibc executable even when
`CGO_ENABLED=0`, and therefore is not a valid musl proof.

When changing Monty or either native crate:

1. Update all five Cargo revisions together and refresh `Cargo.lock`.
2. Choose a new `runtime_version` and `release_tag` in
   `internal/runtimebundle/manifests/current.json`. Never reuse a published tag.
3. Update the Monty version, commit, and target asset names in that manifest.
4. Generate candidate hashes, then prove the complete feature branch before
   submitting it.

Local Cargo patches are acceptable during development but must be removed before
calculating `native_source_sha256` or building release assets.

## 1. Prove a Feature Branch Without Publishing

Push the feature branch, then run:

```bash
make runtime-release-check
```

This dispatches the registered `verify` workflow on the feature branch. Manual
verification calls the branch's read-only `runtime-release-build` reusable
workflow, so new release automation can be tested before its dispatch wrapper
exists on the default branch. It builds the shared library and worker on
compatible builders for:

- `darwin/arm64`
- `linux/amd64` GNU/glibc
- `linux/arm64` GNU/glibc
- `linux/amd64` musl
- `linux/arm64` musl
- `windows/amd64`

It then:

- assembles deterministic ZIPs from those exact build outputs
- calculates each archive's size and SHA-256
- calculates the size and SHA-256 of both files inside every archive
- calculates a deterministic digest of every reviewed native build input
- verifies the archives through the production extractor and verifier
- loads and executes every generated archive on its destination OS, architecture,
  and libc, including both musl targets
- proves no native executable is tracked in Git
- runs source-only Go race tests, vet, and `git diff --check`
- creates provenance attestations for the exact ZIPs and manifest
- retains one `runtime-release-package` Actions artifact containing those exact
  bytes, the build run ID, and source commit

Proof mode cannot push, open a PR, tag, or publish. Download the generated
manifest from the retained artifact when the feature branch needs platform
hashes that cannot be generated on one local host, and review its diff before
committing it to the feature branch.

## 2. Rebuild on Protected Main

After the feature PR merges, dispatch the publication candidate from protected
`main`:

```bash
make runtime-release-pr
```

Only `main` is allowed to use `open_pr=true`. The workflow repeats every build
and verification step. If generated source metadata or hashes differ from
merged `main`, it commits only these reviewable files to a release-preparation
PR:

- `Cargo.lock`
- `internal/ffi/include/monty_go_ffi.h`
- `internal/runtimebundle/manifests/current.json`

Native executables remain in the retained Actions artifact. If the files are
already identical, no PR is needed and the successful run itself is the
publication candidate. In either case, record the workflow run ID. If a prep PR
was opened, merge it without rewriting away the preparation run's source commit;
the publish workflow requires that commit to remain an ancestor of `main`.

## 3. Promote the Exact Bytes

Enable release immutability in the repository's GitHub settings before the first
runtime release. The workflow refuses to create a release when the setting is
disabled.

Also create a `native-runtime-release` GitHub Actions environment, restrict its
deployment branches to protected `main`, and require a human reviewer. The
publication job references that environment and also rejects dispatches whose
workflow ref is not `main`.

After any release-preparation PR merges, publish from its recorded run:

```bash
make publish-runtime PREP_RUN_ID=1234567890
```

`publish-native-runtime`:

- requires the named run to be a successful `runtime-release-prep` run
- requires its source commit to be an ancestor of current protected `main`
- downloads `runtime-release-package` from that exact run, never a latest run
- requires its manifest to be byte-identical to merged `main`
- re-verifies every archive and internal file hash
- creates another provenance attestation for the promoted bytes
- creates a draft release and uploads the retained files without rebuilding
- compares GitHub's reported `sha256:` digest for every uploaded asset with the
  local digest and rejects missing or extra assets
- publishes only after every digest agrees
- verifies that the resulting release reports `immutable: true`

If publication fails while the release is a draft, inspect and delete that draft
before a deliberate retry. Never replace a published asset, move its tag, or
reuse its runtime version; fix forward with a new version.

## Consumer Verification

After publication, verify from a clean module and cache boundary:

```bash
go install github.com/ewhauser/gomonty/cmd/gomonty@vX.Y.Z
gomonty prepare download
```

Run a fresh consumer that evaluates `40 + 2` and confirm that its worker PID
differs from the host PID. Download preparation must request the exact tagged
asset URL in the committed manifest and fail before `Dlopen` or worker startup
when any archive, file, receipt, target, or version check differs.

The publication workflow repeats that clean-cache public-download, hash, load,
distinct-worker, and evaluation proof on all six supported targets after the
immutable release becomes visible. A successful publish job without all six
public verification jobs is not a completed runtime release.

`prepare build` is a separate trust choice. It verifies the reviewed source
digest and manifest-pinned Rust/Cargo version and target before executing the
build, then records and rechecks the local outputs. It still trusts the user's
Python, linker, and SDK and does not require local bytes to reproduce hosted
builder output byte-for-byte. Standard `CARGO_TARGET_DIR` is respected for an
explicitly managed compilation cache.

## 4. Tag the Go Module

Create the next Go module tag only after the immutable runtime release and all
six public-download jobs are green. The module tag must point at the same
protected-main commit used by the runtime publication workflow, and its
committed manifest must name that immutable runtime release. Never tag a module
whose default download path has not been proven from a clean cache.

Finally, fetch the tagged module from a fresh module cache, confirm its ZIP has
no native executable, install `cmd/gomonty` at that exact tag, and repeat the
download plus distinct-worker `40 + 2` consumer proof. Source and runtime tags
are never moved or reused; failures are fixed forward with new versions.
