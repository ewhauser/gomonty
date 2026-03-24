# Bundled Native Archives

This directory stores the prebuilt `monty_go_ffi` static libraries used by the
Go bindings.

Expected layout:

- `darwin_arm64/libmonty_go_ffi.a`
- `linux_amd64/libmonty_go_ffi.a`
- `linux_arm64/libmonty_go_ffi.a`
- `linux_amd64_musl/libmonty_go_ffi.a`
- `linux_arm64_musl/libmonty_go_ffi.a`
- `windows_amd64/monty_go_ffi.lib`

Refresh a target artifact with:

```bash
scripts/build-go-ffi.sh <target-triple>
```

The generated C header lives in `internal/ffi/include/monty_go_ffi.h`.
