package preferences

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// Store is the persistence port for the preferences of a person. A row
// belongs to a person, not to a namespace, so no write carries a fence.
//
// A store keeps at most one row for each person, and it keeps the row only
// while the person exists: the end of a person ends its row in the same
// transaction.
type Store interface {
	// Get returns the preferences of the person personID: the stored
	// settings over the defaults, with UpdatedAt set to the time of the last
	// change, or the zero time when the person never changed a preference.
	// It returns ErrPersonNotFound when no person with that ID exists. It
	// returns ErrInvalidStored when the row fails the checks of this
	// package, and never returns a value that failed a check.
	Get(ctx context.Context, personID string) (Document, error)

	// Update applies patch to the preferences of the person personID in one
	// transaction, and returns the new document and the flat names of the
	// fields that changed. It checks every value of patch again with Apply.
	//
	// A patch that changes no value writes nothing: no row, no new UpdatedAt
	// and no audit entry, and the result has no changed field. A patch that
	// changes a value writes the row with UpdatedAt = e.At, and appends the
	// audit entry e in the scope audit.ServerScope in the same transaction,
	// with one change for each changed field (see ChangeEntry). The entry
	// holds the names of the fields, never a value. Two updates of one person
	// at the same time both apply: neither one loses the change of the
	// other.
	//
	// It returns ErrPersonNotFound when no person with that ID exists,
	// ErrInvalidValue (a *FieldError) or ErrUnknownField for a patch that
	// fails a check, and audit.ErrInvalidEntry when e fails validation or
	// names another scope than audit.ServerScope, also for a patch that
	// changes nothing. A failed call changes nothing.
	Update(ctx context.Context, personID string, patch Patch, e audit.Entry) (Result, error)
}

// Result is the answer of Update.
type Result struct {
	Document Document
	// Changed holds the flat names of the fields that changed, in the order
	// of the document. It is empty for a patch that changed nothing.
	Changed []string
}

// millisecond is the unit of the time of a row.
const millisecond = time.Millisecond

// changeNew is the value of the change that records a changed field. The
// entry records the name of the field and never its value.
const changeNew = "changed"

// ChangeEntry returns e with one change for each name in changed, and the
// action of the preferences audit entry. A store calls it in the transaction
// of a real change. The change holds the name only: Old is nil and New is a
// fixed word.
func ChangeEntry(e audit.Entry, changed []string) audit.Entry {
	out := e
	out.Action = AuditAction
	out.Changes = make([]audit.Change, 0, len(e.Changes)+len(changed))
	out.Changes = append(out.Changes, e.Changes...)

	for _, name := range changed {
		out.Changes = append(out.Changes, audit.Change{
			Key: name, Old: nil, New: changeNew, Layer: "", TakesEffect: "",
		})
	}

	return out
}

// Load reads the row of a person in a store. An empty document means that the
// person has no row: the settings are then empty and the time is the zero time.
// A stored document is never empty. It returns ErrInvalidStored for a document
// that fails the checks, so a store never returns a value that failed a check,
// also when the row came from an older version or from a restore.
func Load(document string, updatedAtMs int64) (Settings, time.Time, error) {
	if document == "" {
		return Settings{}, time.Time{}, nil
	}

	settings, err := ParseSettings(document)
	if err != nil {
		return nil, time.Time{}, err
	}

	return settings, time.UnixMilli(updatedAtMs).UTC(), nil
}

// DocumentAt returns the document that s gives, with UpdatedAt set to at.
func (s Settings) DocumentAt(at time.Time) Document {
	d := s.Document()
	d.UpdatedAt = at

	return d
}
