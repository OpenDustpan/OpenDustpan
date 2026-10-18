package breakglass

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Filter selects reviews for List. A zero field selects all.
type Filter struct {
	Scope namespace.Name
	State State
	// DueBefore selects the reviews with a deadline before it, for the
	// list of overdue reviews.
	DueBefore time.Time
	// After is the ID of the last review of the previous page.
	After string
	Limit int
}

// Store is the persistence port for reviews. A review of a freeze break
// is a write in the namespace of its job: each write carries the fence of
// that namespace, and the store checks it in the transaction of the write.
// A review of a break-glass account session is in the server scope and
// carries a zero fence. Open and Record append the audit entry e in their
// transaction, and a failed write appends nothing.
type Store interface {
	// Open persists r with version 1, or returns the review that already
	// exists for the kind and the reference of r.Use, unchanged. So an
	// open that runs again after a crash makes no second review. It
	// returns ErrInvalid for a review whose use fails Validate, and
	// namespace.ErrFenced for a stale fence. It appends e only when it
	// creates the review.
	Open(ctx context.Context, r Review, fence namespace.Fence, e audit.Entry) (Review, error)
	// Get returns the review id, or ErrNotFound.
	Get(ctx context.Context, id string) (Review, error)
	// List returns the reviews that match f, ordered by ID.
	List(ctx context.Context, f Filter) ([]Review, error)
	// Record stores the verdict of r when r.Version is the stored version,
	// and increments the version. It returns ErrNotFound, ErrConflict for
	// an older version, ErrReviewed when the stored review has a verdict,
	// and namespace.ErrFenced.
	Record(ctx context.Context, r Review, fence namespace.Fence, e audit.Entry) error
	// Advance stores r.Step and r.NextNoticeAt when r.Version is the
	// stored version, and increments the version. It returns ErrNotFound,
	// ErrConflict and namespace.ErrFenced. It writes no audit entry: a
	// notice is not an action of a person.
	Advance(ctx context.Context, r Review, fence namespace.Fence) error
	// Due returns at most limit pending reviews whose NextNoticeAt is at
	// or before now, oldest first.
	Due(ctx context.Context, now time.Time, limit int) ([]Review, error)
}
