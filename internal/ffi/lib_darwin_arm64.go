//go:build darwin && arm64

package ffi

import "embed"

//go:embed lib/darwin_arm64/libmonty_go_ffi.dylib
var embeddedLibs embed.FS

const (
	embeddedLibraryDir      = "lib/darwin_arm64"
	embeddedLibraryFilename = "libmonty_go_ffi.dylib"
)
