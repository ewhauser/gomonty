//go:build linux && arm64 && musl

package ffi

import "embed"

//go:embed lib/linux_arm64_musl/libmonty_go_ffi.so
var embeddedLibs embed.FS

const (
	embeddedLibraryDir      = "lib/linux_arm64_musl"
	embeddedLibraryFilename = "libmonty_go_ffi.so"
)
