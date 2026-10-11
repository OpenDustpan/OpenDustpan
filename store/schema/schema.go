// Package schema reports the schema versions of the database and the
// binary. It holds the newer-schema refusal shared by every store adapter.
//
// It lives here, not in store/internal/migrate, because both store adapters
// and cmd/opendustpan must import it: a package under store/internal can only
// be imported from inside store, so the command could not reach it. This
// package imports only the standard library, so no import cycle is possible.
// The version comparison itself stays in store/internal/migrate with the
// migrations it checks.
package schema

import (
	"errors"
	"fmt"
)

// ErrSchemaNewer reports a database that records a migration version the
// binary does not embed.
var ErrSchemaNewer = errors.New("database schema is newer than the binary knows")

// NewerError reports the schema version of the database and the schema
// version of the binary when the database is newer. The adapters return it.
// The command adds the version of the binary to the text, because the
// adapters do not know it.
type NewerError struct {
	// DatabaseVersion is the newest migration version the database records.
	DatabaseVersion int64
	// BinaryVersion is the newest migration version the binary embeds.
	BinaryVersion int64
}

// NewSchemaNewerError builds the refusal for recorded versions the binary
// does not embed.
func NewSchemaNewerError(recorded, embedded []int64) *NewerError {
	return &NewerError{
		DatabaseVersion: DatabaseVersion(recorded),
		BinaryVersion:   BinaryVersion(embedded),
	}
}

// Error describes the refusal and the one action: start a newer binary or
// restore a backup.
func (e *NewerError) Error() string {
	return fmt.Sprintf(
		"database schema version %d is newer than binary schema version %d: "+
			"start a newer binary or restore a backup",
		e.DatabaseVersion,
		e.BinaryVersion,
	)
}

// Unwrap returns ErrSchemaNewer, so errors.Is finds the sentinel.
func (e *NewerError) Unwrap() error {
	return ErrSchemaNewer
}

// DatabaseVersion returns the newest version the database records, or zero
// when it records none.
func DatabaseVersion(recorded []int64) int64 {
	var newest int64

	for _, v := range recorded {
		if v > newest {
			newest = v
		}
	}

	return newest
}

// BinaryVersion returns the newest migration the binary embeds, or zero when
// it embeds none.
func BinaryVersion(embedded []int64) int64 {
	var newest int64

	for _, v := range embedded {
		if v > newest {
			newest = v
		}
	}

	return newest
}

// Inspection is the read-only schema state of one database. Inspect in each
// store adapter returns it, and `opendustpan upgrade check` reads it.
type Inspection struct {
	// DatabaseVersion is the newest migration version the database records.
	// It is zero when the database records none.
	DatabaseVersion int64
	// BinaryVersion is the newest migration version the binary embeds.
	BinaryVersion int64
	// Pending holds the names of the embedded migrations the database does
	// not record, ordered by version.
	Pending []string
	// Unknown holds the recorded versions the binary does not embed,
	// ordered. It is empty when the database is equal or older.
	Unknown []int64
}
