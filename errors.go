package monty

import (
	"encoding/json"
	"fmt"

	"github.com/ewhauser/gomonty/internal/ffi"
)

// Frame is a structured traceback frame returned by runtime and syntax errors.
type Frame struct {
	Filename     string  `json:"filename"`
	Line         uint32  `json:"line"`
	Column       uint32  `json:"column"`
	EndLine      uint32  `json:"end_line"`
	EndColumn    uint32  `json:"end_column"`
	FunctionName *string `json:"function_name,omitempty"`
	SourceLine   *string `json:"source_line,omitempty"`
}

type errorSummary struct {
	Version   uint32  `json:"version"`
	Kind      string  `json:"kind"`
	TypeName  string  `json:"type_name"`
	Message   string  `json:"message"`
	Traceback []Frame `json:"traceback"`
}

type baseError struct {
	summary errorSummary
	handle  *ffi.Error
}

func (e *baseError) Error() string {
	if e == nil {
		return ""
	}
	if e.handle != nil {
		if formatted := e.handle.Display("type-msg", false); formatted != "" {
			return formatted
		}
	}
	if e.summary.Message == "" {
		return e.summary.TypeName
	}
	return e.summary.TypeName + ": " + e.summary.Message
}

func (e *baseError) TracebackString() string {
	if e == nil || e.handle == nil {
		return ""
	}
	return e.handle.Display("traceback", false)
}

// SyntaxError is raised for parse or compile failures.
type SyntaxError struct {
	baseError
}

// RuntimeError is raised for runtime failures and exposes traceback frames.
type RuntimeError struct {
	baseError
	Frames []Frame
}

// TypingError is raised for static type-check failures and can re-render the
// diagnostics using the formatter strings supported by Rust.
type TypingError struct {
	baseError
}

// Display renders the typing diagnostics using one of Rust's formatter names:
// `full`, `concise`, `azure`, `json`, `jsonlines`, `rdjson`, `pylint`,
// `gitlab`, or `github`.
func (e *TypingError) Display(format string, color bool) string {
	if e == nil || e.handle == nil {
		return e.Error()
	}
	return e.handle.Display(format, color)
}

func newError(err *ffi.Error) error {
	if err == nil {
		return nil
	}
	var summary errorSummary
	if bytes := err.JSON(); len(bytes) > 0 {
		if decodeErr := json.Unmarshal(bytes, &summary); decodeErr != nil {
			return fmt.Errorf("invalid monty error payload: %w", decodeErr)
		}
	}

	base := baseError{
		summary: summary,
		handle:  err,
	}

	switch summary.Kind {
	case "syntax":
		return &SyntaxError{baseError: base}
	case "typing":
		return &TypingError{baseError: base}
	case "runtime", "api":
		return &RuntimeError{
			baseError: base,
			Frames:    summary.Traceback,
		}
	default:
		return &RuntimeError{
			baseError: base,
			Frames:    summary.Traceback,
		}
	}
}
