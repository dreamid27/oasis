package a2a

import (
	"errors"
	"fmt"
)

// Sentinel errors for A2A protocol failures. errors.Is-able; client methods
// wrap them with remote agent name + task ID for observability.
var (
	ErrTaskNotFound      = errors.New("a2a: task not found")
	ErrTaskNotCancelable = errors.New("a2a: task not cancelable")
	ErrPushNotSupported  = errors.New("a2a: push notifications not supported")
	ErrUnsupportedOp     = errors.New("a2a: unsupported operation")
	ErrContentType       = errors.New("a2a: content type not supported")
	ErrInvalidAgentResp  = errors.New("a2a: invalid agent response")
)

// A2A-specific JSON-RPC error codes (spec §5.4, verified against the
// official spec in Task 1).
const (
	codeTaskNotFound      = -32001
	codeTaskNotCancelable = -32002
	codePushNotSupported  = -32003
	codeUnsupportedOp     = -32004
	codeContentType       = -32005
	codeInvalidAgentResp  = -32006
)

// codeFor maps a sentinel to its wire code; 0 for non-protocol errors.
func codeFor(err error) int {
	switch {
	case errors.Is(err, ErrTaskNotFound):
		return codeTaskNotFound
	case errors.Is(err, ErrTaskNotCancelable):
		return codeTaskNotCancelable
	case errors.Is(err, ErrPushNotSupported):
		return codePushNotSupported
	case errors.Is(err, ErrUnsupportedOp):
		return codeUnsupportedOp
	case errors.Is(err, ErrContentType):
		return codeContentType
	case errors.Is(err, ErrInvalidAgentResp):
		return codeInvalidAgentResp
	}
	return 0
}

// taskError wraps a sentinel with the task ID so logs alone reconstruct
// what failed (errors must be observable).
func taskError(sentinel error, taskID string) error {
	return fmt.Errorf("%w (task %q)", sentinel, taskID)
}
