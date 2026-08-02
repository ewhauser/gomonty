# Native Build Staging

No native executable is tracked in this directory. Git and Go module ZIPs contain
only reviewable source, the generated C header, and the versioned hash manifest.

Repository build and release workflows may temporarily write target output here:

Expected temporary layout:

- `darwin_arm64/libmonty_go_ffi.dylib`
- `linux_amd64/libmonty_go_ffi.so`
- `linux_arm64/libmonty_go_ffi.so`
- `linux_amd64_musl/libmonty_go_ffi.so`
- `linux_arm64_musl/libmonty_go_ffi.so`
- `windows_amd64/monty_go_ffi.dll`

Build a target pair for development with:

```bash
scripts/build-go-ffi.sh <target-triple>
```

The output is ignored and must not be committed. Consumers use `gomonty prepare
download` or `gomonty prepare build`; verified runtime files live under the user
cache, not this source directory. The generated C header remains at
`internal/ffi/include/monty_go_ffi.h`.
