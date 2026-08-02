// Package monty exposes cgo-free Go bindings for the Monty Python interpreter.
//
// The package provides compiled runner and REPL APIs, typed value conversion,
// host callback dispatch, and low-level pause/resume snapshots. Native code is
// prepared explicitly with Prepare or the gomonty command, verified before
// every load, and then loaded with purego. Normal execution never downloads or
// builds code automatically.
//
// For Go-owned filesystem and environment callbacks, use the companion vfs
// package.
package monty
