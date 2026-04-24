// Package usererror defines stable, user-facing tool errors.
package usererror

import "fmt"

// Error is an error that is safe to return to an MCP tool caller.
type Error struct {
	Code    string
	Message string
	Cause   error
}

// New creates a user-facing error with a stable code.
func New(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Wrap creates a user-facing error that preserves the internal cause for logs
// and errors.As/errors.Is checks.
func Wrap(code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
