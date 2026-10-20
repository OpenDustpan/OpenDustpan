package preferences

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
)

// Tx is what a store gives inside one of its transactions. Change runs the
// rules of an update on it, so each adapter keeps the SQL and nothing more,
// and the rules have one source.
type Tx interface {
	// Load returns the stored document and its time in Unix milliseconds for
	// the person personID, and an empty document when the person has no row.
	// A store that has row locks takes the lock of the person here, so two
	// updates of one person wait for each other. It returns ErrPersonNotFound
	// when no person with that ID exists.
	Load(ctx context.Context, personID string) (document string, updatedAtMs int64, err error)
	// Save writes the row of the person.
	Save(ctx context.Context, personID, document string, updatedAtMs int64) error
	// Append appends the audit entry e in the scope audit.ServerScope. It
	// returns audit.ErrInvalidEntry when e fails validation.
	Append(ctx context.Context, e audit.Entry) error
}

// Change applies patch to the preferences of the person personID through tx,
// as the Update of a Store must: it reads the row, applies the patch with
// Apply (which checks every value), and, when a value changes, writes the row
// with the time of e and appends the entry with the changed field names. A
// patch that changes nothing writes nothing. The caller runs it in one
// transaction, so a failed call changes nothing.
func Change(ctx context.Context, tx Tx, personID string, patch Patch, e audit.Entry) (Result, error) {
	none := Result{Document: Default(), Changed: nil}

	document, updatedAtMs, err := tx.Load(ctx, personID)
	if err != nil {
		return none, err
	}

	current, updatedAt, err := Load(document, updatedAtMs)
	if err != nil {
		return none, fmt.Errorf("read preferences: %w", err)
	}

	next, changed, err := Apply(current, patch)
	if err != nil {
		return none, fmt.Errorf("apply patch: %w", err)
	}

	if len(changed) == 0 {
		return Result{Document: current.DocumentAt(updatedAt), Changed: []string{}}, nil
	}

	text, err := next.Encode()
	if err != nil {
		return none, fmt.Errorf("encode preferences: %w", err)
	}

	at := e.At.UTC().Truncate(millisecond)

	if err := tx.Save(ctx, personID, text, at.UnixMilli()); err != nil {
		return none, fmt.Errorf("write preferences: %w", err)
	}

	if err := tx.Append(ctx, ChangeEntry(e, changed)); err != nil {
		return none, fmt.Errorf("append audit entry: %w", err)
	}

	return Result{Document: next.DocumentAt(at), Changed: changed}, nil
}
