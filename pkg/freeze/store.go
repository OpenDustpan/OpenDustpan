package freeze

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store is the persistence port for freeze schedules. A schedule is a row
// in its namespace, never in the server scope. Each write carries the
// fence of the namespace of the row, and the store checks it in the
// transaction of the write: it returns namespace.ErrFenced for a stale
// fence and namespace.ErrNotFound for a namespace that does not exist. A
// zero fence, or the fence of another namespace, fails with the error that
// each method names. Every write appends the audit entry e in its
// transaction, and a failed write appends nothing. The namespace of e must
// be the namespace of the row: a write with an entry of another namespace
// fails with the error of a fence of another namespace and changes
// nothing, so a write never appends to the audit log of another
// namespace.
type Store interface {
	// Create persists s with version 1. It returns ErrInvalid for a
	// schedule that fails Validate, or whose namespace is not the
	// namespace of fence (also for a zero fence), and ErrExists for a
	// used ID.
	Create(ctx context.Context, s Schedule, fence namespace.Fence, e audit.Entry) error
	// Get returns the schedule id, or ErrNotFound.
	Get(ctx context.Context, id string) (Schedule, error)
	// Update replaces the schedule s.ID when s.Version is the stored
	// version, and stores it with the version s.Version plus 1. It
	// returns ErrNotFound for an unknown ID, or for a stored schedule
	// whose namespace is not the namespace of fence (also for a zero
	// fence). It returns ErrInvalid for a schedule that fails Validate or
	// that names another namespace than the stored schedule: a schedule
	// never moves to another namespace. It returns ErrConflict for an
	// older version.
	Update(ctx context.Context, s Schedule, fence namespace.Fence, e audit.Entry) error
	// Delete deletes the schedule id. It returns ErrNotFound for an
	// unknown ID, or for a schedule whose namespace is not the namespace
	// of fence (also for a zero fence). It deletes no freeze: the caller
	// then calls Sink.DeleteScheduled in the same request. The scheduled
	// run also calls Sink.DeleteScheduled for each schedule that has
	// freezes but no row, so a request that fails between the two calls
	// leaves no freeze that did not start.
	Delete(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error
	// List returns the schedules of ns, ordered by ID.
	List(ctx context.Context, ns namespace.Name) ([]Schedule, error)
}

// Sink is the freeze store of the job model, with the writes of the
// schedules. The freezes of a schedule are rows of the same table as the
// freezes that a person creates, so the dispatch check reads one table,
// and a person ends a freeze of a schedule with EndFreeze, as any other
// freeze. The Sink and the Store use one database, because
// ReplaceScheduled reads the stored schedule in its transaction.
//
// The freeze of an occurrence o of the schedule s is s.FreezeOf(o). The
// freezes of a schedule are the freezes with the creator Subject(s.ID) in
// the namespace of the fence: each write of a schedule matches a freeze by
// its creator and its namespace, and never reads or changes a freeze of
// another namespace. A schedule ID that is free again after a delete can
// be the ID of a schedule in another namespace, so a creator alone does
// not name the freezes of one schedule. CreateFreeze returns ErrInvalid of
// the job model for a freeze whose CreatedBy starts with SubjectPrefix, so
// a person cannot make a freeze that the Sink takes as a freeze of a
// schedule. It returns ErrInvalid of the job model for a freeze ID that
// exists, in any namespace, and changes nothing: the job model has no
// sentinel for an existing freeze. It returns the error of a fence of another namespace for an
// audit entry of another namespace than the freeze. A
// freeze started when its start is at or before now on the clock of the
// store. The Sink never changes, deletes or adds again a freeze whose
// EndedBy is set: only a person ends a freeze early, and the person
// stays the last word. The writes of a schedule check the fence of the
// namespace of their rows and append no audit entry: the change of the
// schedule wrote one, and the scheduled run is not an action of a
// person.
type Sink interface {
	job.FreezeStore

	// ReplaceScheduled makes the freezes of the schedule s agree with
	// occs, the occurrences of a plan of s:
	//   - For each freeze of s that did not start: it adds each missing
	//     one, sets the end and the reason of each other one, and deletes
	//     each one that occs does not have.
	//   - For each freeze of s that started and that no person ended: when
	//     the occurrence with its ID ends later, it moves the end later.
	//     It never moves an end earlier, and never deletes such a freeze.
	//   - For each occurrence that started and that has no freeze: it adds
	//     the freeze only when no freeze of s overlaps the occurrence,
	//     also a freeze that a person ended. So a freeze that a person
	//     ended does not come back with another ID after a change of the
	//     calendar, of the schedule or of the time zone data.
	//
	// It checks fence and the version of s in its transaction. It returns
	// ErrNotFound for an unknown schedule, or for a stored schedule whose
	// namespace is not the namespace of fence (also for a zero fence). It
	// returns ErrConflict when s.Version is not the stored version, and
	// ErrInvalid for an occurrence of another schedule or with an end
	// that is not after its start. On an error it changes nothing.
	ReplaceScheduled(ctx context.Context, s Schedule, occs []Occurrence, fence namespace.Fence) error
	// DeleteScheduled deletes each freeze of the schedule id in the
	// namespace of fence that did not start and that no person ended. It
	// is for a schedule that no longer exists: it returns ErrExists when
	// the schedule id exists, in any namespace. It returns ErrNotFound for
	// a zero fence. It never deletes a freeze of another namespace. With
	// no freeze to delete it returns nil.
	DeleteScheduled(ctx context.Context, id string, fence namespace.Fence) error
}
