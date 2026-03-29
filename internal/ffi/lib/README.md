# Bundled Shared Libraries

This directory stores the prebuilt `monty_go_ffi` shared libraries embedded by
the Go bindings.

Only the runtime shared libraries are tracked here. Legacy static or import
libraries are intentionally omitted because the Go bindings embed and load the
platform shared library directly.

Expected layout:

- `darwin_arm64/libmonty_go_ffi.dylib`
- `linux_amd64/libmonty_go_ffi.so`
- `linux_arm64/libmonty_go_ffi.so`
- `linux_amd64_musl/libmonty_go_ffi.so`
- `linux_arm64_musl/libmonty_go_ffi.so`
- `windows_amd64/monty_go_ffi.dll`

Refresh a target artifact with:

```bash
scripts/build-go-ffi.sh <target-triple>
```

The generated C header lives in `internal/ffi/include/monty_go_ffi.h`.
