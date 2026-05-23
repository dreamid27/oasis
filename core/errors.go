package core

import "errors"

// ErrNotFound is returned by store Get methods when a record with the
// requested ID does not exist.
var ErrNotFound = errors.New("not found")

// IsNotFound reports whether err is or wraps ErrNotFound.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
