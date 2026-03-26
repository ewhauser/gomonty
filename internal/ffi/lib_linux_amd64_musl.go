//go:build linux && amd64 && musl

package ffi

import "embed"

//go:embed lib/linux_amd64_musl/libmonty_go_ffi.so
var embeddedLibs embed.FS

const (
	embeddedLibraryDir      = "lib/linux_amd64_musl"
	embeddedLibraryFilename = "libmonty_go_ffi.so"
)
