package core

import (
	"errors"
	"fmt"
)

// Exit codes are part of the CLI contract; scripts rely on them.
const (
	ExitOK       = 0
	ExitRuntime  = 1
	ExitUsage    = 2
	ExitNotFound = 3
	ExitEnv      = 4
	ExitTimeout  = 5 // af wait deadline elapsed
)

// CodedError carries the process exit code alongside the error.
type CodedError struct {
	Code int
	Err  error
}

func (e *CodedError) Error() string { return e.Err.Error() }
func (e *CodedError) Unwrap() error { return e.Err }

// Errf builds a CodedError the way fmt.Errorf builds an error.
func Errf(code int, format string, args ...any) error {
	return &CodedError{Code: code, Err: fmt.Errorf(format, args...)}
}

// ExitCode maps any error to its process exit code (default 1).
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ExitRuntime
}
