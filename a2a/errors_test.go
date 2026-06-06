package a2a

import (
	"errors"
	"testing"
)

func TestErrorCodes(t *testing.T) {
	if codeFor(ErrTaskNotFound) != codeTaskNotFound {
		t.Errorf("ErrTaskNotFound code = %d", codeFor(ErrTaskNotFound))
	}
	wrapped := taskError(ErrTaskNotFound, "task-1")
	if !errors.Is(wrapped, ErrTaskNotFound) {
		t.Error("wrapped error must satisfy errors.Is(ErrTaskNotFound)")
	}
	if wrapped.Error() == "" || wrapped.Error() == ErrTaskNotFound.Error() {
		t.Error("wrapped error must carry the task ID for observability")
	}
}
