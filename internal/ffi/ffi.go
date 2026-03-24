//go:build cgo && !(darwin && amd64)

package ffi

/*
#cgo CFLAGS: -I${SRCDIR}/include
#include <stdlib.h>
#include "monty_go_ffi.h"
*/
import "C"

import (
	"runtime"
	"unsafe"
)

// Runner is an owned handle for a compiled Monty runner.
type Runner struct {
	ptr *C.MontyGoRunner
}

// Repl is an owned handle for a Monty REPL session.
type Repl struct {
	ptr *C.MontyGoRepl
}

// Progress is an owned handle for an in-flight Monty snapshot.
type Progress struct {
	ptr *C.MontyGoProgress
}

// Error is an owned handle for a Monty error object.
type Error struct {
	ptr *C.MontyGoError
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
	Progress *Progress
	Repl     *Repl
	Error    *Error
	Prints   string
}

func newRunner(ptr *C.MontyGoRunner) *Runner {
	if ptr == nil {
		return nil
	}
	runner := &Runner{ptr: ptr}
	runtime.SetFinalizer(runner, (*Runner).Close)
	return runner
}

func newRepl(ptr *C.MontyGoRepl) *Repl {
	if ptr == nil {
		return nil
	}
	repl := &Repl{ptr: ptr}
	runtime.SetFinalizer(repl, (*Repl).Close)
	return repl
}

func newProgress(ptr *C.MontyGoProgress) *Progress {
	if ptr == nil {
		return nil
	}
	progress := &Progress{ptr: ptr}
	runtime.SetFinalizer(progress, (*Progress).Close)
	return progress
}

func newError(ptr *C.MontyGoError) *Error {
	if ptr == nil {
		return nil
	}
	err := &Error{ptr: ptr}
	runtime.SetFinalizer(err, (*Error).Close)
	return err
}

func runnerResultFromC(result C.MontyGoRunnerResult) RunnerResult {
	return RunnerResult{
		Runner: newRunner(result.runner),
		Error:  newError(result.error),
	}
}

func replResultFromC(result C.MontyGoReplResult) ReplResult {
	return ReplResult{
		Repl:  newRepl(result.repl),
		Error: newError(result.error),
	}
}

func opResultFromC(result C.MontyGoOpResult) OpResult {
	return OpResult{
		Progress: newProgress(result.progress),
		Repl:     newRepl(result.repl),
		Error:    newError(result.error),
		Prints:   string(takeBytes(result.prints)),
	}
}

func byteArgs(data []byte) (*C.uint8_t, C.size_t) {
	if len(data) == 0 {
		return nil, 0
	}
	return (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(data))), C.size_t(len(data))
}

func takeBytes(bytes C.MontyGoBytes) []byte {
	if bytes.ptr == nil || bytes.len == 0 {
		return nil
	}
	defer C.monty_go_bytes_free(bytes)
	view := unsafe.Slice((*byte)(unsafe.Pointer(bytes.ptr)), int(bytes.len))
	return append([]byte(nil), view...)
}

// Close frees the owned runner handle.
func (r *Runner) Close() {
	if r == nil || r.ptr == nil {
		return
	}
	C.monty_go_runner_free(r.ptr)
	r.ptr = nil
}

// Close frees the owned REPL handle.
func (r *Repl) Close() {
	if r == nil || r.ptr == nil {
		return
	}
	C.monty_go_repl_free(r.ptr)
	r.ptr = nil
}

// Close frees the owned progress handle.
func (p *Progress) Close() {
	if p == nil || p.ptr == nil {
		return
	}
	C.monty_go_progress_free(p.ptr)
	p.ptr = nil
}

// Close frees the owned error handle.
func (e *Error) Close() {
	if e == nil || e.ptr == nil {
		return
	}
	C.monty_go_error_free(e.ptr)
	e.ptr = nil
}

// JSON returns the structured JSON summary for the error handle.
func (e *Error) JSON() []byte {
	if e == nil || e.ptr == nil {
		return nil
	}
	return takeBytes(C.monty_go_error_json(e.ptr))
}

// Display returns a formatted string for the error handle.
func (e *Error) Display(format string, color bool) string {
	if e == nil || e.ptr == nil {
		return ""
	}
	cFormat := C.CString(format)
	defer C.free(unsafe.Pointer(cFormat))
	return string(takeBytes(C.monty_go_error_display(e.ptr, cFormat, C.bool(color))))
}

// NewRunner constructs a compiled runner from source code plus JSON options.
func NewRunner(code []byte, options []byte) RunnerResult {
	codePtr, codeLen := byteArgs(code)
	optionsPtr, optionsLen := byteArgs(options)
	return runnerResultFromC(C.monty_go_runner_new(codePtr, codeLen, optionsPtr, optionsLen))
}

