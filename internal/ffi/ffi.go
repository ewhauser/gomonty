//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64)

package ffi

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

const unavailableMessagePrefix = "monty native bindings are unavailable"

type cRunner struct{}
type cRepl struct{}
type cProgress struct{}
type cError struct{}

type cBytes struct {
	ptr *byte
	len uintptr
}

type cRunnerResult struct {
	runner *cRunner
	err    *cError
}

type cReplResult struct {
	repl *cRepl
	err  *cError
}

type cOpResult struct {
	progress        *cProgress
	progressPayload cBytes
	err             *cError
	repl            *cRepl
	prints          cBytes
}

type nativeAPI struct {
	once    sync.Once
	loadErr error
	handle  uintptr

	bytesFree             func(ptr *byte, len uintptr)
	runnerFree            func(runner *cRunner)
	replFree              func(repl *cRepl)
	progressFree          func(progress *cProgress)
	errorFree             func(err *cError)
	errorJSON             func(err *cError, out *cBytes)
	errorDisplay          func(err *cError, format *byte, color bool, out *cBytes)
	runnerNew             func(codePtr *byte, codeLen uintptr, optionsPtr *byte, optionsLen uintptr, out *cRunnerResult)
	runnerLoad            func(dataPtr *byte, dataLen uintptr, out *cRunnerResult)
	runnerDump            func(runner *cRunner, out *cBytes, errOut **cError)
	runnerTypeCheck       func(runner *cRunner, prefixPtr *byte, prefixLen uintptr) *cError
	runnerStart           func(runner *cRunner, optionsPtr *byte, optionsLen uintptr, out *cOpResult)
	replNew               func(optionsPtr *byte, optionsLen uintptr, out *cReplResult)
	replLoad              func(dataPtr *byte, dataLen uintptr, out *cReplResult)
	replDump              func(repl *cRepl, out *cBytes, errOut **cError)
	replFeedStart         func(repl *cRepl, codePtr *byte, codeLen uintptr, optionsPtr *byte, optionsLen uintptr, out *cOpResult)
	progressDescribe      func(progress *cProgress, out *cBytes, errOut **cError)
	progressDump          func(progress *cProgress, out *cBytes, errOut **cError)
	progressLoad          func(dataPtr *byte, dataLen uintptr, out *cOpResult)
	progressTakeRepl      func(progress *cProgress, out *cReplResult)
	progressResumeCall    func(progress *cProgress, resultPtr *byte, resultLen uintptr, out *cOpResult)
	progressResumeLookup  func(progress *cProgress, resultPtr *byte, resultLen uintptr, out *cOpResult)
	progressResumeFutures func(progress *cProgress, resultPtr *byte, resultLen uintptr, out *cOpResult)
}

var api nativeAPI

// Runner is an owned handle for a compiled Monty runner.
type Runner struct {
	ptr *cRunner
}

// Repl is an owned handle for a Monty REPL session.
type Repl struct {
	ptr *cRepl
}

// Progress is an owned handle for an in-flight Monty snapshot.
type Progress struct {
	ptr *cProgress
}

// Error is an owned handle for a Monty error object.
type Error struct {
	ptr     *cError
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

func ensureLoaded() error {
	api.once.Do(func() {
		libraryPath, err := extractEmbeddedLibrary(embeddedLibs, embeddedLibraryDir, embeddedLibraryFilename, libraryCacheRoot())
		if err != nil {
			api.loadErr = err
			return
		}
		handle, err := loadLibrary(libraryPath)
		if err != nil {
			api.loadErr = fmt.Errorf("load %s from %s: %w", embeddedLibraryFilename, libraryPath, err)
			return
		}
		api.handle = handle
		api.loadErr = api.register(handle)
	})
	return api.loadErr
}

func (a *nativeAPI) register(handle uintptr) error {
	for _, binding := range []struct {
		name string
		dst  any
	}{
		{"monty_go_bytes_free", &a.bytesFree},
		{"monty_go_runner_free", &a.runnerFree},
		{"monty_go_repl_free", &a.replFree},
		{"monty_go_progress_free", &a.progressFree},
		{"monty_go_error_free", &a.errorFree},
		{"monty_go_error_json", &a.errorJSON},
		{"monty_go_error_display", &a.errorDisplay},
		{"monty_go_runner_new", &a.runnerNew},
		{"monty_go_runner_load", &a.runnerLoad},
		{"monty_go_runner_dump", &a.runnerDump},
		{"monty_go_runner_type_check", &a.runnerTypeCheck},
		{"monty_go_runner_start", &a.runnerStart},
		{"monty_go_repl_new", &a.replNew},
		{"monty_go_repl_load", &a.replLoad},
		{"monty_go_repl_dump", &a.replDump},
		{"monty_go_repl_feed_start", &a.replFeedStart},
		{"monty_go_progress_describe", &a.progressDescribe},
		{"monty_go_progress_dump", &a.progressDump},
		{"monty_go_progress_load", &a.progressLoad},
		{"monty_go_progress_take_repl", &a.progressTakeRepl},
		{"monty_go_progress_resume_call", &a.progressResumeCall},
		{"monty_go_progress_resume_lookup", &a.progressResumeLookup},
		{"monty_go_progress_resume_futures", &a.progressResumeFutures},
	} {
		if err := registerLibraryFunc(handle, binding.name, binding.dst); err != nil {
			return err
		}
	}
	return nil
}

func registerLibraryFunc(handle uintptr, name string, dst any) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("register %s: %v", name, recovered)
		}
	}()
	purego.RegisterLibFunc(dst, handle, name)
	return nil
}

