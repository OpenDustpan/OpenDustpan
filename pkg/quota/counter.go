package quota

import (
	"context"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// ReservationID identifies a reservation. The Catalog uses the revision
// that the reservation holds a unit for: <request ID>/<revision>.
type ReservationID string

// ReservationState is where a reservation is in its life.
type ReservationState string

// The states of a reservation.
const (
	// ReservationOpen counts as reserved until its end time.
	ReservationOpen ReservationState = "open"
	// ReservationSettled moved its unit to the settled count: the revision
	// was applied.
	ReservationSettled ReservationState = "settled"
	// ReservationReleased counts no more: the revision ended with no unit.
	ReservationReleased ReservationState = "released"
)

// Reservation holds one unit of each of its keys for a create in flight.
// At is the time of the submit: Reserve counts the usage at At. EndsAt is
// the end time of the job of the revision plus ReservationGrace. After it,
// an open reservation counts in no usage.
type Reservation struct {
	ID        ReservationID
	Namespace namespace.Name
	State     ReservationState
	Keys      []Key
	At        time.Time
	EndsAt    time.Time
}

// Validate reports whether r is a new reservation that the counter can
// store: an ID, keys of KindUnits in its namespace with no duplicate, a
// time, an end time after it, and the state open. It returns ErrInvalid
// otherwise.
func (r Reservation) Validate() error {
	switch {
	case r.ID == "":
		return fmt.Errorf("%w: empty reservation ID", ErrInvalid)
	case r.State != ReservationOpen:
		return fmt.Errorf("%w: a new reservation is open, not %q", ErrInvalid, r.State)
	case r.At.IsZero() || !r.EndsAt.After(r.At):
		return fmt.Errorf("%w: reservation %s ends at %v, not after %v", ErrInvalid, r.ID, r.EndsAt, r.At)
	case len(r.Keys) == 0:
		return fmt.Errorf("%w: reservation %s has no key", ErrInvalid, r.ID)
	default:
	}

	for i, k := range r.Keys {
		if k.Kind != KindUnits || k.Namespace != r.Namespace {
			return fmt.Errorf("%w: key %s of reservation %s", ErrInvalid, k, r.ID)
		}

		if slices.Contains(r.Keys[:i], k) {
			return fmt.Errorf("%w: key %s twice in reservation %s", ErrInvalid, k, r.ID)
		}
	}

	return nil
}

// Counter is the port of quota accounting. The default adapter keeps a row
// for each key and a row for each reservation and each request, in the
// database of the namespace. It serializes the writes of one key, for
// example with a row lock on the row of the key, so that two writes at the
// same time never both pass a limit. Each adapter runs the suite in package
// quotatest.
//
// Each write checks fence in its transaction and returns an error that
// wraps namespace.ErrFenced when the fence is stale.
type Counter interface {
	// Reserve checks each of checks with r counted, with the usage at r.At,
	// and stores r only when each one passes, in one transaction. A reservation with the ID of an
	// open or ended one and the same keys returns the stored one, with no
	// change, so a retry is safe. It returns an *ExceededError that matches
	// ErrExceeded with each check that fails, ErrConflict for an ID that
	// names other keys, and ErrInvalid for a reservation that fails
	// Reservation.Validate.
	Reserve(ctx context.Context, r Reservation, checks []Check, fence namespace.Fence) (Reservation, error)
	// Settle moves the units of reservation id from reserved to settled. A
	// settle of a settled reservation changes nothing. It returns
	// ErrNotFound for an unknown ID, and ErrEnded for a released one.
	Settle(ctx context.Context, id ReservationID, fence namespace.Fence) error
	// Release ends reservation id with no unit. A release of a released
	// reservation changes nothing. It returns ErrNotFound for an unknown ID,
	// and ErrEnded for a settled one.
	Release(ctx context.Context, id ReservationID, fence namespace.Fence) error
	// Record checks each of checks, which are of KindRequests, with the
	// request at the time submitted counted in the window of each. It stores
	// the request for key only when each one passes, in one transaction. It
	// returns an *ExceededError that matches ErrExceeded otherwise.
	Record(ctx context.Context, key Key, submitted time.Time, checks []Check, fence namespace.Fence) error
	// Usage returns the usage of the key of each of checks at the time
	// when, in the order of checks: for a key of KindUnits, the settled count
	// and the open reservations whose end time is after when, and for a key
	// of KindRequests, the requests in the window of the check that ends at
	// when. A key that the counter does not know uses nothing.
	Usage(ctx context.Context, checks []Check, when time.Time) ([]Usage, error)
	// SetSettled writes the settled count of key, as the reconcile
	// schedule counts it from the inventory. It returns ErrInvalid for a
	// key that is not of KindUnits or a count below 0.
	SetSettled(ctx context.Context, key Key, count int, fence namespace.Fence) error
}