// LoadRunner restores a runner from a serialized byte slice.
func LoadRunner(data []byte) RunnerResult {
	dataPtr, dataLen := byteArgs(data)
	return runnerResultFromC(C.monty_go_runner_load(dataPtr, dataLen))
}

// Dump serializes the runner handle.
func (r *Runner) Dump() ([]byte, *Error) {
	if r == nil || r.ptr == nil {
		return nil, nil
	}
	var errOut *C.MontyGoError
	bytes := C.monty_go_runner_dump(r.ptr, &errOut)
	return takeBytes(bytes), newError(errOut)
}

// TypeCheck runs static type checking and returns an owned error handle on failure.
func (r *Runner) TypeCheck(prefix []byte) *Error {
	if r == nil || r.ptr == nil {
		return nil
	}
	prefixPtr, prefixLen := byteArgs(prefix)
	return newError(C.monty_go_runner_type_check(r.ptr, prefixPtr, prefixLen))
}

// Start begins runner execution with JSON-encoded start options.
func (r *Runner) Start(options []byte) OpResult {
	if r == nil || r.ptr == nil {
		return OpResult{}
	}
	optionsPtr, optionsLen := byteArgs(options)
	return opResultFromC(C.monty_go_runner_start(r.ptr, optionsPtr, optionsLen))
}

// NewRepl constructs a new REPL from JSON options.
func NewRepl(options []byte) ReplResult {
	optionsPtr, optionsLen := byteArgs(options)
	return replResultFromC(C.monty_go_repl_new(optionsPtr, optionsLen))
}

// LoadRepl restores a serialized REPL.
func LoadRepl(data []byte) ReplResult {
	dataPtr, dataLen := byteArgs(data)
	return replResultFromC(C.monty_go_repl_load(dataPtr, dataLen))
}

// Dump serializes the REPL handle.
func (r *Repl) Dump() ([]byte, *Error) {
	if r == nil || r.ptr == nil {
		return nil, nil
	}
	var errOut *C.MontyGoError
	bytes := C.monty_go_repl_dump(r.ptr, &errOut)
	return takeBytes(bytes), newError(errOut)
}

// FeedStart begins execution of a REPL snippet.
func (r *Repl) FeedStart(code []byte, options []byte) OpResult {
	if r == nil || r.ptr == nil {
		return OpResult{}
	}
	codePtr, codeLen := byteArgs(code)
	optionsPtr, optionsLen := byteArgs(options)
	return opResultFromC(C.monty_go_repl_feed_start(r.ptr, codePtr, codeLen, optionsPtr, optionsLen))
}

// Describe returns the JSON description for the progress handle.
func (p *Progress) Describe() ([]byte, *Error) {
	if p == nil || p.ptr == nil {
		return nil, nil
	}
	var errOut *C.MontyGoError
	bytes := C.monty_go_progress_describe(p.ptr, &errOut)
	return takeBytes(bytes), newError(errOut)
}

// Dump serializes the progress handle.
func (p *Progress) Dump() ([]byte, *Error) {
	if p == nil || p.ptr == nil {
		return nil, nil
	}
	var errOut *C.MontyGoError
	bytes := C.monty_go_progress_dump(p.ptr, &errOut)
	return takeBytes(bytes), newError(errOut)
}

// LoadProgress restores a serialized progress handle.
func LoadProgress(data []byte) OpResult {
	dataPtr, dataLen := byteArgs(data)
	return opResultFromC(C.monty_go_progress_load(dataPtr, dataLen))
}

// TakeRepl extracts a REPL handle from a progress object.
func (p *Progress) TakeRepl() ReplResult {
	if p == nil || p.ptr == nil {
		return ReplResult{}
	}
	return replResultFromC(C.monty_go_progress_take_repl(p.ptr))
}

// ResumeCall resumes a function or OS-call progress handle.
func (p *Progress) ResumeCall(result []byte) OpResult {
	if p == nil || p.ptr == nil {
		return OpResult{}
	}
	resultPtr, resultLen := byteArgs(result)
	return opResultFromC(C.monty_go_progress_resume_call(p.ptr, resultPtr, resultLen))
}

// ResumeLookup resumes a name-lookup progress handle.
func (p *Progress) ResumeLookup(result []byte) OpResult {
	if p == nil || p.ptr == nil {
		return OpResult{}
	}
	resultPtr, resultLen := byteArgs(result)
	return opResultFromC(C.monty_go_progress_resume_lookup(p.ptr, resultPtr, resultLen))
}

// ResumeFutures resumes a future-resolution progress handle.
func (p *Progress) ResumeFutures(result []byte) OpResult {
	if p == nil || p.ptr == nil {
		return OpResult{}
	}
	resultPtr, resultLen := byteArgs(result)
	return opResultFromC(C.monty_go_progress_resume_futures(p.ptr, resultPtr, resultLen))
}
