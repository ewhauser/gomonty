#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CRATE_DIR="$ROOT_DIR/crates/monty-go-ffi"
HEADER_PATH="$ROOT_DIR/internal/ffi/include/monty_go_ffi.h"
LIB_ROOT="$ROOT_DIR/internal/ffi/lib"
SKIP_HEADER="${MONTY_GO_FFI_SKIP_HEADER:-0}"

if ! command -v cargo >/dev/null 2>&1; then
  echo "cargo is required" >&2
  exit 1
fi

if [[ -z "${PYO3_PYTHON:-}" ]]; then
  for candidate in python3 python; do
    if command -v "$candidate" >/dev/null 2>&1; then
      export PYO3_PYTHON
      PYO3_PYTHON="$(command -v "$candidate")"
      break
    fi
  done
fi

if [[ -z "${PYO3_PYTHON:-}" ]]; then
  echo "set PYO3_PYTHON or install python3/python on PATH for PyO3 dependency discovery" >&2
  exit 1
fi

if [[ "$SKIP_HEADER" != "1" ]] && ! command -v cbindgen >/dev/null 2>&1; then
  echo "cbindgen is required to refresh $HEADER_PATH" >&2
  exit 1
fi

if [[ $# -ne 1 ]]; then
  cat >&2 <<'EOF'
usage: scripts/build-go-ffi.sh <target-triple>

Supported targets:
  aarch64-apple-darwin
  aarch64-unknown-linux-gnu
  aarch64-unknown-linux-musl
  x86_64-unknown-linux-gnu
  x86_64-unknown-linux-musl
  x86_64-pc-windows-msvc
EOF
  exit 1
fi

target="$1"
case "$target" in
  aarch64-apple-darwin)
    lib_dir="$LIB_ROOT/darwin_arm64"
    lib_name="libmonty_go_ffi.dylib"
    ;;
  aarch64-unknown-linux-gnu)
    lib_dir="$LIB_ROOT/linux_arm64"
    lib_name="libmonty_go_ffi.so"
    ;;
  aarch64-unknown-linux-musl)
    lib_dir="$LIB_ROOT/linux_arm64_musl"
    lib_name="libmonty_go_ffi.so"
    ;;
  x86_64-unknown-linux-gnu)
    lib_dir="$LIB_ROOT/linux_amd64"
    lib_name="libmonty_go_ffi.so"
    ;;
  x86_64-unknown-linux-musl)
    lib_dir="$LIB_ROOT/linux_amd64_musl"
    lib_name="libmonty_go_ffi.so"
    ;;
  x86_64-pc-windows-msvc)
    lib_dir="$LIB_ROOT/windows_amd64"
    lib_name="monty_go_ffi.dll"
    ;;
  *)
    echo "unsupported target: $target" >&2
    exit 1
    ;;
esac

host_os="$(uname -s)"

if [[ "$target" == *-unknown-linux-musl ]]; then
  export RUSTFLAGS="${RUSTFLAGS:+$RUSTFLAGS }-C target-feature=-crt-static"

  if [[ "$host_os" != "Linux" ]]; then
    echo "building shared libraries for $target requires a compatible Linux host; use the release-prep workflow or a Linux builder" >&2
    exit 1
  fi

  # On a musl-native host (for example Alpine), the default compiler already
  # targets musl and can link the shared library directly.
  if [[ "${MONTY_GO_FFI_MUSL_NATIVE:-0}" != "1" ]]; then
    if ! command -v musl-gcc >/dev/null 2>&1; then
      echo "musl-gcc is required to build shared libraries for $target on glibc Linux hosts; set MONTY_GO_FFI_MUSL_NATIVE=1 on musl-native builders" >&2
      exit 1
    fi

    linker_var="CARGO_TARGET_${target^^}_LINKER"
    linker_var="${linker_var//-/_}"
    if [[ -z "${!linker_var:-}" ]]; then
      export "$linker_var=musl-gcc"
    fi
  fi
fi

mkdir -p "$lib_dir"

echo "Building monty-go-ffi for $target"
cargo build --manifest-path "$ROOT_DIR/Cargo.toml" -p monty-go-ffi --release --target "$target"

if [[ "$SKIP_HEADER" != "1" ]]; then
  echo "Refreshing C header"
  cbindgen --config "$CRATE_DIR/cbindgen.toml" --crate monty-go-ffi --output "$HEADER_PATH" "$CRATE_DIR"
fi

artifact="$ROOT_DIR/target/$target/release/$lib_name"
if [[ ! -f "$artifact" ]]; then
  echo "expected artifact not found: $artifact" >&2
  exit 1
fi

cp "$artifact" "$lib_dir/$lib_name"
rm -f "$lib_dir/libmonty_go_ffi.a" "$lib_dir/monty_go_ffi.lib"
echo "Wrote $lib_dir/$lib_name"