func extractEmbeddedLibrary(libs fs.FS, dir string, filename string, cacheRoot string) (string, error) {
	fullPath := path.Join(dir, filename)
	libraryBytes, err := fs.ReadFile(libs, fullPath)
	if err != nil {
		return "", fmt.Errorf("read embedded library %s: %w", fullPath, err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(libraryBytes))
	cacheDir := filepath.Join(cacheRoot, "gomonty", digest[:12])
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir %s: %w", cacheDir, err)
	}
	targetPath := filepath.Join(cacheDir, filename)
	if existing, err := os.ReadFile(targetPath); err == nil {
		if fmt.Sprintf("%x", sha256.Sum256(existing)) == digest {
			return targetPath, nil
		}
	}

	tempFile, err := os.CreateTemp(cacheDir, filename+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temp cache file: %w", err)
	}
	tempPath := tempFile.Name()
	if _, err := tempFile.Write(libraryBytes); err != nil {
		tempFile.Close()
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("write cached library: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("close cached library: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tempPath, 0o755); err != nil {
			_ = os.Remove(tempPath)
			return "", fmt.Errorf("chmod cached library: %w", err)
		}
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		_ = os.Remove(tempPath)
		if errors.Is(err, os.ErrExist) {
			return targetPath, nil
		}
		if existing, readErr := os.ReadFile(targetPath); readErr == nil {
			if fmt.Sprintf("%x", sha256.Sum256(existing)) == digest {
				return targetPath, nil
			}
		}
		return "", fmt.Errorf("move cached library into place: %w", err)
	}
	return targetPath, nil
}

func libraryCacheRoot() string {
	if root := os.Getenv("GOMONTY_FFI_CACHE_DIR"); root != "" {
		return root
	}
	if root, err := os.UserCacheDir(); err == nil && root != "" {
		return root
	}
	return os.TempDir()
}

func unavailableMessage(err error) string {
	if err == nil {
		return unavailableMessagePrefix
	}
	return unavailableMessagePrefix + ": " + err.Error()
}

func unavailableError(err error) *Error {
	return &Error{message: unavailableMessage(err)}
}

func syntheticErrorJSON(message string) []byte {
	payload := map[string]any{
		"version":   1,
		"kind":      "api",
		"type_name": "RuntimeError",
		"message":   message,
		"traceback": []any{},
	}
	bytes, _ := json.Marshal(payload)
	return bytes
}

func newRunner(ptr *cRunner) *Runner {
	if ptr == nil {
		return nil
	}
	runner := &Runner{ptr: ptr}
	runtime.SetFinalizer(runner, (*Runner).Close)
	return runner
}

func newRepl(ptr *cRepl) *Repl {
	if ptr == nil {
		return nil
	}
	repl := &Repl{ptr: ptr}
	runtime.SetFinalizer(repl, (*Repl).Close)
	return repl
}

func newProgress(ptr *cProgress) *Progress {
	if ptr == nil {
		return nil
	}
	progress := &Progress{ptr: ptr}
	runtime.SetFinalizer(progress, (*Progress).Close)
	return progress
}

func newError(ptr *cError) *Error {
	if ptr == nil {
		return nil
	}
	err := &Error{ptr: ptr}
	runtime.SetFinalizer(err, (*Error).Close)
	return err
}

func runnerResultFromC(result cRunnerResult) RunnerResult {
	return RunnerResult{
		Runner: newRunner(result.runner),
		Error:  newError(result.err),
	}
}

