// Package installation models the installation kind of OpenDustpan: the
// marker that says which command made the data of an installation.
//
// The kind is dev or production. The first command that starts on a new
// database claims it, and no API and no command changes it later. So a
// database that opendustpan dev made never opens under opendustpan serve, and
// the reverse. The marker stops a mistake, such as a copy of a test database
// or a wrong path in a unit file. It does not stop a person who can write to
// the database.
//
// The package holds no SQL and no HTTP. Each store adapter implements the
// KindStore port, and the conformance suite in pkg/store/storetest checks it
// before any adapter does.
package installation

import (
	"context"
	"errors"
	"fmt"
)

// Kinds of an installation.
const (
	// KindDev marks an installation that opendustpan dev made, for a trial
	// or for CI.
	KindDev Kind = "dev"
	// KindProduction marks an installation that opendustpan serve made, and
	// is the kind of an SDK server that sets none.
	KindProduction Kind = "production"
)

// Sentinel errors of this package and of KindStore implementations.
var (
	// ErrInvalidKind reports a value that is neither dev nor production.
	ErrInvalidKind = errors.New("invalid installation kind")
	// ErrKindMismatch reports a database that another kind claimed. The
	// error is a *MismatchError.
	ErrKindMismatch = errors.New("the database belongs to another installation kind")
	// ErrUnavailable reports that the kind cannot be read or stored. Store
	// adapters translate driver failures to it, so callers never see driver
	// text.
	ErrUnavailable = errors.New("installation kind unavailable")
)

// Kind is the installation kind: dev or production.
type Kind string

// String returns the kind as a plain string.
func (k Kind) String() string {
	return string(k)
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	return k == KindDev || k == KindProduction
}

// ParseKind returns the kind that raw names, or an error that wraps
// ErrInvalidKind.
func ParseKind(raw string) (Kind, error) {
	kind := Kind(raw)
	if !kind.Valid() {
		return "", fmt.Errorf("%w: %q", ErrInvalidKind, raw)
	}

	return kind, nil
}

// MismatchError reports that Stored claimed the database and the caller
// runs as Want. It wraps ErrKindMismatch.
type MismatchError struct {
	Stored Kind
	Want   Kind
}

// Error implements error.
func (e *MismatchError) Error() string {
	return fmt.Sprintf("%s: it holds the kind %s and the caller runs as %s", ErrKindMismatch, e.Stored, e.Want)
}

// Is makes errors.Is(err, ErrKindMismatch) true.
func (e *MismatchError) Is(target error) bool {
	return target == ErrKindMismatch
}

// FromStored returns the kind that a store holds in raw. An adapter calls it
// for the value that it read after a claim. A value that is not a known kind
// is a server fault, so it returns ErrUnavailable and keeps the value out of
// the text.
func FromStored(raw string) (Kind, error) {
	kind := Kind(raw)
	if !kind.Valid() {
		return "", fmt.Errorf("stored installation kind is not a known kind: %w", ErrUnavailable)
	}

	return kind, nil
}

// Check compares the stored value of a database with the kind of the caller
// before the caller changes the schema. An empty stored value is NULL: no
// kind claimed the database yet, so the check passes and the claim decides.
// It returns a *MismatchError when another kind claimed the database, and
// ErrUnavailable when the stored value is not a known kind.
func Check(stored string, want Kind) error {
	if stored == "" {
		return nil
	}

	kind, err := FromStored(stored)
	if err != nil {
		return err
	}

	if kind != want {
		return &MismatchError{Stored: kind, Want: want}
	}

	return nil
}

// KindStore is the port of the installation kind.
type KindStore interface {
	// ClaimKind stores kind when the stored kind is NULL, and returns the
	// stored kind. It returns ErrUnavailable when the row is missing, when
	// the kind cannot be read, or when the stored value is not a known kind.
	// It returns ErrInvalidKind, and changes nothing, when kind is not a
	// known kind.
	ClaimKind(ctx context.Context, kind Kind) (Kind, error)
}
