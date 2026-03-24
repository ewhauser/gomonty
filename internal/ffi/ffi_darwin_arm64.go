//go:build cgo && darwin && arm64

package ffi

/*
#cgo LDFLAGS: -L${SRCDIR}/lib/darwin_arm64 -lmonty_go_ffi
*/
import "C"
