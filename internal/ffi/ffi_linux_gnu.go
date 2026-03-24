//go:build cgo && linux && !musl

package ffi

/*
#cgo amd64 LDFLAGS: -L${SRCDIR}/lib/linux_amd64 -lmonty_go_ffi -lm
#cgo arm64 LDFLAGS: -L${SRCDIR}/lib/linux_arm64 -lmonty_go_ffi -lm
*/
import "C"
