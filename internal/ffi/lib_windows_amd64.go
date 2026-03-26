//go:build windows && amd64

package ffi

import "embed"

//go:embed lib/windows_amd64/monty_go_ffi.dll
var embeddedLibs embed.FS

const (
	embeddedLibraryDir      = "lib/windows_amd64"
	embeddedLibraryFilename = "monty_go_ffi.dll"
)
