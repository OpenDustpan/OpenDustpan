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

// Store is the persistence port for reviews.
//
// Each write carries the fence of the namespace of its review, and the
// store checks it in the transaction of the write: the fence names the
// namespace of the review at the current failover version. A review of a
// freeze break is in the namespace of its job, so its writes carry the
// fence of that namespace. A review of a break-glass account session is
// in audit.ServerScope, so its writes carry a zero fence, and the store
// refuses a non-zero fence for it. A fence of an unknown namespace gives
// namespace.ErrNotFound, and a fence at an older failover version gives
// namespace.ErrFenced.
//
// Open and Record append the audit entry e in their transaction, and a
// failed write changes nothing and appends nothing.
type Store interface {
	// Open persists r with version 1, or returns the review that already
	// exists for the kind and the reference of r.Use, unchanged. So an
	// open that runs again after a crash makes no second review. It
	// returns ErrInvalid for a review whose use fails Validate, and for a
	// fence that does not belong to r.Use.Scope (a zero fence for a
	// namespace, a fence for audit.ServerScope, or the fence of another
	// namespace), or to the scope of the review that exists. It returns
	// namespace.ErrNotFound and namespace.ErrFenced as the Store
	// describes. It appends e only when it creates the review.
	Open(ctx context.Context, r Review, fence namespace.Fence, e audit.Entry) (Review, error)
	// Get returns the review id, or ErrNotFound.
	Get(ctx context.Context, id string) (Review, error)
	// List returns the reviews that match f, ordered by ID.
	List(ctx context.Context, f Filter) ([]Review, error)
	// Record stores the verdict of r when r.Version is the stored version,
	// and increments the version. It checks the verdict against the stored
	// review with CheckVerdict, not against r.Use. It returns ErrNotFound
	// for an unknown review and for a fence that does not belong to the
	// scope of the stored review, ErrConflict for an older version, the
	// errors of CheckVerdict (ErrReviewed, ErrSelfReview, ErrInUse,
	// ErrInvalid), and namespace.ErrNotFound and namespace.ErrFenced as
	// the Store describes.
	Record(ctx context.Context, r Review, fence namespace.Fence, e audit.Entry) error
	// Advance stores r.Step and r.NextNoticeAt when r.Version is the
	// stored version, and increments the version. It returns ErrNotFound
	// for an unknown review and for a fence that does not belong to the
	// scope of the stored review, ErrConflict for an older version, and
	// namespace.ErrNotFound and namespace.ErrFenced as the Store
	// describes. It writes no audit entry: a notice is not an action of a
	// person.
	Advance(ctx context.Context, r Review, fence namespace.Fence) error
	// Due returns at most limit pending reviews whose NextNoticeAt is at
	// or before now, oldest first.
	Due(ctx context.Context, now time.Time, limit int) ([]Review, error)
}
