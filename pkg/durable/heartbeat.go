package durable

import (
	"context"
	"time"
)

// HeartbeatRetention is the age after which a store deletes the beat of an
// owner. A beat call deletes the beats that are older than this, so the rows
// of dead workers do not grow without bound.
const HeartbeatRetention = time.Hour

// Beat is the most recent heartbeat in the store, on the clock of the store.
type Beat struct {
	// Owner is the owner of the latest beat. It is empty when there is none.
	Owner string
	// At is the time of the latest beat. It is zero when there is none.
	At time.Time
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
	// Beat records that owner claimed or renewed work now. It keeps one
	// beat for each owner, at the time of the store clock, and deletes the
	// beats that are older than HeartbeatRetention in the same transaction.
	// It returns ErrInvalid for an empty owner.
	Beat(ctx context.Context, owner string) error

	// LastBeat returns the latest beat of any owner. When there is no beat,
	// At is zero and Owner is empty, and the error is nil. Now is always
	// set.
	LastBeat(ctx context.Context) (Beat, error)
}
