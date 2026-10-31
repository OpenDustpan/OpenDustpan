// Package storeerr is the one error type that the SQL adapters use for a
// database that does not answer. The type wraps namespace.ErrUnavailable, so
// the HTTP adapters answer 503 with Retry-After, and it keeps the driver
// error for errors.As and for the log. Its text has no driver text.
package storeerr

import (
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Class names why the database does not answer. The value is fixed text.
type Class string

// The classes of a database that does not answer.
const (
	// ConnectionFailed means that the adapter could not connect, or lost the
	// connection.
	ConnectionFailed Class = "connection failed"
	// Shutdown means that the database server stops or restarts.
	Shutdown Class = "server is shutting down"
	// ReadOnly means that the database refuses writes, for example after a
	// failover to a replica.
	ReadOnly Class = "database is read-only"
	// Capacity means that the database server refuses a new session for now,
	// because it has no free one.
	Capacity Class = "database has no free session"
	// Busy means that the database file stayed locked for the whole busy bound.
	Busy Class = "database is busy"
)

// Error is a database that does not answer.
type Error struct {
	class Class
	cause error
}

// New returns an Error of class with the driver error cause.
func New(class Class, cause error) *Error {
	return &Error{class: class, cause: cause}
}

// Error returns the fixed text of the sentinel and the class, never the
// driver text.
func (e *Error) Error() string {
	return namespace.ErrUnavailable.Error() + ": " + string(e.class)
}

// Class returns why the database does not answer.
func (e *Error) Class() Class {
	return e.class
}

// Unwrap returns namespace.ErrUnavailable and the driver error.
func (e *Error) Unwrap() []error {
	return []error{namespace.ErrUnavailable, e.cause}
}

// Sentinel returns sentinel, and also err when err is a database that does not
// answer. A store port that has its own failure sentinel uses it, so that its
// callers keep the sentinel and the APIs still answer 503 for a lost database.
func Sentinel(sentinel, err error) error {
	if Is(err) {
		return fmt.Errorf("%w: %w", sentinel, err)
	}

	return sentinel
}

// Is reports whether err already is an Error, so that an adapter does not
// translate twice.
func Is(err error) bool {
	var target *Error

	return errors.As(err, &target)
}
