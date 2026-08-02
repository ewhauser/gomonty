package monty

import (
	"errors"
	"testing"
)

func TestRuntimeErrorUnwrapsPreparationCause(t *testing.T) {
	t.Parallel()
	err := &RuntimeError{baseError: baseError{cause: ErrRuntimeNotPrepared}}
	if !errors.Is(err, ErrRuntimeNotPrepared) {
		t.Fatalf("errors.Is(%v, ErrRuntimeNotPrepared) = false", err)
	}
}
