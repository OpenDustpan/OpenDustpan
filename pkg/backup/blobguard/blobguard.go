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

// Hold calls w and returns nil only when nothing runs. It returns a
// *WaitError when a backup or a restore runs, ErrNoWatch when w is nil, and
// the error of the read, wrapped, when the read fails. An answer with an
// unknown kind or an empty ID returns ErrUnknown. So every result but nil
// means: delete nothing.
func Hold(ctx context.Context, w Watch) error {
	if w == nil {
		return ErrNoWatch
	}

	work, ok, err := w.Running(ctx)
	if err != nil {
		return fmt.Errorf("read running backup or restore: %w", err)
	}

	if !ok {
		return nil
	}

	// Defense in depth: the adapters return the two kinds only. This check
	// stays so that a new adapter or a fake cannot turn an unknown answer
	// into permission to delete.
	if (work.Kind != Backup && work.Kind != Restore) || work.ID == "" {
		return ErrUnknown
	}

	return &WaitError{Work: work}
}