func replResultFromC(result cReplResult) ReplResult {
	return ReplResult{
		Repl:  newRepl(result.repl),
		Error: newError(result.err),
	}
}

func opResultFromC(result cOpResult) OpResult {
	return OpResult{
		Progress:        newProgress(result.progress),
		ProgressPayload: takeBytes(result.progressPayload),
		Repl:            newRepl(result.repl),
		Error:           newError(result.err),
		Prints:          string(takeBytes(result.prints)),
	}
}

func byteArgs(data []byte) (*byte, uintptr) {
	if len(data) == 0 {
		return nil, 0
	}
	return unsafe.SliceData(data), uintptr(len(data))
}

func takeBytes(bytes cBytes) []byte {
	if bytes.ptr == nil || bytes.len == 0 {
		return nil
	}
	defer api.bytesFree(bytes.ptr, bytes.len)
	view := unsafe.Slice(bytes.ptr, int(bytes.len))
	return append([]byte(nil), view...)
}

func cStringBytes(value string) []byte {
	bytes := make([]byte, len(value)+1)
	copy(bytes, value)
	return bytes
}

// Close frees the owned runner handle.
func (r *Runner) Close() {
	if r == nil || r.ptr == nil {
		return
	}
	api.runnerFree(r.ptr)
	r.ptr = nil
}

// Close frees the owned REPL handle.
func (r *Repl) Close() {
	if r == nil || r.ptr == nil {
		return
	}
	api.replFree(r.ptr)
	r.ptr = nil
}

// Close frees the owned progress handle.
func (p *Progress) Close() {
	if p == nil || p.ptr == nil {
		return
	}
	api.progressFree(p.ptr)
	p.ptr = nil
}

// Close frees the owned error handle.
func (e *Error) Close() {
	if e == nil || e.ptr == nil {
		return
	}
	api.errorFree(e.ptr)
	e.ptr = nil
}

// JSON returns the structured JSON summary for the error handle.
func (e *Error) JSON() []byte {
	if e == nil {
		return nil
	}
	if e.ptr == nil {
		return syntheticErrorJSON(e.message)
	}
	var out cBytes
	api.errorJSON(e.ptr, &out)
	return takeBytes(out)
}

// Display returns a formatted string for the error handle.
func (e *Error) Display(format string, color bool) string {
	if e == nil {
		return ""
	}
	if e.ptr == nil {
		return e.message
	}
	formatBytes := cStringBytes(format)
	var out cBytes
	api.errorDisplay(e.ptr, unsafe.SliceData(formatBytes), color, &out)
	runtime.KeepAlive(formatBytes)
	return string(takeBytes(out))
}

// NewRunner constructs a compiled runner from source code plus JSON options.
func NewRunner(code []byte, options []byte) RunnerResult {
	if err := ensureLoaded(); err != nil {
		return RunnerResult{Error: unavailableError(err)}
	}
	codePtr, codeLen := byteArgs(code)
	optionsPtr, optionsLen := byteArgs(options)
	var result cRunnerResult
	api.runnerNew(codePtr, codeLen, optionsPtr, optionsLen, &result)
	runtime.KeepAlive(code)
	runtime.KeepAlive(options)
	return runnerResultFromC(result)
}

// LoadRunner restores a runner from a serialized byte slice.
func LoadRunner(data []byte) RunnerResult {
	if err := ensureLoaded(); err != nil {
		return RunnerResult{Error: unavailableError(err)}
	}
	dataPtr, dataLen := byteArgs(data)
	var result cRunnerResult
	api.runnerLoad(dataPtr, dataLen, &result)
	runtime.KeepAlive(data)
	return runnerResultFromC(result)
}

// Dump serializes the runner handle.
func (r *Runner) Dump() ([]byte, *Error) {
	if r == nil || r.ptr == nil {
		return nil, nil
	}
	var out cBytes
	var errOut *cError
	api.runnerDump(r.ptr, &out, &errOut)
	return takeBytes(out), newError(errOut)
}

// TypeCheck runs static type checking and returns an owned error handle on failure.
func (r *Runner) TypeCheck(prefix []byte) *Error {
	if r == nil || r.ptr == nil {
		return nil
	}
	prefixPtr, prefixLen := byteArgs(prefix)
	err := api.runnerTypeCheck(r.ptr, prefixPtr, prefixLen)
	runtime.KeepAlive(prefix)
	return newError(err)
}

