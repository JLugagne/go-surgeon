package domain

import (
	"errors"
	"fmt"
)

// Error represents a domain-level error with a machine-readable code,
// a human-readable message, and an optional underlying error.
type Error struct {
	Code    string
	Message string
	Err     error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error {
	return e.Err
}

// IsDomainError checks if an error is of type *Error.
func IsDomainError(err error) bool {
	var de *Error
	return errors.As(err, &de)
}

var (
	// ErrInvalidAction indicates that the requested action is not supported or malformed.
	ErrInvalidAction = &Error{Code: "INVALID_ACTION", Message: "invalid action"}

	// ErrActionLimitExceeded indicates that more than the allowed number of actions were provided.
	ErrActionLimitExceeded = &Error{Code: "ACTION_LIMIT_EXCEEDED", Message: "action limit exceeded"}

	// ErrFileNotFound indicates that a target file could not be found.
	ErrFileNotFound = &Error{Code: "FILE_NOT_FOUND", Message: "file not found"}

	// ErrFileAlreadyExists indicates that a file already exists when create-file is used.
	ErrFileAlreadyExists = &Error{Code: "FILE_ALREADY_EXISTS", Message: "file already exists"}

	// ErrNodeNotFound indicates that a specific AST node (function, struct, etc.) was not found.
	ErrNodeNotFound = &Error{Code: "NODE_NOT_FOUND", Message: "node not found"}
)
