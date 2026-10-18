package freeze

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store is the persistence port for freeze schedules. A schedule is a
// write in its namespace: each write carries the fence of the namespace,
// and the store checks it in the transaction of the write. Every write
// appends the audit entry e in its transaction, and a failed write
// appends nothing.
type Store interface {
	// Create persists s with version 1. It returns ErrInvalid for a
	// schedule that fails Validate, ErrExists for a used ID, and
	// namespace.ErrFenced for a stale fence.
	Create(ctx context.Context, s Schedule, fence namespace.Fence, e audit.Entry) error
	// Get returns the schedule id, or ErrNotFound.
	Get(ctx context.Context, id string) (Schedule, error)
	// Update replaces s.ID when s.Version is the stored version, and
	// increments the version. It returns ErrNotFound, ErrInvalid,
	// ErrConflict for an older version, and namespace.ErrFenced.
	Update(ctx context.Context, s Schedule, fence namespace.Fence, e audit.Entry) error
	// Delete deletes the schedule id. It returns ErrNotFound and
	// namespace.ErrFenced. The freezes that the schedule made and that
	// started before the delete stay. The caller removes the others with
	// Sink.ReplaceScheduled in the same request.
	Delete(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error
	// List returns the schedules of ns, ordered by ID.
	List(ctx context.Context, ns namespace.Name) ([]Schedule, error)
}

// Sink writes the freezes that a schedule makes. The store of the
// job model implements it, on the same table as the freezes that a person
// creates, so the dispatch check reads one table. Each freeze of an
// occurrence has the ID OccurrenceID(o.Schedule, o.Start), the reason of the
// schedule, and the schedule ID as its origin.
type Sink interface {
	// ReplaceScheduled makes the freezes of the schedule id that start at
	// or after from equal to the occurrences of occs that start at or
	// after from: it adds the missing ones, changes the end of the
	// changed ones, and deletes the others. An occurrence that starts
	// before from is added only when no freeze has its ID, so a freeze
	// that a person ended early never comes back. A freeze of the
	// schedule that started before from never changes. It checks fence in
	// its transaction and returns namespace.ErrFenced for a stale one. It
	// writes no audit entry: the change of the schedule wrote one, and a
	// scheduled run is not an action of a person.
	ReplaceScheduled(
		ctx context.Context, fence namespace.Fence, id string, from time.Time, occs []Occurrence,
	) error
}
