// Package history holds the tool-neutral parts of the history of a unit: the
// actor of an item, the redaction of any item kind (state, plan, job log)
// through one Redactor port and one RedactionLog, and the retention rule: the
// expiry of an item is a redaction with the cause CauseRetention. The job
// model stores plans and job logs and implements Redactor for them.
//
// A history item is one entry in the history of a unit that has content:
// one Pulumi update, one OpenTofu state version, one stored plan or one
// version of a job log. Each tool keeps its own state items in its own
// tables. Shared code names an item by its unit, its kind and the ID that
// the tool gives it, and never parses that ID.
//
// Each item has its own item key. A redaction shreds that key and deletes
// the content, in the live blob store and in every backup target, and keeps
// the metadata and the hashes of the item.
package history

import (
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// Sentinel errors of the history ports.
var (
	// ErrInvalidItem reports a unit or item reference that fails
	// validation.
	ErrInvalidItem = errors.New("invalid history item")
	// ErrItemNotFound reports a history item that does not exist.
	ErrItemNotFound = errors.New("history item not found")
	// ErrLatest reports a redaction of the latest state item of a unit,
	// which includes the replay base of an update that has not ended or
	// whose journal is not replayed yet. The latest state cannot be
	// redacted.
	ErrLatest = errors.New("the latest state cannot be redacted")
	// ErrInProgress reports a redaction of an item that is not finished: an
	// update that runs, a state version that is pending, the log of a job
	// step that runs, or any item of a stack while an update on that stack
	// is active.
	ErrInProgress = errors.New("the history item is not finished")
	// ErrBlamePending reports a redaction of a state item before blame
	// compared the item and the next state item. The client can try again.
	ErrBlamePending = errors.New("blame for the history item is not ready")
	// ErrRedacted reports a redaction of an item that is already redacted,
	// or a read of the content of a redacted item.
	ErrRedacted = errors.New("the history item is redacted")
	// ErrRedactionNotFound reports a redaction record that does not exist.
	ErrRedactionNotFound = errors.New("redaction not found")
	// ErrTargetNotFound reports a shred target that a redaction does not
	// name.
	ErrTargetNotFound = errors.New("shred target not found")
	// ErrInvalidReason reports a redaction reason that is empty, too long
	// or holds control characters.
	ErrInvalidReason = errors.New("invalid redaction reason")
	// ErrInvalidRedaction reports a redaction record that fails validation
	// for another cause than its reason.
	ErrInvalidRedaction = errors.New("invalid redaction")
)

// Limits of the references.
const (
	maxToolLength = 100
	maxIDLength   = 100
)

// Unit names one unit in shared code: its namespace, its tool and its
// stable ID. The core keeps the tool and the ID opaque.
type Unit struct {
	Namespace namespace.Name
	Tool      string
	ID        string
}

// Validate reports whether u holds a valid namespace, tool and ID. It
// returns ErrInvalidItem otherwise.
func (u Unit) Validate() error {
	if _, err := namespace.ParseName(u.Namespace.String()); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidItem, err)
	}

	if !ident.Valid(u.Tool, maxToolLength) {
		return fmt.Errorf("%w: tool %q", ErrInvalidItem, u.Tool)
	}

	if !ident.Valid(u.ID, maxIDLength) {
		return fmt.Errorf("%w: unit ID %q", ErrInvalidItem, u.ID)
	}

	return nil
}

// ItemRef names one history item: its unit, its kind and the ID that the
// tool gives it, for example an update ID, a state version ID, a plan ID
// or the ID of a job log version.
type ItemRef struct {
	Unit Unit
	Kind secret.ItemKind
	Item string
}

// Validate reports whether r is a valid reference. It returns
// ErrInvalidItem otherwise.
func (r ItemRef) Validate() error {
	if err := r.Unit.Validate(); err != nil {
		return err
	}

	if !r.Kind.Valid() {
		return fmt.Errorf("%w: kind %q", ErrInvalidItem, r.Kind)
	}

	if !ident.Valid(r.Item, maxIDLength) {
		return fmt.Errorf("%w: item %q", ErrInvalidItem, r.Item)
	}

	return nil
}

// KeyRef returns the reference of the item key of the item: the scope of
// its unit, its kind and its ID. The scope of a unit is its namespace and
// its stable ID, as for its data key.
func (r ItemRef) KeyRef() (secret.ItemRef, error) {
	scope, err := secret.NewScope(r.Unit.Namespace, r.Unit.ID)
	if err != nil {
		return secret.ItemRef{}, err
	}

	return secret.NewItemRef(scope, r.Kind, r.Item)
}

// Actor is who made a history item or a redaction: the subject of the
// principal, the ID of the access token, and the ID of the job. TokenID is
// empty for a bootstrap token. JobID is empty when no job made the item.
// The subject of the server itself is ServerSubject.
type Actor struct {
	Subject string
	TokenID string
	JobID   string
}

// ServerSubject is the subject of the server when it acts on its own, for
// example at the end of a retention period.
const ServerSubject = "@server"
