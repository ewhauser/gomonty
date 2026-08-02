//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package runtimebundle

import (
	"context"
	"fmt"
)

func acquireFileLock(context.Context, string) (func(), error) {
	return nil, fmt.Errorf("%w: preparation locking is unavailable on this operating system", ErrUnsupported)
}
