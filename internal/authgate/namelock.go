package authgate

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
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
	// ClearName ends every count and lock of name in each scope of scopes,
	// for each sub-key, and appends e with the change "cleared", the count
	// of the counts that it ended, in one transaction, and returns that
	// count. An unlock by an administrator calls it, so the unlock and its
	// audit entry commit together or not at all. It returns ErrInvalidLock
	// for a request that CheckClearRequest refuses, and
	// audit.ErrInvalidEntry for an entry that fails validation; both change
	// nothing.
	ClearName(ctx context.Context, scopes []string, name string, e audit.Entry) (int64, error)
	// NameLocks returns every count of name in scope, one for each sub-key,
	// in the order of the sub-key. A name with no count returns no entry.
	NameLocks(ctx context.Context, scope, name string) ([]NameLock, error)
	// PruneNameLocks deletes each count whose last failure is age or longer
	// ago, whose lock ended and whose level is past its reset time, and
	// returns how many it deleted.
	PruneNameLocks(ctx context.Context, age time.Duration) (int64, error)
}

// Limits of the keys of a name count. A name is a keyed digest or a
// principal ID, and a sub-key is a source prefix, so both are short.
const (
	MaxLockNameBytes = 256
	MaxLockSubBytes  = 256
)

// CheckLockRequest returns ErrInvalidLock for a scope that is empty or holds
// another character than the letters a to z, an empty name, a name or a
// sub-key above its limit or with a control character, and a rule that
// LockRule.Validate refuses. Each adapter of NameLockCounter calls it before
// it writes, so a request that it refuses changes nothing.
func CheckLockRequest(scope, name, sub string, rule LockRule) error {
	switch {
	case scope == "" || !validScope(scope):
		return fmt.Errorf("%w: scope %q", ErrInvalidLock, scope)
	case name == "" || len(name) > MaxLockNameBytes || hasControl(name):
		return fmt.Errorf("%w: a name of %d bytes", ErrInvalidLock, len(name))
	case len(sub) > MaxLockSubBytes || hasControl(sub):
		return fmt.Errorf("%w: a sub-key of %d bytes", ErrInvalidLock, len(sub))
	default:
		return rule.Validate()
	}
}

// CheckClearRequest returns ErrInvalidLock for a clear of a name with no
// scope, a scope that CheckLockRequest refuses, or a name that it refuses.
// Each adapter of NameLockCounter calls it before ClearName writes, so a
// request that it refuses changes nothing.
func CheckClearRequest(scopes []string, name string) error {
	if len(scopes) == 0 {
		return fmt.Errorf("%w: a clear with no scope", ErrInvalidLock)
	}

	for _, scope := range scopes {
		if err := CheckLockRequest(scope, name, "", PasswordRule()); err != nil {
			return err
		}
	}

	return nil
}

// ClearedChange returns the change of the entry of a clear of a name: the
// count of the counts that the clear ended.
func ClearedChange(n int64) audit.Change {
	return audit.Change{Key: "cleared", Old: nil, New: strconv.FormatInt(n, 10), Layer: "", TakesEffect: ""}
}

// Validate returns ErrInvalidLock for a limit below 1, a lock time of 0 or
// less, a maximum below the lock time, or a negative reset time.
func (r LockRule) Validate() error {
	switch {
	case r.Limit < 1:
		return fmt.Errorf("%w: limit %d", ErrInvalidLock, r.Limit)
	case r.LockFor <= 0:
		return fmt.Errorf("%w: lock time %s", ErrInvalidLock, r.LockFor)
	case r.MaxLockFor < r.LockFor:
		return fmt.Errorf("%w: maximum %s below the lock time %s", ErrInvalidLock, r.MaxLockFor, r.LockFor)
	case r.ResetAfter < 0:
		return fmt.Errorf("%w: reset time %s", ErrInvalidLock, r.ResetAfter)
	default:
		return nil
	}
}

// LockDuration returns how long the lock at level lasts under r: LockFor
// times 2 to the power of level, and never more than MaxLockFor.
func (r LockRule) LockDuration(level int) time.Duration {
	d := r.LockFor

	for range level {
		if d >= r.MaxLockFor/2 {
			return r.MaxLockFor
		}

		d *= 2
	}

	return min(d, r.MaxLockFor)
}

// hasControl reports a byte below 0x20 or the byte 0x7f.
func hasControl(s string) bool {
	for i := range len(s) {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}

	return false
}

// PasswordRule is the rule of the count of one name from one source prefix.
func PasswordRule() LockRule {
	return LockRule{
		Limit: DefaultNameLimit, LockFor: DefaultNameLockFor, MaxLockFor: DefaultNameLockFor, ResetAfter: 0,
	}
}

// PasswordNameRule is the rule of the count of one name from every source.
func PasswordNameRule() LockRule {
	return LockRule{
		Limit: DefaultNameGlobalLimit, LockFor: DefaultNameGlobalLockFor, MaxLockFor: DefaultNameGlobalMaxLockFor,
		ResetAfter: DefaultNameGlobalResetAfter,
	}
}
