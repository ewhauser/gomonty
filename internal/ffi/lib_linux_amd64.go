//go:build linux && amd64 && !musl

package ffi

import "embed"

//go:embed lib/linux_amd64/libmonty_go_ffi.so
var embeddedLibs embed.FS

const (
	embeddedLibraryDir      = "lib/linux_amd64"
	embeddedLibraryFilename = "libmonty_go_ffi.so"
)
