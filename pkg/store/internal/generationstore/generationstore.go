// Package generationstore holds the one bump of the session generation of a
// principal that both SQL adapters share. session.PrincipalStateStore and
// the writes of the local user store call it, in their own transaction, so
// that each bump checks its cause, adds 1, and appends the audit entry
// session:generation-bumped that names the cause.
package generationstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// ErrNoRow reports a principal that the read of a Tx did not find.
var ErrNoRow = errors.New("generationstore: no row")

// Tx is the part of one transaction of an adapter that Bump needs.
type Tx interface {
	// PrincipalKind reads the kind of the principal id that exists (not
	// deleted), or returns ErrNoRow.
	PrincipalKind(ctx context.Context, id string) (string, error)
	// BumpGeneration adds 1 to the stored generation of the principal id and
	// returns the count of rows that it wrote.
	BumpGeneration(ctx context.Context, id string) (int64, error)
	// Generation reads the stored generation of the principal id: 1 when it
	// has no row.
	Generation(ctx context.Context, id string) (int64, error)
	// Append appends e to the audit log of the installation.
	Append(ctx context.Context, e audit.Entry) error
}

// Bump adds 1 to the session generation of the person id for cause, in the
// transaction of tx, and appends e with the cause and the new generation as
// its changes. It returns the new generation. It returns session.ErrInvalid
// for a cause that session.ValidBumpCause refuses,
// session.ErrPrincipalNotFound for a principal that does not exist or is not
// a person, and audit.ErrInvalidEntry for an entry that fails validation.
func Bump(ctx context.Context, tx Tx, id string, cause session.BumpCause, e audit.Entry) (uint64, error) {
	if err := session.ValidBumpCause(cause); err != nil {
		return 0, err
	}

	kind, err := tx.PrincipalKind(ctx, id)
	if errors.Is(err, ErrNoRow) {
		return 0, fmt.Errorf("%w: %s", session.ErrPrincipalNotFound, id)
	}

	if err != nil {
		return 0, fmt.Errorf("read the principal: %w", err)
	}

	if kind != string(principal.KindPerson) {
		return 0, fmt.Errorf("%w: the principal is a %s", session.ErrPrincipalNotFound, kind)
	}

	n, err := tx.BumpGeneration(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("bump the session generation: %w", err)
	}

	if n != 1 {
		return 0, fmt.Errorf("bump the session generation: %w", session.ErrPrincipalNotFound)
	}

	stored, err := tx.Generation(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("read the session generation: %w", err)
	}

	g, err := FromRow(stored)
	if err != nil {
		return 0, err
	}

	e.Changes = append(append([]audit.Change{}, e.Changes...),
		audit.Change{Key: "cause", Old: nil, New: string(cause), Layer: "", TakesEffect: ""},
		audit.Change{Key: "generation", Old: nil, New: strconv.FormatUint(g, 10), Layer: "", TakesEffect: ""},
	)

	if err := tx.Append(ctx, e); err != nil {
		return 0, fmt.Errorf("append the entry of the bump: %w", err)
	}

	return g, nil
}

// EntryOf returns the entry session:generation-bumped of a bump of the
// principal id that the change with the entry cause makes: the actor and the
// source of cause, and the action of cause as a change, so the log names the
// change that ended the sessions.
func EntryOf(cause audit.Entry, id string) audit.Entry {
	e := cause
	e.Action = session.ActionGenerationBumped
	e.Object = principal.Ref(id)
	e.ObjectID = ""
	e.Changes = []audit.Change{{Key: "causeAction", Old: nil, New: cause.Action, Layer: "", TakesEffect: ""}}

	return e
}

// FromRow returns the generation of a stored value. A value below 1 is a
// changed row or a row of another version: it fails closed with
// session.ErrInvalid.
func FromRow(v int64) (uint64, error) {
	if v < 1 {
		return 0, fmt.Errorf("%w: a stored session generation of %d", session.ErrInvalid, v)
	}

	return uint64(v), nil
}

// ToRow returns the stored form of the generation g. A generation above the
// range of a stored value fails with session.ErrInvalid.
func ToRow(g uint64) (int64, error) {
	if g > math.MaxInt64 {
		return 0, fmt.Errorf("%w: a session generation of %d", session.ErrInvalid, g)
	}

	return int64(g), nil
}

// OfSession returns the generation of a stored session row. A session from
// before the generation existed has 0, which no principal has, so it never
// authenticates. A value below 0 is a changed row and reads as 0 too.
func OfSession(v int64) uint64 {
	if v < 1 {
		return 0
	}

	return uint64(v)
}
