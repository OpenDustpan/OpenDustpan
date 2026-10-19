package main

import (
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/installation"
)

// The names of the commands that run the server. A refusal of a database
// that another kind made names both.
const (
	commandServe = "serve"
	commandDev   = "dev"
)

// kindSettingKey is the key of the installation kind in the audit entry of
// each start.
const kindSettingKey = "installation-kind"

// refusalError is a refusal with a decided text: it keeps the text, and wraps
// the cause, so errors.Is still finds the cause.
type refusalError struct {
	text  string
	cause error
}

// Error implements error.
func (e *refusalError) Error() string { return e.text }

// Unwrap returns the cause of the refusal.
func (e *refusalError) Unwrap() error { return e.cause }

// commandOfKind answers the command that makes a database of kind.
func commandOfKind(kind installation.Kind) string {
	if kind == installation.KindDev {
		return commandDev
	}

	return commandServe
}

// kindRefusal turns the error of a database that another kind made into the
// refusal of command: it says which command made the database, which command
// refuses it, and the setting that helps. It answers every other error
// unchanged.
func kindRefusal(err error, command string) error {
	mismatch, ok := errors.AsType[*installation.MismatchError](err)
	if !ok {
		return err
	}

	help := "Set --database to a new database."
	if command == commandDev {
		help = "Set a new --data-dir."
	}

	return &refusalError{
		text: fmt.Sprintf(
			"opendustpan %s made this database: %s refuses it. %s",
			commandOfKind(mismatch.Stored),
			command,
			help,
		),
		cause: err,
	}
}

// kindChange answers the change of an audit entry of a start that records
// the installation kind.
func kindChange(kind installation.Kind) audit.Change {
	return audit.Change{Key: kindSettingKey, Old: nil, New: kind.String(), Layer: "default", TakesEffect: ""}
}
