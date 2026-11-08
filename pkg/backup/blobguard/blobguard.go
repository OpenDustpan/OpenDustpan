// Package blobguard is the guard of the blob deletes. A backup snapshot, and a
// restore, can name a blob that a deleter is about to delete. A deleter asks
// this package whether a backup or a restore runs, and deletes nothing while
// one does. The package imports no domain package, so every deleter can use
// it.
package blobguard

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Kind names the kind of work that holds blob deletes back.
type Kind string

const (
	// Backup is a backup row in the running state.
	Backup Kind = "backup"
	// Restore is a restores row in the checking or applying state.
	Restore Kind = "restore"
)

// Work names one backup or restore that runs. ID is the backup ID (Backup)
// or the restore ID (Restore).
type Work struct {
	Kind Kind
	ID   string
}

// String returns the kind and the ID, for example "backup bkp-...".
func (w Work) String() string {
	return string(w.Kind) + " " + w.ID
}

var (
	// ErrWait reports a blob delete that did not run because a backup or a
	// restore runs. The blob stays, and a later sweep or delete of the same
	// owner tries again. Use errors.As with *WaitError for the running work.
	ErrWait = errors.New("blob deletes wait for a running backup or restore")
	// ErrNoWatch reports a deleter without the guard. A deleter that has no
	// guard deletes nothing.
	ErrNoWatch = errors.New("no guard for blob deletes")
	// ErrUnknown reports an answer of the guard that names a kind of work that
	// this package does not know, or no ID. The deleter deletes nothing.
	ErrUnknown = errors.New("unknown kind of running work")
	// ErrNoHolder reports a lease take for a deleter that names no holder. A
	// lease names the deleter that holds it, so a deleter without a name
	// takes no lease and deletes nothing.
	ErrNoHolder = errors.New("no holder for the blob delete lease")
)

// WaitError is the error of a blob delete that waits for Work. It matches
// ErrWait.
type WaitError struct {
	Work Work
}

// Error implements error. The text names the work and no blob key.
func (e *WaitError) Error() string {
	return fmt.Sprintf("%s: %s", ErrWait, e.Work)
}

// Is reports whether target is ErrWait.
func (e *WaitError) Is(target error) bool {
	return target == ErrWait
}

// WaitReason names why a blob delete did not run. It is the label value of
// the counter of the blob deletes that wait.
type WaitReason string

const (
	// WaitBackup counts a delete that waits for a backup.
	WaitBackup WaitReason = "backup"
	// WaitRestore counts a delete that waits for a restore.
	WaitRestore WaitReason = "restore"
	// WaitNoGuard counts a delete of a deleter that has no guard.
	WaitNoGuard WaitReason = "no-guard"
	// WaitGuardError counts a delete that a failed take, an unknown answer or
	// another guard failure kept back.
	WaitGuardError WaitReason = "guard-error"
)

// WaitCounter counts each refused take of a blob delete lease by reason. A
// deleter calls it with ReasonOf(err) after HoldLease refuses the take. A nil
// WaitCounter counts nothing: the caller tests it before the call.
type WaitCounter func(reason WaitReason)

// ReasonOf returns the reason of the refused take that HoldLease answered
// with err: the running work for a *WaitError, WaitNoGuard for ErrNoWatch,
// and WaitGuardError for every other error, including a nil error and a
// *WaitError with a kind that this package does not know. So an unknown
// answer counts as a guard failure: fail closed.
func ReasonOf(err error) WaitReason {
	if wait, ok := errors.AsType[*WaitError](err); ok {
		switch wait.Work.Kind {
		case Backup:
			return WaitBackup
		case Restore:
			return WaitRestore
		default:
			return WaitGuardError
		}
	}

	if errors.Is(err, ErrNoWatch) {
		return WaitNoGuard
	}

	return WaitGuardError
}

// Watch is the guard of the blob deletes. A deleter of a blob that a backup
// snapshot can name, or that restored data can name, calls it after the
// commit that removed or replaced the last row that names the blob, and
// before the delete. The rule: the deleter deletes only when a read that
// starts after that commit finds nothing running. A deleter that lists blobs
// and reads the keys that rows name calls the guard after both reads.
type Watch interface {
	// Running returns the backup in the running state, or else the restore in
	// the checking or applying state, and true. It returns false when no such
	// work runs. It reads the backups table and the restores table in one
	// statement. It defines no sentinel error: a failed read is an error, and
	// the deleter deletes nothing.
	Running(ctx context.Context) (Work, bool, error)
}

// LeaseTTL is the time that one lease of a blob deleter holds back a restore.
// A deleter takes a lease around the delete of one blob, so this is the
// longest time that one blob delete holds a restore back. A deleter that
// stops leaves its row behind, and the row ends on its own, so a deleter that
// stopped holds a restore back at most this long.
const LeaseTTL = time.Minute

// Leases is the lease of the blob deletes. The guard of Watch holds a deleter
// back that reads it while a backup runs or a restore checks or applies. It
// does not hold back a deleter that read the guard before the restore
// started, and then stalls: the restore applies the data, checks the blobs
// after the apply, and the deleter deletes after that check.
//
// A deleter takes a lease before each delete, and releases it after. A take
// writes nothing and answers the running work while a backup runs or a
// restore checks or applies. A restore that applies data waits for zero live
// leases. So a deleter that stalls past the read of the guard holds the
// restore back, and it deletes nothing after a refused take.
type Leases interface {
	// TakeLease writes the lease of holder, ending at the database clock plus
	// ttl, in one statement. It writes nothing and returns the running work
	// and true when a backup runs or a restore checks or applies. A take for
	// a holder with a row replaces that row. It defines no sentinel error: a
	// failed write is an error, and the deleter deletes nothing.
	TakeLease(ctx context.Context, holder string, ttl time.Duration) (Work, bool, error)
	// ReleaseLease deletes the lease row of holder. A holder with no row is
	// not an error. It defines no sentinel error: a failed write is an error.
	ReleaseLease(ctx context.Context, holder string) error
	// LiveLeases returns the number of lease rows that end after the database
	// clock. It defines no sentinel error: a failed read is an error.
	LiveLeases(ctx context.Context) (int, error)
}

// HoldLease takes the lease of holder and returns the function that releases
// it. It returns a *WaitError when a backup or a restore runs, ErrNoWatch
// when l is nil, ErrNoHolder when holder is empty, and the error of the
// write, wrapped, when the write fails. An answer with an unknown kind or an
// empty ID returns ErrUnknown. So every result but a release function means:
// delete nothing.
func HoldLease(ctx context.Context, l Leases, holder string) (func(context.Context) error, error) {
	if l == nil {
		return nil, ErrNoWatch
	}

	// A lease names the deleter that holds it. An empty holder takes no
	// lease: its row would hold back every restore for the whole TTL, and no
	// record would name who took it.
	if holder == "" {
		return nil, ErrNoHolder
	}

	work, ok, err := l.TakeLease(ctx, holder, LeaseTTL)
	if err != nil {
		return nil, fmt.Errorf("take the blob delete lease: %w", err)
	}

	if !ok {
		return func(ctx context.Context) error {
			if err := l.ReleaseLease(ctx, holder); err != nil {
				return fmt.Errorf("release the blob delete lease: %w", err)
			}

			return nil
		}, nil
	}

	// Defense in depth: the adapters return the two kinds only. This check
	// stays so that a new adapter or a fake cannot turn an unknown answer
	// into permission to delete.
	if (work.Kind != Backup && work.Kind != Restore) || work.ID == "" {
		return nil, ErrUnknown
	}

	return nil, &WaitError{Work: work}
}
