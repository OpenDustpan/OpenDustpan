package authgate

import (
	"context"
	"errors"
	"time"
)

// Scopes of the lockout by name. A scope holds the letters a to z only.
const (
	// ScopePassword counts the failures of one user name from one source
	// prefix: an IPv4 address, or the /64 of an IPv6 address. The key joins
	// the keyed digest of the name and the prefix.
	ScopePassword = "password"
	// ScopePasswordName counts the failures of one user name from every
	// source. Its lock holds for every source.
	ScopePasswordName = "passwordname"
	// ScopeFactor counts the wrong second factors of one person across every
	// pending sign-in. The key is the principal ID.
	ScopeFactor = "factor"
)

// Defaults of the lockout by name.
const (
	// DefaultNameLimit and DefaultNameLockFor: 10 failures in a row of one
	// name from one source prefix lock that pair for 30 minutes.
	DefaultNameLimit   = 10
	DefaultNameLockFor = 30 * time.Minute
	// DefaultNameGlobalLimit: 100 failures in a row of one name from every
	// source lock the name for every source until an administrator unlocks
	// it. The lock time 0 means that.
	DefaultNameGlobalLimit   = 100
	DefaultNameGlobalLockFor = time.Duration(0)
	// DefaultFactorLimit and DefaultFactorLockFor: 10 wrong second factors
	// in a row of one person lock the factors of the person for 30 minutes.
	DefaultFactorLimit   = 10
	DefaultFactorLockFor = 30 * time.Minute
)

// ErrInvalidLock reports a scope that is not one of the letters a to z, an
// empty key, a limit below 1, or a negative lock time.
var ErrInvalidLock = errors.New("authgate: invalid name lock request")

// ErrNameLocked reports a name that is locked after too many failures in a
// row. A sign-in service answers it with the same error as a wrong
// password, so that the answer does not tell that a user exists.
var ErrNameLocked = errors.New("authgate: the name is locked after failed attempts")

// NameLock is the state of one name in the lockout by name. Failures is the
// count of failures in a row. LockedUntil is the end of a lock with a time,
// or the zero time. UntilCleared marks a lock that holds until a clear.
type NameLock struct {
	LockedUntil  time.Time
	Failures     int
	UntilCleared bool
}

// NameLockCounter counts the failures in a row of each name in a scope, so
// that every instance on one database shares one lockout. The key is a
// keyed digest of the name that the caller computes, so the store holds no
// user name, and an unknown name counts the same as a known one. The store
// takes the time of each call from its own clock.
//
// The caller adds a failure before it checks the credential, and clears the
// count only at the end of a full sign-in. So of calls at the same time, no more than
// the limit pass. The conformance suite RunNameLocks in package
// authgatetest checks an implementation.
type NameLockCounter interface {
	// AddNameFailure adds one failure to key in scope, in one atomic write.
	// The add that takes the count to limit sets the lock to the time of
	// the add plus lockFor, and passes. A lockFor of 0 sets a lock that holds
	// until ClearNameFailures. An add while the lock holds counts
	// nothing and returns ErrNameLocked. An add after the lock ended starts
	// a new count at 1. It returns an error that is not ErrNameLocked when
	// the write fails, and the caller then refuses the attempt.
	AddNameFailure(ctx context.Context, scope, key string, limit int, lockFor time.Duration) (NameLock, error)
	// ClearNameFailures sets the count of key in scope to 0 and ends its
	// lock. The end of a full sign-in, after the second factor when one is
	// needed, and an unlock by an administrator call it. A good password
	// alone never calls it. A key with no count is no error.
	ClearNameFailures(ctx context.Context, scope, key string) error
	// NameLockState returns the state of key in scope, with no change. A key
	// with no count has the zero state.
	NameLockState(ctx context.Context, scope, key string) (NameLock, error)
	// PruneNameLocks deletes each count whose last failure is age or longer
	// ago and whose lock ended, and returns how many it deleted.
	PruneNameLocks(ctx context.Context, age time.Duration) (int64, error)
}
