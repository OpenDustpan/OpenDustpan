package incident

import (
	"context"
	"time"

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

// StepRequest is one step of an incident for Store.Step: the step Step
// by the principal By at At, on the version Version of the incident ID.
type StepRequest struct {
	ID      string
	Version int64
	Step    Step
	By      string
	At      time.Time
}

// Store is the persistence port for incidents and their timelines. An
// incident is a write in its namespace: each write carries the fence of
// the namespace of the incident and appends the audit entry e in its
// transaction. A failed write changes nothing and appends no entry.
//
// The store checks the fence against the namespace of the row in the
// transaction. An incident is always in a namespace, never in the server
// scope, so a zero fence is always refused. For a create, which names the
// namespace, a zero fence or the fence of another namespace is ErrInvalid.
// For a write on a stored incident, a zero fence or the fence of another
// namespace is ErrNotFound, so the error does not tell whether the
// incident exists in another namespace. A fence at an older failover
// version is namespace.ErrFenced.
//
// Each audit entry of a write names the namespace of the incident. The
// store refuses a write whose entry names another namespace, with the
// error of a fence of another namespace for that write, and changes
// nothing, so a write cannot append to the audit log of another
// namespace.
type Store interface {
	// Create persists in with version 1. It returns ErrInvalid for an
	// incident that fails ValidateNew, so an incident starts open, and
	// for a zero fence or the fence of another namespace; ErrExists; and
	// namespace.ErrFenced.
	Create(ctx context.Context, in Incident, fence namespace.Fence, e audit.Entry) error
	// Get returns the incident id, or ErrNotFound.
	Get(ctx context.Context, id string) (Incident, error)
	// Update replaces the fields of in.ID that a person edits when
	// in.Version is the stored version, and increments the version. It
	// never changes the ID, the namespace, the state, the step times,
	// OpenedAt, OpenedBy, the classifications or the reports: the store
	// keeps the stored ones. Step changes the state and the step times,
	// and SetClassification and AddReport append to the lists. It
	// returns ErrNotFound for an unknown incident, a zero fence or the
	// fence of another namespace; ErrInvalid for an incident that fails
	// Validate after the merge or that names another namespace;
	// ErrConflict; ErrTransition for a change of a closed incident; and
	// namespace.ErrFenced.
	Update(ctx context.Context, in Incident, fence namespace.Fence, e audit.Entry) error
	// Step runs Apply with r.Step, r.By and r.At on the stored incident
	// r.ID in the transaction, when r.Version is the stored version,
	// stores the result, increments the version and returns the stored
	// incident. It
	// returns ErrNotFound for an unknown incident, a zero fence or the
	// fence of another namespace; ErrConflict; the errors of Apply
	// (ErrTransition, and ErrInvalid for a close with no review);
	// ErrInvalid for a result that fails Validate; and
	// namespace.ErrFenced.
	Step(ctx context.Context, r StepRequest, fence namespace.Fence, e audit.Entry) (Incident, error)
	// SetClassification appends c to the classifications of the incident
	// id, so c is the current classification, and increments the
	// version. A closed incident takes it too, because a classification
	// can change after the close. It returns ErrNotFound for an unknown
	// incident, a zero fence or the fence of another namespace;
	// ErrInvalid for c that fails Validate or for an incident with
	// MaxRecords classifications; and namespace.ErrFenced.
	SetClassification(ctx context.Context, id string, c Classification, fence namespace.Fence, e audit.Entry) error
	// AddReport appends r to the reports of the incident id and
	// increments the version. A closed incident takes it too, because a
	// final report can come after the close. It returns ErrNotFound for
	// an unknown incident, a zero fence or the fence of another
	// namespace; ErrInvalid for r that fails Validate or for an incident
	// with MaxRecords reports; and namespace.ErrFenced.
	AddReport(ctx context.Context, id string, r Report, fence namespace.Fence, e audit.Entry) error
	// List returns the incidents that match f, newest first.
	List(ctx context.Context, f Filter) ([]Incident, error)

	Timeline
}

// Timeline is the part of Store that holds the notes of the timeline of
// an incident. Its writes follow the rules of Store.
type Timeline interface {
	// AddNote appends n to the timeline of its incident. A closed
	// incident still takes notes. It returns ErrNotFound for an unknown
	// incident, a zero fence or the fence of another namespace;
	// ErrInvalid for an empty note or a note above MaxNoteLen; and
	// namespace.ErrFenced.
	AddNote(ctx context.Context, n Note, fence namespace.Fence, e audit.Entry) error
	// Notes returns the timeline of the incident id, oldest first.
	Notes(ctx context.Context, id string) ([]Note, error)
}
