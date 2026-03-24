//go:build cgo && windows && amd64

package ffi

/*
#cgo LDFLAGS: -L${SRCDIR}/lib/windows_amd64 -lmonty_go_ffi
*/
import "C"
