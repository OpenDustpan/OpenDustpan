package durable

import (
	"context"
	"time"
)

// Store is the persistence port for durable operations. Each SQL store
// adapter implements it in its own SQL, and runs the conformance suite in
// package durabletest.
//
// Every decision about time uses the clock of the store, never the clock of
// the caller. So an instance with a wrong clock cannot take or keep a claim
// early or late. Every method that takes a Claim or a StepFence checks the
// token in the transaction of its write.
//
// A store method of another port can also take a StepFence or a
// StartRequest. Such a method checks the fence, or starts the operation, in
// the transaction of its own write, with the rules of RecordStep or Start.
// Then a domain write and its step record, or a domain write and the start of
// an operation, commit together or not at all. Such a method takes the time
// of the expiry check when it checks, not when its transaction starts, so a
// claim that expires during a long transaction cannot write.
//
// A method that returns an error changes nothing, with one exception: Claim
// can end expired operations with a requested cancel before it returns
// ErrNoWork.
//
//nolint:interfacebloat // One contract for one row per operation: every method reads or changes that row.
type Store interface {
	// Start creates a pending operation for req, with req.Version, or
	// returns the operation that req.Key already names. created is true only
	// for a new operation.
	// It returns ErrInvalid for a request that fails Validate, and
	// ErrKeyConflict when the key names an operation of another kind. A
	// second start with the same key and kind returns the first operation,
	// whatever its input, version and state.
	Start(ctx context.Context, req StartRequest) (op Operation, created bool, err error)

	// Get returns the operation id, or ErrNotFound.
	Get(ctx context.Context, id ID) (Operation, error)

	// List returns the operations that f selects, oldest first. Two
	// operations with one creation time are ordered by ID.
	List(ctx context.Context, f Filter) ([]Operation, error)

	// Claim takes one operation of req.Kinds that can run now, and returns
	// it with its new claim. An operation can run when it is pending and its
	// NotBefore has passed, or when it is running and its claim expired.
	// The claim sets the state to running, increments the token and the
	// attempt, records req.Owner, and sets the expiry to now plus req.Lease.
	// A claim of a running operation whose claim expired also increments
	// Expiries.
	// An operation with an expired claim and a requested cancel ends
	// canceled instead, and Claim looks for another one. The claim commits before Claim returns, and Claim holds no
	// lock after it returns. Of several concurrent claims of one operation,
	// at most one succeeds. It returns ErrNoWork when no operation can run
	// now, and ErrInvalid for a request that fails Validate.
	Claim(ctx context.Context, req ClaimRequest) (Operation, error)

	// Extend moves the expiry of c to now plus lease, and returns the new
	// claim. It returns ErrClaimLost when c is not the live claim, also when
	// c expired and no other claim took the operation. It returns
	// ErrCanceled when a cancel is requested. It returns ErrInvalid for a
	// lease that is not positive. On an error it changes nothing.
	Extend(ctx context.Context, c Claim, lease time.Duration) (Claim, error)

	// RecordStep records the step f.Step with result. It checks in this
	// order, and returns the error of the first check that fails:
	// ErrClaimLost when f does not carry the live claim, ErrCanceled when a
	// cancel is requested, and ErrStepDone when the operation already
	// recorded the step. On an error it changes nothing. So a replaced
	// holder that writes a done step gets ErrClaimLost.
	RecordStep(ctx context.Context, f StepFence, result []byte) error

	// Complete ends the operation of c as succeeded and clears its claim.
	// It returns ErrClaimLost when c is not the live claim, and ErrCanceled
	// when a cancel is requested. On an error it changes nothing: the holder
	// then calls Release, which ends the operation canceled.
	Complete(ctx context.Context, c Claim) error

	// Fail ends the attempt of c and records f.Message as the last error.
	// With f.Final, the operation ends failed. Otherwise it returns to
	// pending with NotBefore at now plus f.RetryAfter. The claim is cleared
	// in both cases. It returns ErrClaimLost when c is not the live claim,
	// and then changes nothing. When a cancel is requested, it ends the
	// operation canceled instead, and returns nil.
	Fail(ctx context.Context, c Claim, f Failure) error

	// Release returns the operation of c to pending without a failure, for
	// a holder that stops before the operation ends. The attempt count
	// stays. It returns ErrClaimLost when c is not the live claim, and then
	// changes nothing. When a cancel is requested, it ends the operation
	// canceled instead, and returns nil.
	Release(ctx context.Context, c Claim) error

	// Cancel stops the operation id. A pending operation ends canceled at
	// once. A running operation records the request. Its holder then gets
	// ErrCanceled from Extend, RecordStep, a step write of another port and
	// Complete, which change nothing. Fail, Release, or a claim after the
	// expiry ends the operation canceled. A second cancel changes nothing. It
	// returns ErrNotFound for an unknown ID, and ErrFinished for an
	// operation that succeeded or failed.
	Cancel(ctx context.Context, id ID) error
}
