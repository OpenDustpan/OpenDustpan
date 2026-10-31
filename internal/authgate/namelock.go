package authgate

import (
	"context"
	"errors"
	"time"
)

// Defaults of the lockout by name: 10 wrong passwords in a row lock a name
// for 30 minutes.
const (
	DefaultNameLimit   = 10
	DefaultNameLockFor = 30 * time.Minute
)

// ErrNameLocked reports a name that is locked after too many failures in a
// row. A sign-in service answers it with the same error as a wrong
// password, so that the answer does not tell that a user exists.
var ErrNameLocked = errors.New("authgate: the name is locked after failed attempts")

// NameLock is the state of one name in the lockout by name. Failures is the
// count of failures in a row. LockedUntil is the end of the lock, or the
// zero time.
type NameLock struct {
	LockedUntil time.Time
	Failures    int
}

// NameLockCounter counts the failures in a row of each name in a scope, so
// that every instance on one database shares one lockout. The key is a
// keyed digest of the name that the caller computes, so the store holds no
// user name, and an unknown name counts the same as a known one. The store
// takes the time of each call from its own clock.
//
// The caller adds a failure before it checks the credential, and clears the
// count after a good credential. So of calls at the same time, no more than
// the limit pass. The conformance suite RunNameLocks in package
// authgatetest checks an implementation.
type NameLockCounter interface {
	// AddNameFailure adds one failure to key in scope, in one atomic write.
	// The add that takes the count to limit sets the lock to the time of
	// the add plus lockFor, and passes. An add while the lock holds counts
	// nothing and returns ErrNameLocked. An add after the lock ended starts
	// a new count at 1. It returns an error that is not ErrNameLocked when
	// the write fails, and the caller then refuses the attempt.
	AddNameFailure(ctx context.Context, scope, key string, limit int, lockFor time.Duration) (NameLock, error)
	// ClearNameFailures sets the count of key in scope to 0 and ends its
	// lock. A good credential and an unlock by an administrator call it. A
	// key with no count is no error.
	ClearNameFailures(ctx context.Context, scope, key string) error
	// NameLockState returns the state of key in scope, with no change. A key
	// with no count has the zero state.
	NameLockState(ctx context.Context, scope, key string) (NameLock, error)
	// PruneNameLocks deletes each count whose last failure is age or longer
	// ago and whose lock ended, and returns how many it deleted.
	PruneNameLocks(ctx context.Context, age time.Duration) (int64, error)
}
