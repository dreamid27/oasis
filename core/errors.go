package core

import "errors"

// ErrNotFound is returned by store Get methods when a record with the
// requested ID does not exist.
var ErrNotFound = errors.New("not found")

// IsNotFound reports whether err is or wraps ErrNotFound.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// infraError is the private wrapper used by InfraError / IsInfraError.
type infraError struct{ err error }

func (e *infraError) Error() string { return e.err.Error() }
func (e *infraError) Unwrap() error { return e.err }

// InfraError wraps err to signal an infrastructure failure. When a tool
// returns InfraError from its callback, Func and Erase propagate it as
// the Go error return from ExecuteRaw, allowing the dispatch layer to
// inspect it for retry decisions.
func InfraError(err error) error {
	if err == nil {
		return nil
	}
	return &infraError{err: err}
}

// IsInfraError reports whether err was wrapped with InfraError.
func IsInfraError(err error) bool {
	var ie *infraError
	return errors.As(err, &ie)
}
