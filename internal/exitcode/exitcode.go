// Package exitcode defines the exit codes of bulkhead (docs/adr/0018-checker-in-this-repository.md).
package exitcode

import (
	"errors"
	"fmt"
)

// The exit codes. Continuous integration may rely on them.
const (
	// OK: every selected check passed, or the command succeeded.
	OK = 0
	// ChecksFailed: at least one selected check failed.
	ChecksFailed = 1
	// Usage: the command line or a file given to it is wrong.
	Usage = 2
	// Error: the checks could not run, for example without a cluster or when a safety rule refused to start.
	Error = 3
)

// Err is an error with the exit code it should end the program with.
type Err struct {
	Code int
	Err  error
}

// Error returns the message of the wrapped error.
func (e *Err) Error() string { return e.Err.Error() }

// Unwrap returns the wrapped error, for errors.Is and errors.As.
func (e *Err) Unwrap() error { return e.Err }

// Usagef returns a usage error.
func Usagef(format string, args ...any) error {
	return &Err{Code: Usage, Err: fmt.Errorf(format, args...)}
}

// Errorf returns an error that stopped the checks from running.
func Errorf(format string, args ...any) error {
	return &Err{Code: Error, Err: fmt.Errorf(format, args...)}
}

// Of returns the exit code for an error: OK for nil, the code of an Err, and Error otherwise.
func Of(err error) int {
	if err == nil {
		return OK
	}
	var e *Err
	if errors.As(err, &e) {
		return e.Code
	}
	return Error
}
