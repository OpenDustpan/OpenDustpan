package authgate

import (
	"context"
	"errors"
	"time"
)

// Scopes of the lockout by name. A scope holds the letters a to z only.
const (
	// ScopePassword counts the failures of one user name from one source
	// prefix: an IPv4 address, or the /64 of an IPv6 address. The sub-key is
	// the prefix.
	ScopePassword = "password"
	// ScopePasswordName counts the failures of one user name from every
	// source. Its lock holds for every source. The sub-key is empty.
	ScopePasswordName = "passwordname"
	// ScopeFactor counts the wrong second factors of one person across every
	// pending sign-in. The name is the principal ID, and the sub-key is
	// empty.
	ScopeFactor = "factor"
)

// LockRule says when a count locks, and for how long.
//
// The add that takes the count to Limit sets a lock. The first lock of a
// key lasts LockFor. Each new lock of the same key, while an earlier lock
// ended less than ResetAfter ago, lasts twice the one before, up to
// MaxLockFor. After ResetAfter with no lock, the next lock lasts LockFor
// again. A rule with MaxLockFor equal to LockFor never doubles.
type LockRule struct {
	Limit      int
	LockFor    time.Duration
	MaxLockFor time.Duration
	ResetAfter time.Duration
}

// Defaults of the lockout by name.
const (
	// DefaultNameLimit and DefaultNameLockFor: 10 failures in a row of one
	// name from one source prefix lock that
	// pair for 30 minutes.
	DefaultNameLimit   = 10
	DefaultNameLockFor = 30 * time.Minute
	// DefaultNameGlobalLimit and the rest of its rule: 100 failures in a row
	// of one name from every source lock the name for
	// every source for 30 minutes, then 60, 120 and so on, up to 24 hours.
	// After 24 hours with no global lock, the next lock lasts 30 minutes.
	DefaultNameGlobalLimit      = 100
	DefaultNameGlobalLockFor    = 30 * time.Minute
	DefaultNameGlobalMaxLockFor = 24 * time.Hour
	DefaultNameGlobalResetAfter = 24 * time.Hour
	// DefaultFactorLimit and DefaultFactorLockFor: 10 wrong second factors
	// in a row of one person lock the factors of
	// the person for 30 minutes.
	DefaultFactorLimit   = 10
	DefaultFactorLockFor = 30 * time.Minute
)

// ErrInvalidLock reports a scope that is not one of the letters a to z, an
// empty name, a limit below 1, a lock time of 0 or less, a maximum below the
// lock time, or a negative reset time.
var ErrInvalidLock = errors.New("authgate: invalid name lock request")

// ErrNameLocked reports a name that is locked after too many failures in a
// row. A sign-in service answers it with the same error as a wrong
// password, so that the answer does not tell that a user exists.
var ErrNameLocked = errors.New("authgate: the name is locked after failed attempts")

// NameLock is the state of one count. Sub is its sub-key. Failures is the
// count of failures in a row. LockedUntil is the end of the lock, or the
// zero time. Level is the count of locks of the key since the last reset of
// the rule, so the next lock lasts LockFor times 2 to the power of Level, up
// to MaxLockFor.
type NameLock struct {
	LockedUntil time.Time
	Sub         string
	Failures    int
	Level       int
}

// NameLockCounter counts the failures in a row of each name in a scope, so
// that every instance on one database shares one lockout. The name is a
// keyed digest of the user name that the caller computes, or a principal ID
// in ScopeFactor. So the store holds no user name, and an unknown name
// counts the same as a known one. The sub-key splits the counts of one name,
// for example by source prefix, so that an administrator can list and clear
// every count of one name. The store takes the time of each call from its
// own clock.
//
// The caller adds a failure before it compares the credential, and clears a
// count only after the commit of a full sign-in. A failed clear leaves the
// count, which is the safe direction. So of calls at the same time, no more
// than the limit pass. The conformance suite RunNameLocks in package
// authgatetest checks an implementation.
type NameLockCounter interface {
	// AddNameFailure adds one failure to the count of name and sub in scope,
	// in one atomic write, and applies rule. The add that takes the count to
	// rule.Limit passes and sets the lock. An add while the lock holds counts
	// nothing and returns ErrNameLocked. An add after the lock ended starts
	// a new count at 1 and keeps the level. It returns ErrInvalidLock for a
	// request that LockRule or the scope refuses, and an error that is not
	// ErrNameLocked when the write fails. The caller then refuses the
	// attempt.
	AddNameFailure(ctx context.Context, scope, name, sub string, rule LockRule) (NameLock, error)
	// ClearNameFailures sets the count of name and sub in scope to 0 and
	// ends its lock. The level stays, so a lock soon after still doubles. A
	// count that does not exist is no error.
	ClearNameFailures(ctx context.Context, scope, name, sub string) error
	// ClearName ends every count and lock of name in scope, for each
	// sub-key, and returns how many it cleared. An unlock by an
	// administrator calls it.
	ClearName(ctx context.Context, scope, name string) (int64, error)
	// NameLocks returns every count of name in scope, one for each sub-key,
	// in the order of the sub-key. A name with no count returns no entry.
	NameLocks(ctx context.Context, scope, name string) ([]NameLock, error)
	// PruneNameLocks deletes each count whose last failure is age or longer
	// ago, whose lock ended and whose level is past its reset time, and
	// returns how many it deleted.
	PruneNameLocks(ctx context.Context, age time.Duration) (int64, error)
}
