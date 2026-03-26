//go:build !((darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64))

package ffi

import "encoding/json"

const unavailableMessage = "monty native bindings are unavailable: this target is not supported; darwin/amd64 remains unsupported"

// Runner is the unsupported-target stub for a compiled runner handle.
type Runner struct{}

// Repl is the unsupported-target stub for a REPL handle.
type Repl struct{}

// Progress is the unsupported-target stub for a progress handle.
type Progress struct{}

// Error is the unsupported-target stub for an FFI error handle.
type Error struct {
	message string
}

// RunnerResult wraps runner construction or load results.
type RunnerResult struct {
	Runner *Runner
	Error  *Error
}

// ReplResult wraps REPL construction or load results.
type ReplResult struct {
	Repl  *Repl
	Error *Error
}

// OpResult wraps start/resume/feed operations.
type OpResult struct {
	Progress        *Progress
	ProgressPayload []byte
	Repl            *Repl
	Error           *Error
	Prints          string
}

func unavailableError() *Error {
	return &Error{message: unavailableMessage}
}

// Close releases the stub runner handle.
func (*Runner) Close() {}

// Close releases the stub REPL handle.
func (*Repl) Close() {}

// Close releases the stub progress handle.
func (*Progress) Close() {}

// Close releases the stub error handle.
func (*Error) Close() {}

// JSON returns a synthetic API error payload.
func (e *Error) JSON() []byte {
	payload := map[string]any{
		"version":   1,
		"kind":      "api",
		"type_name": "RuntimeError",
		"message":   e.message,
		"traceback": []any{},
	}
	bytes, _ := json.Marshal(payload)
	return bytes
}

// Display returns the stub error message.
func (e *Error) Display(string, bool) string {
	return e.message
}

// NewRunner returns an unavailable error on unsupported targets.
func NewRunner([]byte, []byte) RunnerResult {
	return RunnerResult{Error: unavailableError()}
}

// LoadRunner returns an unavailable error on unsupported targets.
func LoadRunner([]byte) RunnerResult {
	return RunnerResult{Error: unavailableError()}
}

// Dump returns an unavailable error on unsupported targets.
func (*Runner) Dump() ([]byte, *Error) {
	return nil, unavailableError()
}

// TypeCheck returns an unavailable error on unsupported targets.
func (*Runner) TypeCheck([]byte) *Error {
	return unavailableError()
}

// Start returns an unavailable error on unsupported targets.
func (*Runner) Start([]byte) OpResult {
	return OpResult{Error: unavailableError()}
}

// NewRepl returns an unavailable error on unsupported targets.
func NewRepl([]byte) ReplResult {
	return ReplResult{Error: unavailableError()}
}

// LoadRepl returns an unavailable error on unsupported targets.
func LoadRepl([]byte) ReplResult {
	return ReplResult{Error: unavailableError()}
}

// Dump returns an unavailable error on unsupported targets.
func (*Repl) Dump() ([]byte, *Error) {
	return nil, unavailableError()
}

// FeedStart returns an unavailable error on unsupported targets.
func (*Repl) FeedStart([]byte, []byte) OpResult {
	return OpResult{Error: unavailableError()}
}

// Describe returns an unavailable error on unsupported targets.
func (*Progress) Describe() ([]byte, *Error) {
	return nil, unavailableError()
}

// Dump returns an unavailable error on unsupported targets.
func (*Progress) Dump() ([]byte, *Error) {
	return nil, unavailableError()
}

// LoadProgress returns an unavailable error on unsupported targets.
func LoadProgress([]byte) OpResult {
	return OpResult{Error: unavailableError()}
}

// TakeRepl returns an unavailable error on unsupported targets.
func (*Progress) TakeRepl() ReplResult {
	return ReplResult{Error: unavailableError()}
}

// ResumeCall returns an unavailable error on unsupported targets.
func (*Progress) ResumeCall([]byte) OpResult {
	return OpResult{Error: unavailableError()}
}

// ResumeLookup returns an unavailable error on unsupported targets.
func (*Progress) ResumeLookup([]byte) OpResult {
	return OpResult{Error: unavailableError()}
}

// ResumeFutures returns an unavailable error on unsupported targets.
func (*Progress) ResumeFutures([]byte) OpResult {
	return OpResult{Error: unavailableError()}
}
