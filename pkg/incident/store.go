package incident

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Filter selects incidents for List. A zero field selects all.
type Filter struct {
	Namespace namespace.Name
	State     State
	Unit      string
	After     string
	Limit     int
}

// Store is the persistence port for incidents and their timelines. An
// incident is a write in its namespace: each write carries the fence of
// the namespace and appends the audit entry e in its transaction.
type Store interface {
	// Create persists in with version 1. It returns ErrInvalid for an
	// incident that fails Validate, ErrExists, and namespace.ErrFenced.
	Create(ctx context.Context, in Incident, fence namespace.Fence, e audit.Entry) error
	// Get returns the incident id, or ErrNotFound.
	Get(ctx context.Context, id string) (Incident, error)
	// Update replaces in.ID when in.Version is the stored version, and
	// increments the version. It returns ErrNotFound, ErrInvalid,
	// ErrConflict, ErrTransition for a change of a closed incident, and
	// namespace.ErrFenced.
	Update(ctx context.Context, in Incident, fence namespace.Fence, e audit.Entry) error
	// List returns the incidents that match f, newest first.
	List(ctx context.Context, f Filter) ([]Incident, error)
	// AddNote appends n to the timeline of its incident. It returns
	// ErrNotFound, ErrInvalid for an empty note or a note above
	// MaxNoteLen, and namespace.ErrFenced. A closed incident still takes
	// notes.
	AddNote(ctx context.Context, n Note, fence namespace.Fence, e audit.Entry) error
	// Notes returns the timeline of the incident id, oldest first.
	Notes(ctx context.Context, id string) ([]Note, error)
}