// Start begins runner execution with JSON-encoded start options.
func (r *Runner) Start(options []byte) OpResult {
	if r == nil || r.ptr == nil {
		return OpResult{}
	}
	optionsPtr, optionsLen := byteArgs(options)
	var result cOpResult
	api.runnerStart(r.ptr, optionsPtr, optionsLen, &result)
	runtime.KeepAlive(options)
	return opResultFromC(result)
}

// NewRepl constructs a new REPL from JSON options.
func NewRepl(options []byte) ReplResult {
	if err := ensureLoaded(); err != nil {
		return ReplResult{Error: unavailableError(err)}
	}
	optionsPtr, optionsLen := byteArgs(options)
	var result cReplResult
	api.replNew(optionsPtr, optionsLen, &result)
	runtime.KeepAlive(options)
	return replResultFromC(result)
}

// LoadRepl restores a serialized REPL.
func LoadRepl(data []byte) ReplResult {
	if err := ensureLoaded(); err != nil {
		return ReplResult{Error: unavailableError(err)}
	}
	dataPtr, dataLen := byteArgs(data)
	var result cReplResult
	api.replLoad(dataPtr, dataLen, &result)
	runtime.KeepAlive(data)
	return replResultFromC(result)
}

// Dump serializes the REPL handle.
func (r *Repl) Dump() ([]byte, *Error) {
	if r == nil || r.ptr == nil {
		return nil, nil
	}
	var out cBytes
	var errOut *cError
	api.replDump(r.ptr, &out, &errOut)
	return takeBytes(out), newError(errOut)
}

// FeedStart begins execution of a REPL snippet.
func (r *Repl) FeedStart(code []byte, options []byte) OpResult {
	if r == nil || r.ptr == nil {
		return OpResult{}
	}
	codePtr, codeLen := byteArgs(code)
	optionsPtr, optionsLen := byteArgs(options)
	var result cOpResult
	api.replFeedStart(r.ptr, codePtr, codeLen, optionsPtr, optionsLen, &result)
	runtime.KeepAlive(code)
	runtime.KeepAlive(options)
	return opResultFromC(result)
}

// Describe returns the JSON description for the progress handle.
func (p *Progress) Describe() ([]byte, *Error) {
	if p == nil || p.ptr == nil {
		return nil, nil
	}
	var out cBytes
	var errOut *cError
	api.progressDescribe(p.ptr, &out, &errOut)
	return takeBytes(out), newError(errOut)
}

// Dump serializes the progress handle.
func (p *Progress) Dump() ([]byte, *Error) {
	if p == nil || p.ptr == nil {
		return nil, nil
	}
	var out cBytes
	var errOut *cError
	api.progressDump(p.ptr, &out, &errOut)
	return takeBytes(out), newError(errOut)
}

// LoadProgress restores a serialized progress handle.
func LoadProgress(data []byte) OpResult {
	if err := ensureLoaded(); err != nil {
		return OpResult{Error: unavailableError(err)}
	}
	dataPtr, dataLen := byteArgs(data)
	var result cOpResult
	api.progressLoad(dataPtr, dataLen, &result)
	runtime.KeepAlive(data)
	return opResultFromC(result)
}

// TakeRepl extracts a REPL handle from a progress object.
func (p *Progress) TakeRepl() ReplResult {
	if p == nil || p.ptr == nil {
		return ReplResult{}
	}
	var result cReplResult
	api.progressTakeRepl(p.ptr, &result)
	return replResultFromC(result)
}

// ResumeCall resumes a function or OS-call progress handle.
func (p *Progress) ResumeCall(result []byte) OpResult {
	if p == nil || p.ptr == nil {
		return OpResult{}
	}
	resultPtr, resultLen := byteArgs(result)
	var out cOpResult
	api.progressResumeCall(p.ptr, resultPtr, resultLen, &out)
	runtime.KeepAlive(result)
	return opResultFromC(out)
}

// ResumeLookup resumes a name-lookup progress handle.
func (p *Progress) ResumeLookup(result []byte) OpResult {
	if p == nil || p.ptr == nil {
		return OpResult{}
	}
	resultPtr, resultLen := byteArgs(result)
	var out cOpResult
	api.progressResumeLookup(p.ptr, resultPtr, resultLen, &out)
	runtime.KeepAlive(result)
	return opResultFromC(out)
}

// ResumeFutures resumes a future-resolution progress handle.
func (p *Progress) ResumeFutures(result []byte) OpResult {
	if p == nil || p.ptr == nil {
		return OpResult{}
	}
	resultPtr, resultLen := byteArgs(result)
	var out cOpResult
	api.progressResumeFutures(p.ptr, resultPtr, resultLen, &out)
	runtime.KeepAlive(result)
	return opResultFromC(out)
}
