package authgate

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
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
	// ScopeBreakGlass counts the failed passkey assertions for the passkeys of
	// one break-glass account. The name is the principal ID, and the sub-key
	// is empty. The count only raises an alert: no sign-in reads its lock.
	ScopeBreakGlass = "breakglass"
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

// The prune of the name counts. The server deletes the counts that can no
// longer change a lock, so that failed attempts for many names cannot grow
// the table with no limit.
const (
	// NamePruneInterval is the time between two runs of the prune.
	NamePruneInterval = time.Hour
	// NamePruneAge is how long after its last failure a count can go: the
	// longest lock plus the reset time of the global rule. The store deletes
	// a count only when its lock ended and its level is past its reset time
	// too.
	NamePruneAge = DefaultNameGlobalMaxLockFor + DefaultNameGlobalResetAfter
	// MaxNamePruneRows is the most counts that one run of the prune deletes.
	// The next run deletes the rest.
	MaxNamePruneRows = 10000
	// MaxNamePruneEnded is the most counts with a lock that ended, and that no
	// attempt saw, that one run of the prune deletes. Each such count gets
	// the audit entry of the end of its lock, so this bound is also the
	// most entries of that kind that one run appends. The next run deletes
	// the rest.
	MaxNamePruneEnded = 1000
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

// NameFailure is one failure that AddNameFailure adds: the count that it
// names, the rule that applies to the count, and the records that the add
// writes.
type NameFailure struct {
	// Events holds the records of the add. It is required.
	Events *LockEvents
	// Scope, Name and Sub name the count.
	Scope string
	Name  string
	Sub   string
	// Rule says when the count locks, and for how long.
	Rule LockRule
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
	// AddNameFailure adds one failure to the count of f.Name and f.Sub in
	// f.Scope, in one atomic write, and applies f.Rule. The add that takes the count to
	// rule.Limit passes and sets the lock. An add while the lock holds counts
	// nothing and returns ErrNameLocked. An add after the lock ended starts
	// a new count at 1 and keeps the level.
	//
	// The same transaction appends the records of f.Events. The add that
	// sets a lock appends the entry of Locked and the alert of Alert (see
	// LockedEntry and LockAlert). The add that finds that an earlier lock
	// ended appends the entry of Ended (see EndedEntry). If a record
	// cannot be appended, the transaction rolls back and the add returns the
	// error: the count does not change, and the caller refuses the attempt.
	// So a lock never exists without its record.
	//
	// It returns ErrInvalidLock for a request that LockRule or the scope
	// refuses, and for events that LockEvents.Validate refuses, also nil
	// events. It returns
	// an error that is not ErrNameLocked when the write fails. The caller
	// then refuses the attempt.
	AddNameFailure(ctx context.Context, f NameFailure) (NameLock, error)
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
	// PruneNameLocks deletes at most limit counts whose last failure is age
	// or longer ago, whose lock ended and whose level is past its reset
	// time, and returns how many it deleted. Of these, at most endedLimit
	// are counts whose lock ended and that no attempt saw: each of them gets
	// the audit entry of the end of its lock (see EndedEntry, built from e
	// with the action ActionNameLockEnded). The same transaction appends e
	// with the changes of PrunedChanges. The scheduled prune of the server
	// calls it, so each run, its entries of the end of a lock and its audit
	// entry commit together or not at all. It returns ErrInvalidLock for a
	// request that CheckPruneRequest refuses, and audit.ErrInvalidEntry for
	// an entry that fails validation; both change nothing.
	PruneNameLocks(ctx context.Context, age time.Duration, limit, endedLimit int, e audit.Entry) (int64, error)
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

// CheckPruneRequest returns ErrInvalidLock for a prune with an age of 0 or
// less, with a limit below 1 or above MaxNamePruneRows, or with an ended
// limit below 1, above MaxNamePruneEnded or above the limit. Each adapter of NameLockCounter
// calls it before PruneNameLocks writes, so a request that it refuses changes
// nothing.
func CheckPruneRequest(age time.Duration, limit, endedLimit int) error {
	switch {
	case age <= 0:
		return fmt.Errorf("%w: prune age %s", ErrInvalidLock, age)
	case limit < 1 || limit > MaxNamePruneRows:
		return fmt.Errorf("%w: prune limit %d", ErrInvalidLock, limit)
	case endedLimit < 1 || endedLimit > MaxNamePruneEnded:
		return fmt.Errorf("%w: prune limit of ended locks %d", ErrInvalidLock, endedLimit)
	case endedLimit > limit:
		return fmt.Errorf("%w: prune limit of ended locks %d above the limit %d", ErrInvalidLock, endedLimit, limit)
	default:
		return nil
	}
}

// PrunedChange returns the change of the entry of a prune: the count of the
// counts that the prune deleted.
func PrunedChange(n int64) audit.Change {
	return audit.Change{Key: "pruned", Old: nil, New: strconv.FormatInt(n, 10), Layer: "", TakesEffect: ""}
}

// PrunedChanges returns the changes of the entry of a prune: the count of the
// counts that the prune deleted, and how many of them had a lock that ended
// and that no attempt saw. Each of those has its own entry of the end of a
// lock.
func PrunedChanges(deleted, ended int64) []audit.Change {
	return []audit.Change{
		PrunedChange(deleted),
		{Key: "locksEnded", Old: nil, New: strconv.FormatInt(ended, 10), Layer: "", TakesEffect: ""},
	}
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

// BreakGlassRule is the rule of the count of the failed assertions for one
// break-glass account: 10 failures make the alert, and the count then waits an
// hour before it counts again, so that one account gets at most one alert in
// each hour. The lock of the count stops no sign-in.
func BreakGlassRule() LockRule {
	return LockRule{Limit: BreakGlassAlertLimit, LockFor: AlertWindow, MaxLockFor: AlertWindow, ResetAfter: 0}
}

// BreakGlassAlertLimit is the count of failed assertions for one break-glass
// account that raises the alert.
const BreakGlassAlertLimit = 10

// PasswordNameRule is the rule of the count of one name from every source.
func PasswordNameRule() LockRule {
	return LockRule{
		Limit: DefaultNameGlobalLimit, LockFor: DefaultNameGlobalLockFor, MaxLockFor: DefaultNameGlobalMaxLockFor,
		ResetAfter: DefaultNameGlobalResetAfter,
	}
}

// changeScope is the key of the change and of the attribute that name the
// scope of a count.
const changeScope = "scope"

// AlertWindow is the time in which one name gets at most one alert of a lock
// by source prefix or by factor. The window is the hour before the add.
const AlertWindow = time.Hour

// Audit actions and reasons of the records that a name count writes.
const (
	// ActionNameLocked is the action of the entry of a new lock.
	ActionNameLocked = "sign-in.name-locked"
	// ActionNameLockEnded is the action of the entry of the end of a lock
	// by time. A prune of a count whose end no add saw also writes it, as a
	// change of the entry of the prune.
	ActionNameLockEnded = "sign-in.name-lock-ended"
)

// LockEvents are the records that AddNameFailure appends in its transaction.
// The caller gives the parts that it knows: who, from where, and which
// object. The store adds the parts that only the add knows: the scope, the
// end of the lock, the level and the key of the alert.
type LockEvents struct {
	// Locked is the entry of a new lock. Its action is ActionNameLocked.
	Locked audit.Entry
	// Ended is the entry of the end of an earlier lock. Its action is
	// ActionNameLockEnded.
	Ended audit.Entry
	// Alert is the alert to the administrators of a new lock. Its type is
	// notify.SignInLocked.
	Alert notify.Notification
}

// Validate returns ErrInvalidLock for events that miss an action or a type,
// or that name another action or type than the ones above. A caller that
// gives no events could write a lock without a record, so the store refuses
// it.
func (ev *LockEvents) Validate() error {
	switch {
	case ev == nil:
		return fmt.Errorf("%w: no events for the records of a lock", ErrInvalidLock)
	case ev.Locked.Action != ActionNameLocked:
		return fmt.Errorf("%w: the entry of a lock has the action %q", ErrInvalidLock, ev.Locked.Action)
	case ev.Ended.Action != ActionNameLockEnded:
		return fmt.Errorf("%w: the entry of the end of a lock has the action %q", ErrInvalidLock, ev.Ended.Action)
	case ev.Alert.Type != notify.SignInLocked:
		return fmt.Errorf("%w: the alert of a lock has the type %q", ErrInvalidLock, ev.Alert.Type)
	default:
		return nil
	}
}

// LockedEntry returns the entry of a new lock in scope: the base entry with
// the changes scope, lockedUntil and level.
func LockedEntry(base audit.Entry, scope string, lock NameLock) audit.Entry {
	e := base
	e.Changes = []audit.Change{
		{Key: changeScope, Old: nil, New: scope, Layer: "", TakesEffect: ""},
		{Key: "lockedUntil", Old: nil, New: lock.LockedUntil.UTC().Format(time.RFC3339), Layer: "", TakesEffect: ""},
		{Key: "level", Old: nil, New: strconv.Itoa(lock.Level), Layer: "", TakesEffect: ""},
	}

	return e
}

// EndedEntry returns the entry of the end of a lock in scope: the base entry
// with the changes scope and endedAt.
func EndedEntry(base audit.Entry, scope string, endedAt time.Time) audit.Entry {
	e := base
	e.Changes = []audit.Change{
		{Key: changeScope, Old: nil, New: scope, Layer: "", TakesEffect: ""},
		{Key: "endedAt", Old: nil, New: endedAt.UTC().Format(time.RFC3339), Layer: "", TakesEffect: ""},
	}

	return e
}

// ReasonLockEndedUnseen is the reason of the entry of the end of a lock that
// a prune writes: no attempt saw the end.
const ReasonLockEndedUnseen = "the lock ended and no attempt for the name followed"

// PrunedLockEndedEntry returns the entry of the end of a lock that a prune
// deletes with its count: the entry e of the prune, with the action
// ActionNameLockEnded, the reason ReasonLockEndedUnseen, and the changes scope
// and endedAt.
func PrunedLockEndedEntry(e audit.Entry, scope string, endedAt time.Time) audit.Entry {
	end := EndedEntry(e, scope, endedAt)
	end.Action = ActionNameLockEnded
	end.Reason = ReasonLockEndedUnseen

	return end
}

// AlertKey returns the dedupe key of the alert of a lock. The outbox keeps one
// notification for each scope and dedupe key, so no two alerts share a key.
//
// A lock of the name from every source gets a key with the end of the lock.
// A lock by source prefix or by factor gets a key with the minute of at. The
// name is a keyed digest or a principal ID, so the key holds no user name.
func AlertKey(scope, name string, lock NameLock, at time.Time) string {
	if scope == ScopePasswordName {
		return string(notify.SignInLocked) + "/" + scope + "/" + name + "/" +
			strconv.FormatInt(lock.LockedUntil.UnixMilli(), 10)
	}

	return alertMinuteKey(scope, name, at.UnixMilli()/alertStep.Milliseconds())
}

// alertStep is the step of the minute number in the key of an alert.
const alertStep = time.Minute

// alertMinuteKey returns the key of an alert of scope and name in the minute
// with the number minute.
func alertMinuteKey(scope, name string, minute int64) string {
	return string(notify.SignInLocked) + "/" + scope + "/" + name + "/" + strconv.FormatInt(minute, 10)
}

// AlertWindowKeys returns the keys of the alerts of scope and name that make
// a new alert too early: one key for each minute from AlertWindow before the
// time of the alert up to the minute of the alert. A lock by source prefix or by factor
// sends its alert only when the outbox holds none of these keys, so one name
// gets at most one such alert in any AlertWindow (a rolling hour, counted in
// whole minutes). A lock of the name from every source alerts at once, and
// its window is empty.
func AlertWindowKeys(scope, name string, at time.Time) []string {
	if scope == ScopePasswordName {
		return nil
	}

	now := at.UnixMilli() / alertStep.Milliseconds()
	span := int64(AlertWindow / alertStep)
	keys := make([]string, 0, span+1)

	for minute := now - span; minute <= now; minute++ {
		keys = append(keys, alertMinuteKey(scope, name, minute))
	}

	return keys
}

// LockAlert returns the alert of a new lock in scope: the alert of ev with
// the dedupe key of AlertKey and the attributes scope, object, locked-until
// and level. The object is the object of the entry of the lock, so an
// administrator finds the person in the audit log. The alert holds no user
// name.
func LockAlert(ev *LockEvents, scope, name string, lock NameLock, at time.Time) notify.Notification {
	until := lock.LockedUntil.UTC().Format(time.RFC3339)

	n := ev.Alert
	n.At = at
	n.DedupeKey = AlertKey(scope, name, lock, at)
	n.Attributes = map[string]string{
		changeScope:    scope,
		"object":       ev.Locked.Object,
		"locked-until": until,
		"level":        strconv.Itoa(lock.Level),
	}
	n.Summary = "Sign-in lock in scope " + scope + " on " + ev.Locked.Object + " until " + until +
		" after failed attempts."

	if scope == ScopeBreakGlass {
		n.Summary = strconv.Itoa(BreakGlassAlertLimit) + " failed passkey sign-ins for the break-glass account " +
			ev.Locked.Object + ". The account is not locked."
	}

	return n
}
