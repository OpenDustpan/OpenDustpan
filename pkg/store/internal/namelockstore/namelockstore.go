// Package namelockstore holds the logic of the lockout by name that both SQL
// adapters of authgate.NameLockCounter share. Each adapter gives a Tx over
// its own queries, in one transaction, and this package decides the count,
// the lock and the level. The time comes from the clock of the store.
package namelockstore

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
)

// KeyBytes is the length of the key of the digests of the names.
const KeyBytes = 32

// ErrKey reports a stored key of a length other than KeyBytes. The caller
// fails closed on it.
var ErrKey = errors.New("namelockstore: the stored key of the names is not valid")

// Row is the stored state of one count.
type Row struct {
	Sub           string
	Failures      int64
	LockedUntilMs int64
	Level         int64
	LastFailureMs int64
	LastLockEndMs int64
	ResetAfterMs  int64
}

// Tx is the part of one transaction of an adapter that Add needs.
type Tx interface {
	// NowMs reads the clock of the store, in Unix milliseconds.
	NowMs(ctx context.Context) (int64, error)
	// Ensure makes the row of scope, name and sub when it does not exist.
	Ensure(ctx context.Context, scope, name, sub string) error
	// Get reads the row of scope, name and sub. In PostgreSQL it locks the
	// row until the end of the transaction.
	Get(ctx context.Context, scope, name, sub string) (Row, error)
	// Set writes r as the row of scope and name.
	Set(ctx context.Context, scope, name string, r Row) error
	// AppendAudit appends e to the audit log of the server scope, in the
	// transaction. It returns audit.ErrInvalidEntry for an entry that fails
	// validation.
	AppendAudit(ctx context.Context, e audit.Entry) error
	// HasNotification reports whether the outbox of the server scope holds a
	// notification with the dedupe key.
	HasNotification(ctx context.Context, dedupeKey string) (bool, error)
	// AppendNotification appends n to the outbox of the server scope, in the
	// transaction. The outbox skips a notification whose dedupe key it holds.
	AppendNotification(ctx context.Context, n notify.Notification) error
}

// Add adds one failure to the count of f under its rule, in
// the transaction of tx. It appends the records of ev in the same
// transaction: the entry of the end of an earlier lock, and the entry and the
// alert of a new lock. A record that cannot be appended fails the add, and
// the adapter rolls the transaction back. An add while the lock holds counts nothing and
// returns authgate.ErrNameLocked. An add after the lock ended starts a new
// count at 1. A level past the reset time of its rule goes back to 0. The add
// that takes the count to the limit sets the lock and passes.
func Add(ctx context.Context, tx Tx, f authgate.NameFailure) (authgate.NameLock, error) {
	scope, name, sub, rule, ev := f.Scope, f.Name, f.Sub, f.Rule, f.Events

	// Defense in depth: the service checks the request too. This check stays
	// so that no caller writes a row with a scope, a name or a rule that the
	// port refuses.
	if checkErr := authgate.CheckLockRequest(scope, name, sub, rule); checkErr != nil {
		return authgate.NameLock{}, checkErr
	}

	// Defense in depth: the service builds the events too. This check stays
	// so that no caller sets a lock that has no record.
	if evErr := ev.Validate(); evErr != nil {
		return authgate.NameLock{}, evErr
	}

	now, err := tx.NowMs(ctx)
	if err != nil {
		return authgate.NameLock{}, fmt.Errorf("read the clock of the store: %w", err)
	}

	if ensureErr := tx.Ensure(ctx, scope, name, sub); ensureErr != nil {
		return authgate.NameLock{}, fmt.Errorf("make the name count: %w", ensureErr)
	}

	r, err := tx.Get(ctx, scope, name, sub)
	if err != nil {
		return authgate.NameLock{}, fmt.Errorf("read the name count: %w", err)
	}

	if r.LockedUntilMs > now {
		return Lock(r), authgate.ErrNameLocked
	}

	if r.LockedUntilMs != 0 {
		// An earlier lock ended, and this is the first add that sees it.
		ended := authgate.EndedEntry(ev.Ended, scope, codec.FromMillis(r.LockedUntilMs))
		if auditErr := tx.AppendAudit(ctx, ended); auditErr != nil {
			return authgate.NameLock{}, fmt.Errorf("append the entry of the end of the lock: %w", auditErr)
		}
	}

	next := step(r, now, rule)

	if setErr := tx.Set(ctx, scope, name, next); setErr != nil {
		return authgate.NameLock{}, fmt.Errorf("write the name count: %w", setErr)
	}

	lock := Lock(next)

	if next.LockedUntilMs != 0 {
		// This add set the lock: it appends the entry and the alert of the
		// lock before the transaction commits.
		if recordErr := appendLock(ctx, tx, f, lock, now); recordErr != nil {
			return authgate.NameLock{}, recordErr
		}
	}

	return lock, nil
}

// appendLock appends the entry and the alert of the new lock of k.
func appendLock(ctx context.Context, tx Tx, f authgate.NameFailure, lock authgate.NameLock, nowMs int64) error {
	ev := f.Events

	if err := tx.AppendAudit(ctx, authgate.LockedEntry(ev.Locked, f.Scope, lock)); err != nil {
		return fmt.Errorf("append the entry of the lock: %w", err)
	}

	at := codec.FromMillis(nowMs)

	alert := authgate.LockAlert(ev, f.Scope, f.Name, lock, at).Fit()
	if err := alert.Validate(); err != nil {
		return fmt.Errorf("the alert of the lock: %w", err)
	}

	// One name gets at most one alert of a lock by source prefix or by factor
	// in the hour before this add. The audit entry above still records the
	// lock.
	for _, key := range authgate.AlertWindowKeys(f.Scope, f.Name, at) {
		held, err := tx.HasNotification(ctx, key)
		if err != nil {
			return fmt.Errorf("read the alerts of the name: %w", err)
		}

		if held {
			return nil
		}
	}

	if err := tx.AppendNotification(ctx, alert); err != nil {
		return fmt.Errorf("append the alert of the lock: %w", err)
	}

	return nil
}

// step returns the row after one failure at now under rule, for a row whose
// lock does not hold at now.
func step(r Row, now int64, rule authgate.LockRule) Row {
	if r.LockedUntilMs != 0 {
		// The lock ended: a new count starts, and the level stays.
		r.Failures, r.LockedUntilMs = 0, 0
	}

	if r.Level > 0 && now >= r.LastLockEndMs+r.ResetAfterMs {
		r.Level = 0
	}

	r.Failures++
	r.LastFailureMs = now
	r.ResetAfterMs = rule.ResetAfter.Milliseconds()

	if r.Failures >= int64(rule.Limit) {
		until := now + rule.LockDuration(int(r.Level)).Milliseconds()
		r.LockedUntilMs, r.LastLockEndMs = until, until
		r.Level++
	}

	return r
}

// Lock returns the state of r for the port.
func Lock(r Row) authgate.NameLock {
	until := time.Time{}
	if r.LockedUntilMs != 0 {
		until = codec.FromMillis(r.LockedUntilMs)
	}

	return authgate.NameLock{LockedUntil: until, Sub: r.Sub, Failures: int(r.Failures), Level: int(r.Level)}
}

// NewKey returns a new key of the digests of the names from crypto/rand.
func NewKey() ([]byte, error) {
	b := make([]byte, KeyBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("random key: %w", err)
	}

	return b, nil
}

// CheckKey returns ErrKey for a key of another length than KeyBytes.
func CheckKey(key []byte) error {
	if len(key) != KeyBytes {
		return fmt.Errorf("%w: %d bytes", ErrKey, len(key))
	}

	return nil
}
