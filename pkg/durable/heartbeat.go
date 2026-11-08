package durable

import (
	"context"
	"time"
)

// HeartbeatRetention is the age after which a store deletes the beat of an
// owner. A beat call deletes the beats that are older than this, so the rows
// of dead workers do not grow without bound.
const HeartbeatRetention = time.Hour

// Beat is the heartbeat in the store that stays valid the longest, on the
// clock of the store. A beat stays valid for 2 lease periods of its owner
// after its time, so the beat of an owner with a long lease can outlast a
// later beat of an owner with a short lease.
type Beat struct {
	// Owner is the owner of the beat. It is empty when there is none.
	Owner string
	// At is the time of the beat. It is zero when there is none.
	At time.Time
	// Lease is the lease period that the owner recorded with the beat. It is
	// zero when there is no beat, and when the beat records no lease.
	Lease time.Duration
	// Now is the clock of the store when it read.
	Now time.Time
}

// HeartbeatStore is the persistence port for worker heartbeats. A store
// adapter that implements Store implements it too, and runs the conformance
// suite in package durabletest.
//
// Every time comes from the clock of the store, never the clock of the
// caller.
type HeartbeatStore interface {
	// Beat records that owner claimed or renewed work now, with the lease
	// period that owner claims with. It keeps one beat for each owner, at the
	// time of the store clock, and deletes the beats that are older than
	// HeartbeatRetention in the same transaction. It returns ErrInvalid for
	// an empty owner and for a lease that is zero or negative.
	Beat(ctx context.Context, owner string, lease time.Duration) error

	// LastBeat returns the beat of the owner whose time plus 2 lease periods
	// is the latest, and of those the latest time, then the greatest owner.
	// With one lease period for all owners, this is the latest beat. When
	// there is no beat, At is zero and Owner is empty, and the error is nil.
	// Now is always set.
	LastBeat(ctx context.Context) (Beat, error)
}
